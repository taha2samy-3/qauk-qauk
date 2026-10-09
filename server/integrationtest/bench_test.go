//go:build integration

package integrationtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/coder/websocket"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/taha2samy/quackquack/server/internal/config"
	devicev1 "github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1"
	"github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1/devicev1connect"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// Device transport benchmark: QUACK_IT_BENCH=1 go test -tags integration -run TestTransportBench -v ./integrationtest/
//
// Devices publish on instance A; a dashboard socket on instance B receives
// (so every message crosses Redpanda). Limits are off, the ingester isn't
// running: this measures the transports and the gateway, not the history store.

type benchDev struct {
	id    uuid.UUID
	elem  uuid.UUID
	name  string
	token string
}

func benchFixture(t *testing.T, prefix string, n int) ([]benchDev, func(base string) string) {
	ctx := context.Background()
	s := svc()
	mkUser(t, prefix+"-user", false)
	u, _ := store.GetUserByUsername(ctx, pool, prefix+"-user")
	devs := make([]benchDev, n)
	for i := range devs {
		k := newECKey(t)
		key, err := s.CreateKey(ctx, service.SystemActor, fmt.Sprintf("%s-key-%d", prefix, i), k.pem, nil)
		if err != nil {
			t.Fatal(err)
		}
		d, err := s.CreateDevice(ctx, service.SystemActor, service.DeviceInput{Name: fmt.Sprintf("%s-%d", prefix, i), PublicKeyID: &key.ID})
		if err != nil {
			t.Fatal(err)
		}
		e, err := s.CreateElement(ctx, service.SystemActor, service.ElementInput{DeviceID: d.ID, Name: "v", Points: 0})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetPermission(ctx, service.SystemActor, service.PermissionInput{ElementID: e.ID, UserID: &u.ID, Permission: "R"}); err != nil {
			t.Fatal(err)
		}
		devs[i] = benchDev{id: d.ID, elem: e.ID, name: d.Name,
			token: strings.TrimPrefix(deviceHeader(t, k, d.ID.String()).Get("Authorization"), "Bearer ")}
	}
	return devs, func(base string) string {
		c := newClient(t, base)
		c.login(prefix+"-user", prefix+"-user-password")
		return c.cookieHeader()
	}
}

// sink is a set of dashboard sockets (one per group of devices, so no single
// socket falls behind and gets dropped as a slow consumer) counting element
// messages and timing marked ones.
type sink struct {
	n      atomic.Int64
	closed atomic.Bool
	mu     sync.Mutex
	wait   map[string]chan struct{} // value marker -> waiter
}

func openSink(t *testing.T, base, cookie string, devs []benchDev, sockets int) *sink {
	s := &sink{wait: map[string]chan struct{}{}}
	for k := range sockets {
		var mine []benchDev
		for i := k; i < len(devs); i += sockets {
			mine = append(mine, devs[i])
		}
		s.open(t, base, cookie, mine)
	}
	return s
}

func (s *sink) open(t *testing.T, base, cookie string, devs []benchDev) {
	ctx := context.Background()
	c, _, err := websocket.Dial(ctx, wsURL(base, "/browser/simple/"), &websocket.DialOptions{HTTPHeader: browserHeader(cookie, base)})
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(1 << 20)
	t.Cleanup(func() { _ = c.CloseNow() })
	subscribed := make(chan struct{}, len(devs))
	go func() {
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				s.closed.Store(true)
				return
			}
			if bytes.Contains(data, []byte(`"type":"subscribe"`)) {
				subscribed <- struct{}{}
				continue
			}
			if !bytes.Contains(data, []byte(`"message_element"`)) {
				continue
			}
			s.n.Add(1)
			if i := bytes.Index(data, []byte(`"mark":"`)); i >= 0 {
				rest := data[i+8:]
				mark := string(rest[:bytes.IndexByte(rest, '"')])
				s.mu.Lock()
				if ch, ok := s.wait[mark]; ok {
					close(ch)
					delete(s.wait, mark)
				}
				s.mu.Unlock()
			}
		}
	}()
	for _, d := range devs {
		b, _ := json.Marshal(map[string]any{"type": "subscribe", "element_id": d.elem})
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatal(err)
		}
	}
	for range devs {
		select {
		case <-subscribed:
		case <-time.After(10 * time.Second):
			t.Fatal("subscribe timeout")
		}
	}
}

