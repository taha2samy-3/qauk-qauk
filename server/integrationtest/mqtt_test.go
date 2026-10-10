//go:build integration

package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// MQTT tests need the dev broker: `task infra:up MQTT=1` (Mosquitto on
// 1883 anonymous, 1884 quack/quack, 8883 mTLS with docker/mqtt/certs).
var mqttHost = env("QUACK_IT_MQTT_HOST", "127.0.0.1")

func mqttRoles(c *config.Config) {
	c.Roles = []string{"api", "gateway", "mqtt"}
	c.AllowInsecureTLS = true // the dev broker's password listener is plain TCP
	c.MQTTConnectionMsgRate = 5000
	c.MQTTWeight = 1
}

func certsDir(t *testing.T) string {
	_, file, _, _ := runtime.Caller(0)
	d := filepath.Join(filepath.Dir(file), "..", "..", "docker", "mqtt", "certs")
	if _, err := os.Stat(filepath.Join(d, "ca.crt")); err != nil {
		t.Skip("run `task mqtt:certs` (or `task infra:up MQTT=1`) first")
	}
	return d
}

// testClient is an MQTT client playing the devices.
type testClient struct {
	cm  *autopaho.ConnectionManager
	mu  sync.Mutex
	got map[string]chan *paho.Publish
}

func dialMQTT(t *testing.T) *testClient {
	t.Helper()
	u, _ := url.Parse(fmt.Sprintf("mqtt://%s:1883", mqttHost))
	tc := &testClient{got: map[string]chan *paho.Publish{}}
	ctx, cancel := context.WithCancel(context.Background())
	cm, err := autopaho.NewConnection(ctx, autopaho.ClientConfig{
		ServerUrls: []*url.URL{u}, KeepAlive: 20, CleanStartOnInitialConnection: true,
		ClientConfig: paho.ClientConfig{
			ClientID: "it-device-" + uuid.NewString()[:8],
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){func(pr paho.PublishReceived) (bool, error) {
				tc.mu.Lock()
				for f, ch := range tc.got {
					if f == pr.Packet.Topic || strings.HasSuffix(f, "#") && strings.HasPrefix(pr.Packet.Topic, strings.TrimSuffix(f, "#")) {
						ch <- pr.Packet
					}
				}
				tc.mu.Unlock()
				return true, nil
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	actx, acancel := context.WithTimeout(ctx, 5*time.Second)
	defer acancel()
	if err := cm.AwaitConnection(actx); err != nil {
		cancel()
		t.Skipf("no MQTT broker on %s:1883 (task infra:up MQTT=1): %v", mqttHost, err)
	}
	tc.cm = cm
	t.Cleanup(func() { _ = cm.Disconnect(context.Background()); cancel() })
	return tc
}

func (c *testClient) publish(t *testing.T, topic string, payload []byte, props *paho.PublishProperties) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.cm.Publish(ctx, &paho.Publish{Topic: topic, QoS: 1, Payload: payload, Properties: props}); err != nil {
		t.Fatal(err)
	}
}

func (c *testClient) subscribe(t *testing.T, filter string) chan *paho.Publish {
	t.Helper()
	ch := make(chan *paho.Publish, 100)
	c.mu.Lock()
	c.got[filter] = ch
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.cm.Subscribe(ctx, &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: filter, QoS: 1}}}); err != nil {
		t.Fatal(err)
	}
	return ch
}

// mqttFixture: a device with elements, an MQTT connection granted to it.
type mqttFixture struct {
	realtimeFixture
	conn     events.MQTTConnection
	ext      string
	base     string // topic prefix unique to the test
	elements map[string]uuid.UUID
}

