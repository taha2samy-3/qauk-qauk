package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/ratelimit"
	"github.com/taha2samy/quackquack/server/internal/registry"
)

type fakePub struct {
	mu  sync.Mutex
	evs []*events.Event
}

func (f *fakePub) Publish(_ context.Context, _ string, ev *events.Event) {
	f.mu.Lock()
	f.evs = append(f.evs, ev)
	f.mu.Unlock()
}

func (f *fakePub) PublishDurable(ctx context.Context, topic string, ev *events.Event, done func(error)) {
	f.Publish(ctx, topic, ev)
	done(nil)
}

func (f *fakePub) PublishRecord(context.Context, *kgo.Record) {}

func (f *fakePub) messages(t *testing.T) []events.ElementMessage {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]events.ElementMessage, len(f.evs))
	for i, ev := range f.evs {
		if err := ev.DecodeData(&out[i]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func (f *fakePub) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.evs)
}

// testGateway is a gateway without database or bus: devices come from the
// registry, events go to a fake publisher.
func testGateway(t *testing.T) (*Gateway, *fakePub) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := &config.Config{GatewayID: "gw-test", DeviceMsgRate: 500, ElementMsgRate: 50, ElementMsgRateMax: 1000,
		SyncMaxWait: 2 * time.Second, PresenceTTL: time.Minute, PresenceHeartbeat: time.Second, DeviceJWTMaxLifetime: 24 * time.Hour}
	pub := &fakePub{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	g := &Gateway{cfg: cfg, producer: pub, log: log, hub: NewHub(), ctx: ctx,
		limits: ratelimit.NewLocal(), dedupe: newDedupeCache(), rest: newRESTPresence(),
		registry: registry.New(func(context.Context, uuid.UUID) (events.DeviceConfig, error) {
			return events.DeviceConfig{}, registry.ErrNotFound
		}, log)}
	g.coalesce = newCoalescer(g)
	g.verifier = &authn.DeviceVerifier{MaxLifetime: cfg.DeviceJWTMaxLifetime, Leeway: 30 * time.Second, Load: g.loadDeviceKey}
	return g, pub
}

type testDev struct {
	dev  *registry.Device
	key  *ecdsa.PrivateKey
	cfg  events.DeviceConfig
	elem map[string]uuid.UUID
}

// addDevice puts a device with an ES256 key and the given elements into the
// registry, as the topic would.
func addDevice(t *testing.T, g *Gateway, elements ...events.ElementConfig) *testDev {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	td := &testDev{key: key, elem: map[string]uuid.UUID{}}
	td.cfg = events.DeviceConfig{Device: events.DeviceInfo{ID: uuid.New(), Name: "dev"}, Version: 1,
		Key: &events.DeviceKey{ID: uuid.New(), PEM: pemText, Algorithm: "ES256", Active: true}}
	for _, e := range elements {
		if e.ID == uuid.Nil {
			e.ID = uuid.New()
		}
		if e.OverLimit == "" {
			e.OverLimit = "drop"
		}
		td.cfg.Elements = append(td.cfg.Elements, e)
		td.elem[e.Name] = e.ID
	}
	td.apply(t, g)
	return td
}

func (td *testDev) apply(t *testing.T, g *Gateway) {
	t.Helper()
	ev, err := events.New(events.TypeDeviceConfig, events.SourceAPI, td.cfg.Device.ID.String(), td.cfg)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(ev)
	g.registry.ApplyRecord([]byte(td.cfg.Device.ID.String()), raw)
	td.dev, _ = g.registry.Lookup(td.cfg.Device.ID)
}

