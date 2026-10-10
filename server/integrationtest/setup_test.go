//go:build integration

// Package integrationtest runs the real server components in-process against
// real Postgres/TimescaleDB and Redpanda (`task infra:up`). It uses its own
// database (QUACK_IT_DATABASE_URL, default quack_it) and wipes it.
package integrationtest

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/db"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/gateway"
	"github.com/taha2samy/quackquack/server/internal/history"
	_ "github.com/taha2samy/quackquack/server/internal/history/clickhouse"
	_ "github.com/taha2samy/quackquack/server/internal/history/timescale"
	"github.com/taha2samy/quackquack/server/internal/httpserver"
	"github.com/taha2samy/quackquack/server/internal/ingest"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/outbox"
	"github.com/taha2samy/quackquack/server/internal/service"
)

var (
	dbURL   = env("QUACK_IT_DATABASE_URL", "postgres://quack:quack@127.0.0.1:5433/quack_it?sslmode=disable")
	brokers = strings.Split(env("QUACK_KAFKA_BROKERS", "127.0.0.1:19092"), ",")
	pool    *pgxpool.Pool
	logger  = newLogger()
	// The history store under test: QUACK_IT_HISTORY_DRIVER=clickhouse runs the
	// whole suite on ClickHouse (QUACK_IT_HISTORY_URL), the default on Timescale.
	historyDriver = env("QUACK_IT_HISTORY_DRIVER", "timescale")
	historyURL    = os.Getenv("QUACK_IT_HISTORY_URL")
	hist          history.Store
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func TestMain(m *testing.M) {
	ctx := context.Background()
	if err := db.Migrate(ctx, dbURL); err != nil {
		fmt.Println("migrate:", err)
		os.Exit(1)
	}
	// Own topics, emptied every run: the dev stack may share this Redpanda, and
	// compacted topics (device-config.v1) would otherwise grow run after run.
	bus.SetTopicPrefix(env("QUACK_IT_TOPIC_PREFIX", "it."))
	dctx, dcancel := context.WithTimeout(ctx, 30*time.Second)
	if err := bus.DeleteTopics(dctx, brokers); err != nil {
		fmt.Println("delete topics:", err)
		os.Exit(1)
	}
	dcancel()
	if err := bus.EnsureTopics(ctx, brokers); err != nil {
		fmt.Println("topics:", err)
		os.Exit(1)
	}
	var err error
	if pool, err = db.Open(ctx, dbURL); err != nil {
		fmt.Println("db:", err)
		os.Exit(1)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE users, groups, sessions, dashboards, jwt_public_keys, devices, elements,
		element_permissions, device_presence, device_connections, outbox, audit_log, mqtt_connections, decoders,
		gateway_members RESTART IDENTITY CASCADE`); err != nil {
		fmt.Println("truncate:", err)
		os.Exit(1)
	}
	if hist, err = history.Open(ctx, history.ParseTargets(historyDriver, historyURL, dbURL), history.Settings{}); err != nil {
		fmt.Println("history:", err)
		os.Exit(1)
	}
	if err := hist.Migrate(ctx); err != nil {
		fmt.Println("history migrate:", err)
		os.Exit(1)
	}
	if err := hist.Reset(ctx); err != nil {
		fmt.Println("history reset:", err)
		os.Exit(1)
	}
	fmt.Println("history store:", historyDriver)
	code := m.Run()
	_ = hist.Close()
	pool.Close()
	os.Exit(code)
}

// instance is one running `quack serve` (api + gateway + outbox relay).
type instance struct {
	URL  string
	srv  *httptest.Server
	gw   *gateway.Gateway
	stop func() // stops it now (also runs at test end)
}

func startInstance(t *testing.T, gatewayID string, withIngest bool, tweak ...func(*config.Config)) *instance {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := &config.Config{
		DatabaseURL: dbURL, KafkaBrokers: brokers, KafkaAcksAll: true,
		Roles: []string{"api", "gateway"}, GatewayID: gatewayID,
		SessionTTL: time.Hour, DeviceJWTMaxLifetime: 24 * time.Hour, DeviceMsgRate: 1000, BrowserMsgRate: 1000,
		ElementMsgRate: 1000, ElementMsgRateMax: 1000, RateLimitDriver: "local", SyncMaxWait: 10 * time.Second, StreamMaxAge: time.Hour,
		PresenceHeartbeat: time.Second, PresenceTTL: 3 * time.Second,
		HistoryQueryTimeout: 10 * time.Second, HistoryMaxQueries: 8, HistoryMaxBuckets: 1500, HistoryReplayWindow: 30 * 24 * time.Hour,
	}
	for _, f := range tweak {
		f(cfg)
	}
	producer, err := bus.NewProducer(brokers, true, logger)
	if err != nil {
		t.Fatal(err)
	}
	producer.OnError = func(topic string, _ error) { metrics.BusProduceErrors.WithLabelValues(topic).Inc() }
	gw := gateway.New(ctx, cfg, pool, producer, hist, logger)
	var wg sync.WaitGroup
	run := func(fn func(context.Context) error) {
		wg.Add(1)
		go func() { defer wg.Done(); _ = fn(ctx) }()
	}
	run(gw.Run)
	run((&outbox.Relay{Pool: pool, Producer: producer, Log: logger}).Run)
	if withIngest {
		// A stable group keeps committed offsets between runs; a clean shutdown
		// (waited for below) makes the member leave the group immediately.
		// Note: a brand-new group starts at offset 0 and under -race the first
		// multi-MB LZ4 fetch decompresses very slowly (instrumented byte loops),
		// which can exceed the test timeouts. Not an issue without -race.
		group := env("QUACK_IT_INGEST_GROUP", "quack-ingest-it")
		skipBacklog(t, group)
		run((&ingest.Ingester{Group: group, Store: hist, Brokers: brokers, Publisher: producer, Log: logger}).Run)
	}
	srv := httptest.NewUnstartedServer(httpserver.New(cfg, pool, hist, gw, logger))
	srv.Config.Protocols = new(http.Protocols) // like `quack serve`: HTTP/1.1 + h2c (gRPC)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			srv.CloseClientConnections()
			srv.Close()
			cancel()
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Errorf("%s: components did not stop within 15s", gatewayID)
			}
			producer.Close()
		})
	}
	t.Cleanup(stop)
	time.Sleep(1500 * time.Millisecond) // bus consumer reaches end offsets
	deadline := time.Now().Add(20 * time.Second)
	for !gw.Ready() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !gw.Ready() {
		t.Fatalf("%s: device registry not ready", gatewayID)
	}
	return &instance{URL: srv.URL, srv: srv, gw: gw, stop: stop}
}

// --- HTTP client ---

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar, Timeout: 10 * time.Second}}
}

type resp struct {
	Status int
	Body   []byte
	Header http.Header
}

func (r resp) JSON(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("decode %s: %v", r.Body, err)
	}
}

func (c *client) do(method, path string, body any, hdr ...string) resp {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	r, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = r.Body.Close() }()
	b, _ := io.ReadAll(r.Body)
	return resp{Status: r.StatusCode, Body: b, Header: r.Header}
}

func (c *client) must(status int, method, path string, body any, out any) resp {
	c.t.Helper()
	r := c.do(method, path, body)
	if r.Status != status {
		c.t.Fatalf("%s %s: status %d want %d: %s", method, path, r.Status, status, r.Body)
	}
	if out != nil {
		r.JSON(c.t, out)
	}
	return r
}

func (c *client) login(user, pass string) {
	c.t.Helper()
	c.must(200, "POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pass}, nil)
}

func (c *client) cookieHeader() string {
	req, _ := http.NewRequest("GET", c.base, nil)
	var parts []string
	for _, ck := range c.http.Jar.Cookies(req.URL) {
		parts = append(parts, ck.Name+"="+ck.Value)
	}
	return strings.Join(parts, "; ")
}

var svc = func() *service.Service { return service.New(pool) }

func mkUser(t *testing.T, name string, admin bool) {
	t.Helper()
	if _, err := svc().CreateUser(context.Background(), service.SystemActor, service.UserInput{Username: name, Password: name + "-password", IsActive: true, IsAdmin: admin}); err != nil {
		t.Fatal(err)
	}
}

// --- WebSocket helpers ---

type wsConn struct {
	t  *testing.T
	c  *websocket.Conn
	ch chan map[string]any
}

func dialWS(t *testing.T, url string, hdr http.Header) (*wsConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, err
	}
	w := &wsConn{t: t, c: c, ch: make(chan map[string]any, 1000)}
	go func() {
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				close(w.ch)
				return
			}
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			w.ch <- m
		}
	}()
	t.Cleanup(func() { _ = c.CloseNow() })
	return w, nil
}

func (w *wsConn) send(v any) {
	b, _ := json.Marshal(v)
	if err := w.c.Write(context.Background(), websocket.MessageText, b); err != nil {
		w.t.Fatal(err)
	}
}

// expect waits for a frame matching pred, skipping others.
func (w *wsConn) expect(desc string, pred func(map[string]any) bool) map[string]any {
	w.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-w.ch:
			if !ok {
				w.t.Fatalf("%s: socket closed", desc)
			}
			if pred(m) {
				return m
			}
		case <-deadline:
			w.t.Fatalf("timeout waiting for %s", desc)
		}
	}
}

type deviceKey struct {
	priv crypto.Signer
	pem  string
}

func newECKey(t *testing.T) deviceKey {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(k.Public())
	return deviceKey{priv: k, pem: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))}
}

func deviceHeader(t *testing.T, k deviceKey, deviceID string) http.Header {
	tok, err := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"id": deviceID, "exp": time.Now().Add(time.Hour).Unix()}).SignedString(k.priv)
	if err != nil {
		t.Fatal(err)
	}
	return http.Header{"Authorization": {"Bearer " + tok}}
}

func wsURL(base, path string) string { return "ws" + strings.TrimPrefix(base, "http") + path }

func newLogger() *slog.Logger {
	if os.Getenv("QUACK_IT_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// skipBacklog commits the group's offsets at the current end of the topic, so
// the test ingester only sees events produced by the test itself (the shared
// dev topic can hold a large backlog, which is slow to decompress under -race).
func skipBacklog(t *testing.T, group string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	adm := kadm.NewClient(cl)
	ends, err := adm.ListEndOffsets(ctx, bus.TopicName(events.TopicElementEvents))
	if err != nil {
		t.Fatal(err)
	}
	offs := kadm.Offsets{}
	ends.Each(func(o kadm.ListedOffset) { offs.AddOffset(o.Topic, o.Partition, o.Offset, -1) })
	if err := adm.CommitAllOffsets(ctx, group, offs); err != nil {
		t.Fatalf("commit end offsets for %s: %v", group, err)
	}
}