func (s *sink) expect(mark string) chan struct{} {
	ch := make(chan struct{})
	s.mu.Lock()
	s.wait[mark] = ch
	s.mu.Unlock()
	return ch
}

// publisher sends n messages (one batch) and returns when they're accepted.
type publisher interface {
	publish(ctx context.Context, msgs []map[string]any) error
	close()
}

type wsPub struct{ c *websocket.Conn }

func (p *wsPub) publish(ctx context.Context, msgs []map[string]any) error {
	for _, m := range msgs {
		b, _ := json.Marshal(map[string]any{"element_id": m["element"], "message": m["message"]})
		if err := p.c.Write(ctx, websocket.MessageText, b); err != nil {
			return err
		}
	}
	return nil
}
func (p *wsPub) close() { _ = p.c.CloseNow() }

type restPub struct {
	base, token string
	http        *http.Client
}

func (p *restPub) publish(ctx context.Context, msgs []map[string]any) error {
	b, _ := json.Marshal(msgs)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, p.base+"/device/v1/messages", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Results []struct{ Status, Error string }
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != 200 {
		return fmt.Errorf("status %d: %v", resp.StatusCode, err)
	}
	for _, r := range out.Results {
		if r.Status != "accepted" {
			return fmt.Errorf("message %s: %s", r.Status, r.Error)
		}
	}
	return nil
}
func (p *restPub) close() {}

func toProto(msgs []map[string]any) []*devicev1.DeviceMessage {
	out := make([]*devicev1.DeviceMessage, len(msgs))
	for i, m := range msgs {
		v, _ := structpb.NewValue(m["message"])
		out[i] = &devicev1.DeviceMessage{Element: m["element"].(string), Message: v}
	}
	return out
}

type grpcUnaryPub struct {
	cl    devicev1connect.DeviceServiceClient
	token string
}

func (p *grpcUnaryPub) publish(ctx context.Context, msgs []map[string]any) error {
	req := connect.NewRequest(&devicev1.PublishRequest{Messages: toProto(msgs)})
	req.Header().Set("Authorization", "Bearer "+p.token)
	res, err := p.cl.Publish(ctx, req)
	if err != nil {
		return err
	}
	for _, r := range res.Msg.GetResults() {
		if r.GetStatus() != "accepted" {
			return fmt.Errorf("message %s: %s", r.GetStatus(), r.GetError())
		}
	}
	return nil
}
func (p *grpcUnaryPub) close() {}

type grpcStreamPub struct {
	s *connect.BidiStreamForClient[devicev1.SessionRequest, devicev1.SessionResponse]
}

func (p *grpcStreamPub) publish(_ context.Context, msgs []map[string]any) error {
	for _, m := range toProto(msgs) {
		if err := p.s.Send(&devicev1.SessionRequest{Publish: m}); err != nil {
			return err
		}
	}
	return nil
}
func (p *grpcStreamPub) close() { _ = p.s.CloseRequest() }

type transportCase struct {
	name  string
	batch int
	open  func(ctx context.Context, d benchDev) publisher
}