func (td *testDev) token(t *testing.T) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"id": td.cfg.Device.ID.String(),
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()})
	s, err := tok.SignedString(td.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func rate(v float64) *float64 { return &v }
func burst(v int) *int        { return &v }

func val(v int) json.RawMessage { return json.RawMessage(`{"value":` + itoa(v) + `}`) }

func TestCoreResolvesAndValidates(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "temp"})
	cases := []struct {
		in   DeviceMessage
		want error
	}{
		{DeviceMessage{Element: td.elem["temp"].String(), Message: val(1)}, nil},
		{DeviceMessage{Element: "temp", Message: val(1)}, ErrUnknownElement}, // WebSocket: ids only
		{DeviceMessage{Element: "temp", ByName: true, Message: val(1)}, nil},
		{DeviceMessage{Element: uuid.NewString(), ByName: true, Message: val(1)}, ErrUnknownElement},
		{DeviceMessage{Element: "temp", ByName: true}, ErrInvalidMessage},
		{DeviceMessage{Element: "temp", ByName: true, Message: json.RawMessage(`{`)}, ErrInvalidMessage},
	}
	for i, c := range cases {
		_, err := g.publishDeviceMessage(td.dev, "c1", c.in)
		if !errors.Is(err, c.want) {
			t.Errorf("case %d: err %v, want %v", i, err, c.want)
		}
	}
	var us *UnstorableError
	if _, err := g.publishDeviceMessage(td.dev, "c1", DeviceMessage{Element: "temp", ByName: true,
		Message: json.RawMessage(`{"value":"a\u0000b"}`)}); !errors.As(err, &us) {
		t.Errorf("NUL must be unstorable, got %v", err)
	}
	msgs := pub.messages(t)
	if len(msgs) != 2 {
		t.Fatalf("published %d, want 2", len(msgs))
	}
	m := msgs[0]
	if m.Source != events.SourceDevice || m.DeviceID != td.dev.ID || m.Actor.Name != "dev" || m.Origin.ConnID != "c1" {
		t.Fatalf("message envelope wrong: %+v", m)
	}
}

func TestElementLimitDrop(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "fast"}, events.ElementConfig{Name: "slow", Rate: rate(2), Burst: burst(2)})
	for range 2 {
		if _, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "slow", ByName: true, Message: val(1)}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "slow", ByName: true, Message: val(1)})
	var rl *RateLimitedError
	if !errors.As(err, &rl) || rl.Scope != "element" || rl.RetryAfter <= 0 || rl.RetryAfter > 500*time.Millisecond {
		t.Fatalf("3rd message on a 2/s element: %v", err)
	}
	// another element of the same device has its own (default 50/s) bucket
	for i := range 50 {
		if _, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "fast", ByName: true, Message: val(i)}); err != nil {
			t.Fatalf("fast element message %d: %v", i, err)
		}
	}
	if n := pub.count(); n != 52 {
		t.Fatalf("published %d, want 52", n)
	}
}

func TestElementLimitLatestKeepsNewest(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "s", Rate: rate(20), Burst: burst(1), OverLimit: "latest"})
	for i := range 6 {
		res, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "s", ByName: true, Message: val(i)})
		if err != nil {
			t.Fatal(err)
		}
		want := StatusCoalesced
		if i == 0 {
			want = StatusAccepted
		}
		if res.Status != want {
			t.Fatalf("message %d: status %s, want %s", i, res.Status, want)
		}
	}
	if pub.count() != 1 {
		t.Fatalf("published %d before the bucket refilled, want 1", pub.count())
	}
	deadline := time.Now().Add(2 * time.Second)
	for pub.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(120 * time.Millisecond) // nothing else may follow
	msgs := pub.messages(t)
	if len(msgs) != 2 || string(msgs[1].Message) != `{"value":5}` {
		t.Fatalf("want first and newest only, got %d messages, last %s", len(msgs), msgs[len(msgs)-1].Message)
	}
	if g.coalesce.pending(td.elem["s"]) {
		t.Fatal("held message not cleared")
	}
}