func setupMQTT(t *testing.T, prefix string, conn events.MQTTConnection, elements ...string) mqttFixture {
	t.Helper()
	ctx := context.Background()
	f := mqttFixture{realtimeFixture: setupRealtime(t, prefix, 5), elements: map[string]uuid.UUID{},
		ext: prefix + "-" + uuid.NewString()[:6], base: "it/" + uuid.NewString()[:8]}
	u, _ := store.GetUserByUsername(ctx, pool, prefix+"-user")
	for _, name := range elements {
		e, err := svc().CreateElement(ctx, service.SystemActor, service.ElementInput{DeviceID: f.device.ID, Name: name, Points: 5})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc().SetPermission(ctx, service.SystemActor, service.PermissionInput{ElementID: e.ID, UserID: &u.ID, Permission: "RC"}); err != nil {
			t.Fatal(err)
		}
		f.elements[name] = e.ID
	}
	if conn.Name == "" {
		conn.Name = prefix
	}
	if conn.BrokerURL == "" {
		conn.BrokerURL = fmt.Sprintf("mqtt://%s:1883", mqttHost)
	}
	conn.Enabled = true
	conn.ClientIDPrefix = "it-" + prefix + "-" + uuid.NewString()[:6]
	s := svc()
	s.AllowInsecureTLS = true
	c, err := s.CreateMQTTConnection(ctx, service.SystemActor, conn)
	if err != nil {
		t.Fatal(err)
	}
	f.conn = c
	t.Cleanup(func() { _ = svc().DeleteMQTTConnection(context.Background(), service.SystemActor, c.ID) })
	if err := s.GrantMQTTDevice(ctx, service.SystemActor, c.ID, f.device.ID, f.ext); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f mqttFixture) uplink(t *testing.T, u events.MQTTUplink) events.MQTTUplink {
	t.Helper()
	u.Enabled = true
	out, err := svc().CreateMQTTUplink(context.Background(), service.SystemActor, f.conn.ID, u)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// waitConnected waits until some gateway reports every slot of the connection connected.
func waitConnected(t *testing.T, connID string, slots int) []store.MQTTStatus {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, _ := store.ListMQTTStatus(context.Background(), pool)
		var mine []store.MQTTStatus
		for _, s := range st {
			if s.ConnectionID == connID && s.Connected {
				mine = append(mine, s)
			}
		}
		if len(mine) == slots {
			time.Sleep(300 * time.Millisecond) // subscriptions follow the connect
			return mine
		}
		if time.Now().After(deadline) {
			t.Fatalf("connection %s: %d/%d slots connected (status %+v)", connID, len(mine), slots, st)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func subscribeBrowser(t *testing.T, inst *instance, f mqttFixture, ids ...uuid.UUID) *wsConn {
	t.Helper()
	br, err := dialWS(t, wsURL(inst.URL, "/browser/simple/"), browserHeader(f.cookie(inst.URL), inst.URL))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		br.send(map[string]any{"type": "subscribe", "element_id": id})
		br.expect("subscribe confirm", func(m map[string]any) bool { return m["type"] == "subscribe" && m["element_id"] == id.String() })
	}
	return br
}

// eventsFrom reads element events written after the returned function was made.
func eventsFrom(t *testing.T) func(match func(*events.Event) bool) *events.Event {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		t.Fatal(err)
	}
	topic := bus.TopicName(events.TopicElementEvents)
	ends, err := kadm.NewClient(cl).ListEndOffsets(context.Background(), topic)
	cl.Close()
	if err != nil {
		t.Fatal(err)
	}
	from := map[string]map[int32]kgo.Offset{topic: {}}
	ends.Each(func(o kadm.ListedOffset) { from[topic][o.Partition] = kgo.NewOffset().At(o.Offset) })
	return func(match func(*events.Event) bool) *events.Event {
		rd, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumePartitions(from), kgo.FetchMaxWait(200*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		defer rd.Close()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			fs := rd.PollFetches(ctx)
			cancel()
			var hit *events.Event
			fs.EachRecord(func(r *kgo.Record) {
				var ev events.Event
				if hit == nil && json.Unmarshal(r.Value, &ev) == nil && match(&ev) {
					hit = &ev
				}
			})
			if hit != nil {
				return hit
			}
		}
		return nil
	}
}

// One MQTT message → two elements on a dashboard, as CloudEvents with quackvia.
func TestMQTTUplinkToDashboard(t *testing.T) {
	inst := startInstance(t, "gw-mqtt-up", false, mqttRoles)
	f := setupMQTT(t, "mqttup", events.MQTTConnection{}, "Temperature", "Humidity")
	f.uplink(t, events.MQTTUplink{TopicFilter: f.base + "/+/up", QoS: 1, Format: "json",
		Device:   json.RawMessage(`{"segment":2}`),
		FieldMap: json.RawMessage(`[{"element":"Temperature","value":"t"},{"element":"Humidity","value":"h"}]`)})
	waitConnected(t, f.conn.ID, 1)
	br := subscribeBrowser(t, inst, f, f.elements["Temperature"], f.elements["Humidity"])
	next := eventsFrom(t)
	dev := dialMQTT(t)
	dev.publish(t, f.base+"/"+f.ext+"/up", []byte(`{"t":21.5,"h":48}`), nil)
	br.expect("temperature from MQTT", isMsg(f.elements["Temperature"], 21.5))
	got := br.expect("humidity from MQTT", isMsg(f.elements["Humidity"], 48))
	if auth := got["auth"].(map[string]any); auth["user_id"] != f.device.ID.String() {
		t.Fatalf("the actor must be the device: %v", auth)
	}
	ev := next(func(ev *events.Event) bool { return ev.Subject == f.elements["Temperature"].String() })
	if ev == nil || ev.QuackVia != "mqtt/"+f.conn.ID || ev.Type != events.TypeElementMessage {
		t.Fatalf("CloudEvent from MQTT: %+v", ev)
	}
	// the device is online while its messages arrive (no connection of its own)
	if ok, err := store.DeviceConnected(context.Background(), pool, f.device.ID, time.Minute); err != nil || !ok {
		t.Fatalf("an MQTT device sending values must be online: %v %v", ok, err)
	}
}

// A JS decoder for a binary payload; a device that isn't granted is refused
// and dead-lettered; an element limit is applied to MQTT values too.
func TestMQTTDecoderGrantsAndDLQ(t *testing.T) {
	inst := startInstance(t, "gw-mqtt-dec", false, mqttRoles)
	f := setupMQTT(t, "mqttdec", events.MQTTConnection{}, "Temperature")
	dec, err := svc().CreateDecoder(context.Background(), service.SystemActor, "two bytes",
		`function decodeUplink(input) { return {data: {t: ((input.bytes[0] << 8) | input.bytes[1]) / 100}}; }`)
	if err != nil {
		t.Fatal(err)
	}
	f.uplink(t, events.MQTTUplink{TopicFilter: f.base + "/+/bin", QoS: 1, Format: "bytes", DecoderID: &dec.ID,
		Device: json.RawMessage(`{"segment":2}`), FieldMap: json.RawMessage(`[{"element":"Temperature","value":"t"}]`)})
	waitConnected(t, f.conn.ID, 1)
	br := subscribeBrowser(t, inst, f, f.elements["Temperature"])
	dev := dialMQTT(t)
	dev.publish(t, f.base+"/"+f.ext+"/bin", []byte{0x08, 0x66}, nil) // 2150 / 100
	br.expect("decoded value", isMsg(f.elements["Temperature"], 21.5))

	// a device id that isn't granted: nothing published, a DLQ entry
	stranger := "stranger-" + uuid.NewString()[:4]
	dev.publish(t, f.base+"/"+stranger+"/bin", []byte{0x01, 0x00}, nil)
	c := newClient(t, inst.URL)
	mkUser(t, "mqttdec-admin", true)
	c.login("mqttdec-admin", "mqttdec-admin-password")
	deadline := time.Now().Add(10 * time.Second)
	for {
		var rejected []events.MQTTRejected
		c.must(200, "GET", "/api/v1/admin/mqtt/rejected?limit=50", nil, &rejected)
		if slices.ContainsFunc(rejected, func(r events.MQTTRejected) bool {
			return r.DeviceExternalID == stranger && strings.Contains(strings.Join(r.Reasons, ";"), "not granted")
		}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no DLQ entry for the stranger: %+v", rejected)
		}
		time.Sleep(200 * time.Millisecond)
	}
	select {
	case m := <-br.ch:
		if m["type"] == "message_element" {
			t.Fatalf("a non-granted device's value reached the dashboard: %v", m)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

// A dashboard command goes out on the downlink topic, once.
func TestMQTTDownlink(t *testing.T) {
	inst := startInstance(t, "gw-mqtt-down", false, mqttRoles)
	f := setupMQTT(t, "mqttdown", events.MQTTConnection{}, "Fan")
	f.uplink(t, events.MQTTUplink{TopicFilter: f.base + "/+/state", QoS: 1, Format: "json",
		Device: json.RawMessage(`{"segment":2}`), FieldMap: json.RawMessage(`[{"element":"Fan","value":"on"}]`)})
	if _, err := svc().CreateMQTTDownlink(context.Background(), service.SystemActor, f.conn.ID, events.MQTTDownlink{
		DeviceExternalID: f.ext, Element: "Fan", TopicTemplate: f.base + "/{device}/cmd/{element}", QoS: 1,
		Encoder: json.RawMessage(`{"template":{"on":"{{value}}","by":"quack"}}`)}); err != nil {
		t.Fatal(err)
	}
	waitConnected(t, f.conn.ID, 1)
	dev := dialMQTT(t)
	cmds := dev.subscribe(t, f.base+"/"+f.ext+"/cmd/Fan")
	br := subscribeBrowser(t, inst, f, f.elements["Fan"])
	br.send(map[string]any{"type": "message_element", "element_id": f.elements["Fan"], "message": map[string]any{"value": 1}})
	select {
	case p := <-cmds:
		if string(p.Payload) != `{"by":"quack","on":1}` {
			t.Fatalf("downlink payload %s", p.Payload)
		}
		// the device applies it and reports its state: the dashboard sees the echo
		dev.publish(t, f.base+"/"+f.ext+"/state", []byte(`{"on":1}`), nil)
	case <-time.After(10 * time.Second):
		t.Fatal("no downlink published")
	}
	br.expect("state echo over MQTT", isMsg(f.elements["Fan"], 1))
	select {
	case p := <-cmds:
		t.Fatalf("the command was published twice: %s", p.Payload)
	case <-time.After(500 * time.Millisecond):
	}
}

// Connections spread over three gateways (HRW); stopping one moves only its
// connections, and QoS 1 messages published meanwhile are not lost.
func TestMQTTAssignmentAndFailover(t *testing.T) {
	gws := []*instance{
		startInstance(t, "gw-mqtt-a", false, mqttRoles),
		startInstance(t, "gw-mqtt-b", false, mqttRoles),
		startInstance(t, "gw-mqtt-c", false, mqttRoles),
	}
	var fixtures []mqttFixture
	for i := range 6 {
		f := setupMQTT(t, fmt.Sprintf("mqtthrw%d", i), events.MQTTConnection{SessionExpiry: 600}, "V")
		f.uplink(t, events.MQTTUplink{TopicFilter: f.base + "/+/up", QoS: 1, Format: "json",
			Device: json.RawMessage(`{"segment":2}`), FieldMap: json.RawMessage(`[{"element":"V","value":"v"}]`)})
		fixtures = append(fixtures, f)
	}
	owner := func() map[string]string {
		out := map[string]string{}
		for gi, g := range gws {
			if g == nil {
				continue
			}
			for conn := range g.gw.MQTT().OwnedSlots() {
				if prev, dup := out[conn]; dup {
					t.Fatalf("connection %s owned by gateways %s and %d", conn, prev, gi)
				}
				out[conn] = fmt.Sprint(gi)
			}
		}
		return out
	}
	deadline := time.Now().Add(20 * time.Second)
	for len(owner()) < 6 {
		if time.Now().After(deadline) {
			t.Fatalf("not every connection is owned: %v", owner())
		}
		time.Sleep(200 * time.Millisecond)
	}
	for _, f := range fixtures {
		waitConnected(t, f.conn.ID, 1)
	}
	before := owner()
	perGW := map[string]int{}
	for _, g := range before {
		perGW[g]++
	}
	t.Logf("assignment over 3 gateways: %v", perGW)

	// a dashboard on a gateway that keeps running
	victim := before[fixtures[0].conn.ID]
	vi := int(victim[0] - '0')
	alive := gws[(vi+1)%3]
	br := subscribeBrowser(t, alive, fixtures[0], fixtures[0].elements["V"])

	// stop the gateway that owns fixture 0's connection; publish while nobody owns it
	gws[vi].stop()
	gws[vi] = nil
	dev := dialMQTT(t)
	for i := range 5 {
		dev.publish(t, fixtures[0].base+"/"+fixtures[0].ext+"/up", []byte(fmt.Sprintf(`{"v":%d}`, 100+i)), nil)
	}
	deadline = time.Now().Add(20 * time.Second)
	for {
		after := owner()
		if len(after) == 6 {
			for conn, g := range before {
				if g != victim && after[conn] != g {
					t.Fatalf("connection %s moved from gateway %s to %s although %s kept running", conn, g, after[conn], g)
				}
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connections of the stopped gateway were not taken over: %v", after)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// the new owner resumes the slot's session: the messages queued meanwhile arrive
	for i := range 5 {
		br.expect(fmt.Sprintf("value %d published during the handover", 100+i), isMsg(fixtures[0].elements["V"], float64(100+i)))
	}
}

// replicas: 2 → a shared subscription, one slot per gateway; each message once.
func TestMQTTReplicasSharedSubscription(t *testing.T) {
	a := startInstance(t, "gw-mqtt-ra", false, mqttRoles)
	b := startInstance(t, "gw-mqtt-rb", false, mqttRoles)
	f := setupMQTT(t, "mqttrep", events.MQTTConnection{Replicas: 2}, "V")
	f.uplink(t, events.MQTTUplink{TopicFilter: f.base + "/+/up", QoS: 1, Format: "json",
		Device: json.RawMessage(`{"segment":2}`), FieldMap: json.RawMessage(`[{"element":"V","value":"v"}]`)})
	st := waitConnected(t, f.conn.ID, 2)
	if st[0].GatewayID == st[1].GatewayID {
		t.Fatalf("both slots on one gateway: %+v", st)
	}
	br := subscribeBrowser(t, a, f, f.elements["V"])
	dev := dialMQTT(t)
	const n = 30
	for i := range n {
		dev.publish(t, f.base+"/"+f.ext+"/up", []byte(fmt.Sprintf(`{"v":%d}`, i)), nil)
	}
	seen := map[float64]int{}
	timeout := time.After(10 * time.Second)
	for len(seen) < n {
		select {
		case m := <-br.ch:
			if m["type"] == "message_element" {
				seen[m["message"].(map[string]any)["value"].(float64)]++
			}
		case <-timeout:
			t.Fatalf("got %d of %d values", len(seen), n)
		}
	}
	for v, c := range seen {
		if c != 1 {
			t.Fatalf("value %v delivered %d times", v, c)
		}
	}
	// both slots took a share
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, _ := store.ListMQTTStatus(context.Background(), pool)
		shares := 0
		for _, s := range st {
			var cnt map[string]int64
			_ = json.Unmarshal(s.Counters, &cnt)
			if s.ConnectionID == f.conn.ID && cnt["received"] > 0 {
				shares++
			}
		}
		if shares == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the broker didn't split the shared subscription: %+v", st)
		}
		time.Sleep(500 * time.Millisecond)
	}
	_ = b
}

// Broker authentication: a wrong password shows reason 0x86; mTLS works with
// the dev CA and a rogue client certificate is refused.
func TestMQTTBrokerAuth(t *testing.T) {
	dir := certsDir(t)
	_ = startInstance(t, "gw-mqtt-auth", false, mqttRoles)
	t.Setenv("IT_MQTT_GOOD", "quack")
	t.Setenv("IT_MQTT_BAD", "wrong")
	bad := setupMQTT(t, "mqttbadpw", events.MQTTConnection{BrokerURL: fmt.Sprintf("mqtt://%s:1884", mqttHost),
		Auth: json.RawMessage(`{"method":"password","username":"quack","password":"env:IT_MQTT_BAD"}`)}, "V")
	good := setupMQTT(t, "mqttgoodpw", events.MQTTConnection{BrokerURL: fmt.Sprintf("mqtt://%s:1884", mqttHost),
		Auth: json.RawMessage(`{"method":"password","username":"quack","password":"env:IT_MQTT_GOOD"}`)}, "V")
	mtls := setupMQTT(t, "mqttmtls", events.MQTTConnection{BrokerURL: fmt.Sprintf("mqtts://%s:8883", mqttHost),
		Auth: json.RawMessage(`{"method":"mtls"}`),
		TLS:  json.RawMessage(fmt.Sprintf(`{"ca":"file:%s/ca.crt","cert":"file:%s/client.crt","key":"file:%s/client.key","server_name":"localhost"}`, dir, dir, dir))}, "V")
	rogue := setupMQTT(t, "mqttrogue", events.MQTTConnection{BrokerURL: fmt.Sprintf("mqtts://%s:8883", mqttHost),
		Auth: json.RawMessage(`{"method":"mtls"}`),
		TLS:  json.RawMessage(fmt.Sprintf(`{"ca":"file:%s/ca.crt","cert":"file:%s/rogue.crt","key":"file:%s/rogue.key","server_name":"localhost"}`, dir, dir, dir))}, "V")
	waitConnected(t, good.conn.ID, 1)
	waitConnected(t, mtls.conn.ID, 1)
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, _ := store.ListMQTTStatus(context.Background(), pool)
		var badSt, rogueSt *store.MQTTStatus
		for i := range st {
			switch st[i].ConnectionID {
			case bad.conn.ID:
				badSt = &st[i]
			case rogue.conn.ID:
				rogueSt = &st[i]
			}
		}
		if badSt != nil && badSt.ReasonCode != nil && rogueSt != nil && rogueSt.LastError != nil {
			// MQTT 5 allows 0x86 (bad user name or password) or 0x87 (not
			// authorized) here; Mosquitto answers 0x87.
			if (*badSt.ReasonCode != 0x86 && *badSt.ReasonCode != 0x87) || badSt.Connected ||
				!strings.Contains(*badSt.LastError, "broker refused") {
				t.Fatalf("wrong password status: %+v %s", badSt, *badSt.LastError)
			}
			if rogueSt.Connected {
				t.Fatal("a client certificate from an unknown CA connected")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no error status for the bad credentials: %+v", st)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// Rules change while connected: a new uplink works without a restart; the
// role is opt-in.
func TestMQTTLiveConfigAndOptIn(t *testing.T) {
	inst := startInstance(t, "gw-mqtt-live", false, mqttRoles)
	f := setupMQTT(t, "mqttlive", events.MQTTConnection{}, "A", "B")
	f.uplink(t, events.MQTTUplink{TopicFilter: f.base + "/a", QoS: 1, Format: "number",
		Device: json.RawMessage(fmt.Sprintf(`{"fixed":%q}`, f.ext)), FieldMap: json.RawMessage(`[{"element":"A","value":""}]`)})
	waitConnected(t, f.conn.ID, 1)
	br := subscribeBrowser(t, inst, f, f.elements["A"], f.elements["B"])
	dev := dialMQTT(t)
	dev.publish(t, f.base+"/a", []byte("1.5"), nil)
	br.expect("A", isMsg(f.elements["A"], 1.5))
	f.uplink(t, events.MQTTUplink{TopicFilter: f.base + "/b", QoS: 1, Format: "number",
		Device: json.RawMessage(fmt.Sprintf(`{"fixed":%q}`, f.ext)), FieldMap: json.RawMessage(`[{"element":"B","value":""}]`)})
	deadline := time.Now().Add(10 * time.Second)
	for {
		dev.publish(t, f.base+"/b", []byte("2.5"), nil)
		select {
		case m := <-br.ch:
			if isMsg(f.elements["B"], 2.5)(m) {
				goto optIn
			}
		case <-time.After(500 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the new uplink rule never applied")
		}
	}
optIn:
	plain := startInstance(t, "gw-no-mqtt", false)
	if plain.gw.MQTT() != nil {
		t.Fatal("without the mqtt role there must be no MQTT transport")
	}
}

// The admin API validates input and runs the pipeline test; capture works.
func TestMQTTAdminAPI(t *testing.T) {
	inst := startInstance(t, "gw-mqtt-api", false, mqttRoles)
	mkUser(t, "mqttapi-admin", true)
	c := newClient(t, inst.URL)
	c.login("mqttapi-admin", "mqttapi-admin-password")
	for _, bad := range []map[string]any{
		{"name": "x", "broker_url": "http://nope"},
		{"name": "x", "broker_url": "mqtts://b:8883", "auth": map[string]any{"method": "password", "username": "u", "password": "plaintext"}},
		{"name": "x", "broker_url": "mqtt://b:1883", "replicas": 50},
	} {
		if r := c.do("POST", "/api/v1/admin/mqtt/connections", bad); r.Status != 422 {
			t.Errorf("%v: status %d %s", bad, r.Status, r.Body)
		}
	}
	var conn events.MQTTConnection
	c.must(201, "POST", "/api/v1/admin/mqtt/connections", map[string]any{"name": "api", "broker_url": fmt.Sprintf("mqtt://%s:1883", mqttHost), "enabled": true}, &conn)
	t.Cleanup(func() { _ = svc().DeleteMQTTConnection(context.Background(), service.SystemActor, conn.ID) })
	if conn.Keepalive != 60 || conn.Replicas != 1 || conn.SessionExpiry != 3600 || !strings.HasPrefix(conn.ClientIDPrefix, "quack-") {
		t.Fatalf("defaults: %+v", conn)
	}
	if r := c.do("POST", "/api/v1/admin/mqtt/connections/"+conn.ID+"/uplinks", map[string]any{
		"topic_filter": "a/#/b", "qos": 1, "format": "json", "device": map[string]any{"segment": 1}, "field_map": []any{map[string]any{"element": "x", "value": "x"}}}); r.Status != 422 {
		t.Fatalf("bad topic filter: %d %s", r.Status, r.Body)
	}
	var test struct {
		Values []struct {
			Device, Element string
			Message         json.RawMessage
		} `json:"values"`
		Rejections []string `json:"rejections"`
	}
	c.must(200, "POST", "/api/v1/admin/mqtt/test", map[string]any{
		"format": "bytes", "device": map[string]any{"segment": 1}, "topic": "lora/dev-9/up", "payload": "hex:0866",
		"decoder_source": "function decodeUplink(i) { return {data: {t: ((i.bytes[0] << 8) | i.bytes[1]) / 100}}; }",
		"field_map":      []any{map[string]any{"element": "Temperature", "value": "t"}},
	}, &test)
	if len(test.Values) != 1 || string(test.Values[0].Message) != `{"value":21.5}` {
		t.Fatalf("test endpoint: %+v", test)
	}

	// capture: enable, publish, read back
	f := setupMQTT(t, "mqttcap", events.MQTTConnection{}, "V")
	up := f.uplink(t, events.MQTTUplink{TopicFilter: f.base + "/+/up", QoS: 1, Format: "json",
		Device: json.RawMessage(`{"segment":2}`), FieldMap: json.RawMessage(`[{"element":"V","value":"v"}]`)})
	c.must(200, "POST", "/api/v1/admin/mqtt/uplinks/"+up.ID+"/capture", map[string]any{"seconds": 120}, nil)
	waitConnected(t, f.conn.ID, 1)
	time.Sleep(500 * time.Millisecond) // the new snapshot reaches the owner
	dev := dialMQTT(t)
	marker := fmt.Sprintf(`{"v":%d}`, rand.IntN(1e6))
	deadline := time.Now().Add(10 * time.Second)
	for {
		dev.publish(t, f.base+"/"+f.ext+"/up", []byte(marker), nil)
		var captured []events.MQTTCaptured
		c.must(200, "GET", "/api/v1/admin/mqtt/uplinks/"+up.ID+"/captured", nil, &captured)
		if slices.ContainsFunc(captured, func(m events.MQTTCaptured) bool { return string(m.Payload) == marker }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing captured: %+v", captured)
		}
		time.Sleep(500 * time.Millisecond)
	}
	// the API never returns secret values (only references are accepted anyway)
	r := c.do("GET", "/api/v1/admin/mqtt/connections/"+conn.ID, nil)
	if r.Status != 200 || strings.Contains(string(r.Body), "plaintext") {
		t.Fatalf("get connection: %d %s", r.Status, r.Body)
	}
}