func TestTransportBench(t *testing.T) {
	if os.Getenv("QUACK_IT_BENCH") == "" {
		t.Skip("set QUACK_IT_BENCH=1")
	}
	noLimits := func(c *config.Config) { c.DeviceMsgRate, c.ElementMsgRate = 0, 0 } // 0 = unlimited
	a := startInstance(t, "gw-bench-a", false, noLimits)
	b := startInstance(t, "gw-bench-b", false, noLimits)
	const devices = 20
	devs, cookie := benchFixture(t, fmt.Sprintf("bench%d", time.Now().Unix()%100000), devices)
	sk := openSink(t, b.URL, cookie(b.URL), devs, 4)
	h1 := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 256}}
	// one HTTP/2 connection per device, like real devices (a shared client
	// would multiplex all of them over a single TCP connection)
	grpcClient := func() devicev1connect.DeviceServiceClient {
		return devicev1connect.NewDeviceServiceClient(h2cClient(), a.URL, connect.WithGRPC())
	}

	cases := []transportCase{
		{"WebSocket (protocol v1)", 1, func(ctx context.Context, d benchDev) publisher {
			c, _, err := websocket.Dial(ctx, wsURL(a.URL, "/device/node_red/"), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + d.token}}})
			if err != nil {
				t.Fatal(err)
			}
			go func() { // drain
				for {
					if _, _, err := c.Read(ctx); err != nil {
						return
					}
				}
			}()
			return &wsPub{c: c}
		}},
		{"REST, 1 message per request", 1, func(_ context.Context, d benchDev) publisher { return &restPub{base: a.URL, token: d.token, http: h1} }},
		{"REST, batches of 100", 100, func(_ context.Context, d benchDev) publisher { return &restPub{base: a.URL, token: d.token, http: h1} }},
		{"gRPC unary Publish, 1 message", 1, func(_ context.Context, d benchDev) publisher { return &grpcUnaryPub{cl: grpcClient(), token: d.token} }},
		{"gRPC unary Publish, batches of 100", 100, func(_ context.Context, d benchDev) publisher { return &grpcUnaryPub{cl: grpcClient(), token: d.token} }},
		{"gRPC Session stream", 1, func(ctx context.Context, d benchDev) publisher {
			s := grpcClient().Session(ctx)
			s.RequestHeader().Set("Authorization", "Bearer "+d.token)
			if err := s.Send(nil); err != nil {
				t.Fatal(err)
			}
			go func() { // drain results
				for {
					if _, err := s.Receive(); err != nil {
						return
					}
				}
			}()
			return &grpcStreamPub{s: s}
		}},
	}

	type row struct {
		name               string
		p50, p95, p99      time.Duration
		sent, got, seconds float64
	}
	var rows []row
	for _, tc := range cases {
		ctx, cancel := context.WithCancel(context.Background())
		pubs := make([]publisher, devices)
		for i, d := range devs {
			pubs[i] = tc.open(ctx, d)
		}
		time.Sleep(300 * time.Millisecond)

		// latency: one message in flight, device on A -> dashboard on B
		lat := make([]time.Duration, 0, 300)
		for i := range 300 {
			mark := fmt.Sprintf("%s-%d", strings.ReplaceAll(tc.name, " ", ""), i)
			ch := sk.expect(mark)
			start := time.Now()
			if err := pubs[0].publish(ctx, []map[string]any{{"element": devs[0].elem.String(), "message": map[string]any{"value": i, "mark": mark}}}); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			select {
			case <-ch:
				lat = append(lat, time.Since(start))
			case <-time.After(5 * time.Second):
				t.Fatalf("%s: message %d not delivered (dashboard socket closed: %v)", tc.name, i, sk.closed.Load())
			}
		}
		slices.Sort(lat)
		pct := func(p float64) time.Duration { return lat[int(p*float64(len(lat)-1))] }

		// throughput: every device sends as fast as its transport allows
		const dur = 5 * time.Second
		before := sk.n.Load()
		var sent atomic.Int64
		var wg sync.WaitGroup
		deadline := time.Now().Add(dur)
		for i, d := range devs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				batch := make([]map[string]any, tc.batch)
				for j := range batch {
					batch[j] = map[string]any{"element": d.elem.String(), "message": map[string]any{"value": j}}
				}
				for time.Now().Before(deadline) {
					if err := pubs[i].publish(ctx, batch); err != nil {
						t.Errorf("%s: %v", tc.name, err) // limits are off: any error is a failure
						return
					}
					sent.Add(int64(tc.batch))
				}
			}()
		}
		wg.Wait()
		// let in-flight messages arrive
		for last := int64(-1); last != sk.n.Load(); {
			last = sk.n.Load()
			time.Sleep(700 * time.Millisecond)
		}
		got := sk.n.Load() - before
		if sk.closed.Load() {
			t.Fatalf("%s: a dashboard socket was closed (slow consumer?)", tc.name)
		}
		rows = append(rows, row{tc.name, pct(0.5), pct(0.95), pct(0.99), float64(sent.Load()), float64(got), dur.Seconds()})
		for _, p := range pubs {
			p.close()
		}
		cancel()
		time.Sleep(500 * time.Millisecond)
	}

	ms := func(d time.Duration) string { return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000) }
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n%d devices on instance A, one dashboard socket on instance B (via Redpanda), limits off, no ingester\n\n", devices)
	sb.WriteString("| Transport | Latency p50 | p95 | p99 | Sent/s | Delivered/s | Delivered |\n|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %.0f | %.0f | %.1f%% |\n", r.name, ms(r.p50), ms(r.p95), ms(r.p99),
			r.sent/r.seconds, r.got/r.seconds, 100*r.got/max(r.sent, 1))
	}
	t.Log(sb.String())
	if out := os.Getenv("QUACK_IT_BENCH_OUT"); out != "" {
		_ = os.WriteFile(out, []byte(sb.String()), 0o644)
	}
}