func TestClientIDDeduplicates(t *testing.T) {
	g, pub := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "e"})
	a, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "e", ByName: true, Message: val(1), ClientID: "m-1"})
	if err != nil || a.Status != StatusAccepted || a.EventID == "" {
		t.Fatalf("first: %+v %v", a, err)
	}
	b, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "e", ByName: true, Message: val(1), ClientID: "m-1"})
	if err != nil || b.Status != StatusDuplicate || b.EventID != a.EventID {
		t.Fatalf("retry: %+v %v", b, err)
	}
	other := addDevice(t, g, events.ElementConfig{Name: "e"})
	if c, _ := g.publishDeviceMessage(other.dev, "c", DeviceMessage{Element: "e", ByName: true, Message: val(1), ClientID: "m-1"}); c.Status != StatusAccepted {
		t.Fatal("client ids are per device")
	}
	if pub.count() != 2 {
		t.Fatalf("published %d, want 2", pub.count())
	}
}

func TestClientIDOfRateLimitedMessageCanRetry(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "e", Rate: rate(1), Burst: burst(1)})
	if _, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "e", ByName: true, Message: val(1)}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "e", ByName: true, Message: val(2), ClientID: "x"}); err == nil {
		t.Fatal("want rate limited")
	}
	time.Sleep(1100 * time.Millisecond)
	res, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "e", ByName: true, Message: val(2), ClientID: "x"})
	if err != nil || res.Status != StatusAccepted {
		t.Fatalf("retry after refill: %+v %v", res, err)
	}
}

func TestDeviceGuard(t *testing.T) {
	g, _ := testGateway(t)
	g.cfg.DeviceMsgRate = 3
	id := uuid.New()
	if err := g.allowDevice(id, 3); err != nil {
		t.Fatal(err)
	}
	var rl *RateLimitedError
	if err := g.allowDevice(id, 1); !errors.As(err, &rl) || rl.Scope != "device" {
		t.Fatalf("want device limit, got %v", err)
	}
}

func TestLimitChangeAppliesWithoutReconnect(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "e", Rate: rate(1), Burst: burst(1)})
	_, _ = g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "e", ByName: true, Message: val(1)})
	if _, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "e", ByName: true, Message: val(1)}); err == nil {
		t.Fatal("want limited at 1/s")
	}
	td.cfg.Elements[0].Rate, td.cfg.Elements[0].Burst = rate(100), burst(100)
	td.cfg.Version = 2
	td.apply(t, g)
	// the bucket keeps its tokens but now refills at 100/s
	time.Sleep(30 * time.Millisecond)
	if _, err := g.publishDeviceMessage(td.dev, "c", DeviceMessage{Element: "e", ByName: true, Message: val(1)}); err != nil {
		t.Fatalf("after raising the limit: %v", err)
	}
}

func TestRegistryKeepsNewestVersion(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "e"})
	td.cfg.Version = 0 // stale record
	td.cfg.Device.Name = "stale"
	td.apply(t, g)
	if d, _ := g.registry.Lookup(td.cfg.Device.ID); d.Name != "dev" {
		t.Fatal("a stale snapshot replaced a newer one")
	}
	g.registry.ApplyRecord([]byte(td.cfg.Device.ID.String()), nil)
	if _, ok := g.registry.Lookup(td.cfg.Device.ID); ok {
		t.Fatal("tombstone did not delete the device")
	}
}

func TestAuthenticateDeviceFromRegistry(t *testing.T) {
	g, _ := testGateway(t)
	td := addDevice(t, g, events.ElementConfig{Name: "e"})
	dev, dk, err := g.authenticateDevice(context.Background(), td.token(t))
	if err != nil || dev.ID != td.cfg.Device.ID || dk.KeyID != td.cfg.Key.ID {
		t.Fatalf("auth: %v", err)
	}
	td.cfg.Key.Active = false
	td.cfg.Version = 2
	td.apply(t, g)
	if _, _, err := g.authenticateDevice(context.Background(), td.token(t)); err == nil {
		t.Fatal("inactive key must fail")
	}
	if _, _, err := g.authenticateDevice(context.Background(), "garbage"); err == nil {
		t.Fatal("garbage token must fail")
	}
}
