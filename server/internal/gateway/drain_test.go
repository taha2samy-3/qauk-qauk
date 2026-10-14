package gateway

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/events"
	devicev1 "github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1"
)

// leaveOnClose removes a test client from the hub when it is closed, as the
// socket handlers do, and records when.
func leaveOnClose(g *Gateway, c *client, d *deviceClient, b *browserClient, closed chan<- time.Time) {
	go func() {
		<-c.ctx.Done()
		if d != nil {
			g.hub.RemoveDevice(d)
		}
		if b != nil {
			g.hub.RemoveBrowser(b)
		}
		closed <- time.Now()
	}()
}

// Closes are spread over the drain duration (not all at once), each with
// 1012, and Drain returns as soon as no client is left.
func TestDrainSpreadsClosesAndEndsEarly(t *testing.T) {
	g, _ := testGateway(t)
	g.cfg.DrainDuration = 600 * time.Millisecond
	const n = 30
	closed := make(chan time.Time, n)
	var clients []*client
	for i := range n {
		if i%3 == 0 {
			b := testBrowser(int64(i), uuid.NewString())
			g.hub.AddBrowser(b)
			leaveOnClose(g, b.client, nil, b, closed)
			clients = append(clients, b.client)
			continue
		}
		d := testDevice(uuid.New(), uuid.NewString())
		g.hub.AddDevice(d, nil)
		leaveOnClose(g, d.client, d, nil, closed)
		clients = append(clients, d.client)
	}
	start := time.Now()
	g.Drain(context.Background())
	took := time.Since(start)

	var at []time.Duration
	for range n {
		at = append(at, (<-closed).Sub(start))
	}
	slices.Sort(at)
	if at[0] > 200*time.Millisecond || at[n-1] < 300*time.Millisecond {
		t.Fatalf("closes not spread over the window: first %v, last %v", at[0], at[n-1])
	}
	if took > at[n-1]+200*time.Millisecond {
		t.Fatalf("Drain returned %v after the last client left (%v)", took, at[n-1])
	}
	for _, c := range clients {
		c.mu.Lock()
		code, reason := c.closedCode, c.closedReason
		c.mu.Unlock()
		if code != websocket.StatusCode(1012) || reason != "server restarting" {
			t.Fatalf("client %s closed with %v %q", c.id, code, reason)
		}
	}
	if !g.IsDraining() {
		t.Fatal("draining after Drain")
	}
}

// Clients still there when the duration ends are closed at once.
func TestDrainClosesTheRestAtTheEnd(t *testing.T) {
	g, _ := testGateway(t)
	g.cfg.DrainDuration = 50 * time.Millisecond
	d := testDevice(uuid.New(), "stays") // never leaves the hub
	g.hub.AddDevice(d, nil)
	start := time.Now()
	g.Drain(context.Background())
	if took := time.Since(start); took < 50*time.Millisecond || took > 500*time.Millisecond {
		t.Fatalf("took %v", took)
	}
	select {
	case <-d.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("the remaining client must be closed")
	}
}

func TestDrainAbort(t *testing.T) {
	g, _ := testGateway(t)
	g.cfg.DrainDuration = time.Hour
	d := testDevice(uuid.New(), "d1")
	g.hub.AddDevice(d, nil)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(15*time.Millisecond, cancel)
	start := time.Now()
	g.Drain(ctx)
	if took := time.Since(start); took > 300*time.Millisecond {
		t.Fatalf("Drain ignored the abort: %v", took)
	}
	select {
	case <-d.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("an aborted drain closes everything at once")
	}
}

func TestDrainWithoutClientsAndTwice(t *testing.T) {
	g, _ := testGateway(t)
	g.cfg.DrainDuration = time.Hour
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); g.Drain(context.Background()) }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("with no clients Drain returns at once (and may be called twice)")
	}
}

// Once draining, new gRPC streams are refused (the client reconnects
// elsewhere) and REST long-polls answer at once with Connection: close.
func TestDrainingRefusesStreamsAndEndsLongPolls(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp"})
	tok := td.token(t)
	srv := restServer(t, g, td)

	// a long-poll waiting when the drain starts ends promptly
	type result struct {
		status int
		close  bool // Connection: close (Go's client turns it into resp.Close)
		took   time.Duration
	}
	got := make(chan result, 1)
	go func() {
		start := time.Now()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/device/v1/sync?wait=30s", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			got <- result{}
			return
		}
		_ = resp.Body.Close()
		got <- result{resp.StatusCode, resp.Close, time.Since(start)}
	}()
	time.Sleep(100 * time.Millisecond)
	g.StartDrain()
	r := <-got
	if r.status != http.StatusOK || !r.close || r.took > 2*time.Second {
		t.Fatalf("long-poll during drain: %+v", r)
	}

	client := grpcClient(t, g)
	stream, err := client.Watch(context.Background(), authed(&devicev1.WatchRequest{}, tok))
	if err == nil {
		for stream.Receive() {
		}
		err = stream.Err()
	}
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeUnavailable {
		t.Fatalf("a new stream while draining must be Unavailable, got %v", err)
	}
}

// Messages held over their rate (over_limit=latest) were acknowledged as
// coalesced: stopping publishes them instead of dropping them.
func TestStopFlushesHeldMessages(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp", Rate: rate(0.001), Burst: burst(1), OverLimit: "latest"})
	for _, v := range []int{1, 2} {
		if _, err := g.publishDeviceMessage(td.dev, "o", DeviceMessage{Element: "temp", ByName: true, Message: val(v)}); err != nil {
			t.Fatal(err)
		}
	}
	if pub.count() != 1 || !g.coalesce.pending(td.elem["temp"]) {
		t.Fatalf("want one published and one held, got %d", pub.count())
	}
	g.coalesce.flushAll()
	if msgs := pub.messages(t); len(msgs) != 2 || string(msgs[1].Message) != `{"value":2}` {
		t.Fatalf("held message not published: %d", len(msgs))
	}
}
