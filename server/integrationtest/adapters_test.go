//go:build integration

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/events"
	devicev1 "github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1"
	"github.com/taha2samy/quackquack/server/internal/gen/quack/device/v1/devicev1connect"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// Element rate limits through the admin API: stored, validated, resettable.
func TestElementLimitsAPI(t *testing.T) {
	in := startInstance(t, "gw-limits-api", false)
	mkUser(t, "limits-admin", true)
	c := newClient(t, in.URL)
	c.login("limits-admin", "limits-admin-password")
	var dev store.Device
	c.must(201, "POST", "/api/v1/admin/devices", map[string]any{"name": "limits-dev"}, &dev)

	var el store.Element
	c.must(201, "POST", "/api/v1/admin/elements", map[string]any{"device_id": dev.ID, "name": "e", "points": 1,
		"msg_rate": 2.5, "msg_burst": 5, "over_limit": "latest"}, &el)
	if el.MsgRate == nil || *el.MsgRate != 2.5 || el.MsgBurst == nil || *el.MsgBurst != 5 || el.OverLimit != "latest" {
		t.Fatalf("created %+v", el)
	}
	for _, bad := range []map[string]any{{"msg_rate": 5000}, {"msg_rate": -1}, {"over_limit": "queue"}, {"msg_burst": 100000}} {
		if r := c.do("PATCH", "/api/v1/admin/elements/"+el.ID.String(), bad); r.Status != 422 {
			t.Errorf("PATCH %v: status %d %s", bad, r.Status, r.Body)
		}
	}
	c.must(200, "PATCH", "/api/v1/admin/elements/"+el.ID.String(), map[string]any{"msg_rate": 0, "over_limit": "drop"}, &el)
	if el.MsgRate != nil || el.MsgBurst == nil || el.OverLimit != "drop" {
		t.Fatalf("rate 0 must reset to the default, burst unchanged: %+v", el)
	}
	c.must(200, "PATCH", "/api/v1/admin/elements/"+el.ID.String(), map[string]any{"name": "renamed"}, &el)
	if el.OverLimit != "drop" || el.MsgBurst == nil {
		t.Fatalf("an unrelated patch changed the limits: %+v", el)
	}
}

// configOnTopic returns the newest record for a device on device-config.v1
// (nil value = tombstone), waiting for the outbox relay to publish it.
func configOnTopic(t *testing.T, deviceID uuid.UUID, ready func(*events.DeviceConfig, bool) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var last *kgo.Record
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := bus.ReadAll(ctx, brokers, events.TopicDeviceConfig, func(r *kgo.Record) {
			if string(r.Key) == deviceID.String() {
				last = r
			}
		})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if last != nil {
			if last.Value == nil {
				if ready(nil, true) {
					return
				}
			} else {
				var ev events.Event
				var cfg events.DeviceConfig
				if json.Unmarshal(last.Value, &ev) == nil && ev.DecodeData(&cfg) == nil && ready(&cfg, false) {
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("device %s: expected state never reached device-config.v1", deviceID)
}

// Every device change lands on device-config.v1 as a full snapshot, and a
// deleted device as a tombstone.
func TestDeviceConfigTopic(t *testing.T) {
	_ = startInstance(t, "gw-devcfg", false) // runs the outbox relay
	ctx := context.Background()
	f := setupRealtime(t, "devcfg", 3)
	configOnTopic(t, f.device.ID, func(c *events.DeviceConfig, tomb bool) bool {
		return !tomb && len(c.Elements) == 1 && c.Key != nil && c.Key.Active && c.Elements[0].Points == 3
	})
	r := 7.0
	if _, err := svc().UpdateElement(ctx, service.SystemActor, f.elem.ID, service.ElementPatch{Limits: service.ElementLimits{MsgRate: &r}}); err != nil {
		t.Fatal(err)
	}
	configOnTopic(t, f.device.ID, func(c *events.DeviceConfig, tomb bool) bool {
		return !tomb && c.Elements[0].Rate != nil && *c.Elements[0].Rate == 7
	})
	off := false
	if _, err := svc().UpdateKey(ctx, service.SystemActor, *f.device.PublicKeyID, service.KeyPatch{IsActive: &off}); err != nil {
		t.Fatal(err)
	}
	configOnTopic(t, f.device.ID, func(c *events.DeviceConfig, tomb bool) bool { return !tomb && !c.Key.Active })
	if err := svc().DeleteDevice(ctx, service.SystemActor, f.device.ID); err != nil {
		t.Fatal(err)
	}
	configOnTopic(t, f.device.ID, func(_ *events.DeviceConfig, tomb bool) bool { return tomb })
}

func authHeader(t *testing.T, f realtimeFixture) string {
	return deviceHeader(t, f.key, f.device.ID.String()).Get("Authorization")
}

// REST device adapter end to end: publish by element name reaches a
// dashboard; a dashboard command reaches the device's long-poll; a revoked
// key stops working without a database read.
func TestRESTAdapterEndToEnd(t *testing.T) {
	inst := startInstance(t, "gw-rest", false)
	f := setupRealtime(t, "rest", 5)
	br, err := dialWS(t, wsURL(inst.URL, "/browser/simple/"), browserHeader(f.cookie(inst.URL), inst.URL))
	if err != nil {
		t.Fatal(err)
	}
	br.send(map[string]any{"type": "subscribe", "element_id": f.elem.ID})
	br.expect("subscribe confirm", func(m map[string]any) bool { return m["type"] == "subscribe" })

	c := newClient(t, inst.URL)
	auth := authHeader(t, f)
	r := c.do("POST", "/device/v1/messages", []map[string]any{{"element": "rest-el", "message": map[string]any{"value": 12.5}, "id": "r-1"}},
		"Authorization", auth, "X-Quack-Device", f.device.ID.String())
	if r.Status != 200 || !strings.Contains(string(r.Body), `"accepted"`) {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	br.expect("REST device shows as connected", func(m map[string]any) bool {
		return m["type"] == "element_connection_status" && m["status"] == "connected"
	})
	got := br.expect("REST value on the dashboard", isMsg(f.elem.ID, 12.5))
	if got["auth"].(map[string]any)["username"] != "rest-dev" {
		t.Fatalf("actor %v", got["auth"])
	}
	// a retry with the same id is acknowledged, not delivered again
	r = c.do("POST", "/device/v1/messages", []map[string]any{{"element": "rest-el", "message": map[string]any{"value": 12.5}, "id": "r-1"}}, "Authorization", auth)
	if !strings.Contains(string(r.Body), `"duplicate"`) {
		t.Fatalf("retry: %s", r.Body)
	}

	// dashboard command -> device long-poll
	done := make(chan resp, 1)
	go func() { done <- c.do("GET", "/device/v1/sync?wait=8s", nil, "Authorization", auth) }()
	time.Sleep(300 * time.Millisecond)
	br.send(map[string]any{"type": "message_element", "element_id": f.elem.ID, "message": map[string]any{"value": 1.0}})
	select {
	case r := <-done:
		var out struct {
			Cursor   string
			Messages []map[string]any
		}
		r.JSON(t, &out)
		if len(out.Messages) != 1 || out.Messages[0]["element"] != "rest-el" || out.Cursor == "" {
			t.Fatalf("sync: %s", r.Body)
		}
	case <-time.After(9 * time.Second):
		t.Fatal("long-poll did not return the command")
	}

	// revoke the key: REST stops authenticating once the snapshot arrives
	off := false
	if _, err := svc().UpdateKey(context.Background(), service.SystemActor, *f.device.PublicKeyID, service.KeyPatch{IsActive: &off}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := c.do("POST", "/device/v1/messages", map[string]any{"element": "rest-el", "message": 1}, "Authorization", auth)
		if r.Status == http.StatusUnauthorized {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("revoked key still accepted: %d", r.Status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A brand-new device works at once, even before its snapshot is on the
// topic (registry falls back to the database).
func TestNewDeviceWorksImmediately(t *testing.T) {
	inst := startInstance(t, "gw-newdev", false)
	f := setupRealtime(t, "newdev", 1)
	c := newClient(t, inst.URL)
	r := c.do("POST", "/device/v1/messages", map[string]any{"element": "newdev-el", "message": 1}, "Authorization", authHeader(t, f))
	if r.Status != 200 {
		t.Fatalf("status %d %s", r.Status, r.Body)
	}
}

func h2cClient() *http.Client {
	tr := &http.Transport{Protocols: new(http.Protocols)}
	tr.Protocols.SetUnencryptedHTTP2(true)
	return &http.Client{Transport: tr}
}

// gRPC device adapter end to end over h2c on the main HTTP server.
func TestGRPCAdapterEndToEnd(t *testing.T) {
	inst := startInstance(t, "gw-grpc", false)
	f := setupRealtime(t, "grpc", 5)
	br, err := dialWS(t, wsURL(inst.URL, "/browser/simple/"), browserHeader(f.cookie(inst.URL), inst.URL))
	if err != nil {
		t.Fatal(err)
	}
	br.send(map[string]any{"type": "subscribe", "element_id": f.elem.ID})
	br.expect("subscribe confirm", func(m map[string]any) bool { return m["type"] == "subscribe" })

	cl := devicev1connect.NewDeviceServiceClient(h2cClient(), inst.URL, connect.WithGRPC())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	watchReq := connect.NewRequest(&devicev1.WatchRequest{})
	watchReq.Header().Set("Authorization", authHeader(t, f))
	watch, err := cl.Watch(ctx, watchReq)
	if err != nil {
		t.Fatal(err)
	}
	br.expect("stream counts as connected", func(m map[string]any) bool {
		return m["type"] == "element_connection_status" && m["status"] == "connected"
	})

	v, _ := structpb.NewValue(map[string]any{"value": 33.0})
	pubReq := connect.NewRequest(&devicev1.PublishRequest{Messages: []*devicev1.DeviceMessage{{Element: "grpc-el", Message: v}}})
	pubReq.Header().Set("Authorization", authHeader(t, f))
	res, err := cl.Publish(ctx, pubReq)
	if err != nil || res.Msg.GetResults()[0].GetStatus() != "accepted" {
		t.Fatalf("publish: %v %v", res, err)
	}
	br.expect("gRPC value on the dashboard", isMsg(f.elem.ID, 33))

	br.send(map[string]any{"type": "message_element", "element_id": f.elem.ID, "message": map[string]any{"value": 2.0}})
	if !watch.Receive() {
		t.Fatalf("watch: %v", watch.Err())
	}
	if m := watch.Msg().GetMessage(); m.GetElement() != "grpc-el" || m.GetMessage().GetStructValue().GetFields()["value"].GetNumberValue() != 2 {
		t.Fatalf("watch message %v", m)
	}
	// (disconnect is reported once the Publish call's presence lease expires too)
}

// Per-element limits on the WebSocket: drop keeps the first `burst`
// messages; latest delivers the newest value once the bucket refills.
func TestElementRateLimitOnWebSocket(t *testing.T) {
	inst := startInstance(t, "gw-wslimit", false)
	ctx := context.Background()
	f := setupRealtime(t, "wslimit", 0)
	two, one := 2.0, 1
	if _, err := svc().UpdateElement(ctx, service.SystemActor, f.elem.ID, service.ElementPatch{
		Limits: service.ElementLimits{MsgRate: &two, MsgBurst: &one}}); err != nil {
		t.Fatal(err)
	}
	waitRate := func(want float64) {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if d, ok := inst.gw.Device(f.device.ID); ok && len(d.Elements) == 1 && d.Elements[0].Rate == want {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("registry never got rate %v", want)
	}
	waitRate(2)
	br, err := dialWS(t, wsURL(inst.URL, "/browser/simple/"), browserHeader(f.cookie(inst.URL), inst.URL))
	if err != nil {
		t.Fatal(err)
	}
	br.send(map[string]any{"type": "subscribe", "element_id": f.elem.ID})
	br.expect("subscribe confirm", func(m map[string]any) bool { return m["type"] == "subscribe" })
	dev, err := dialWS(t, wsURL(inst.URL, "/device/node_red/"), deviceHeader(t, f.key, f.device.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	count := func(from, to int) []float64 {
		for v := from; v < to; v++ {
			dev.send(map[string]any{"element_id": f.elem.ID, "message": map[string]any{"value": v}})
		}
		var got []float64
		timeout := time.After(1500 * time.Millisecond)
		for {
			select {
			case m := <-br.ch:
				if m["type"] == "message_element" {
					got = append(got, m["message"].(map[string]any)["value"].(float64))
				}
			case <-timeout:
				return got
			}
		}
	}
	if got := count(0, 10); len(got) != 1 || got[0] != 0 {
		t.Fatalf("drop, burst 1: got %v, want [0]", got)
	}
	latest := "latest"
	if _, err := svc().UpdateElement(ctx, service.SystemActor, f.elem.ID, service.ElementPatch{
		Limits: service.ElementLimits{OverLimit: &latest}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if d, _ := inst.gw.Device(f.device.ID); d != nil && d.Elements[0].OverLimit == "latest" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("registry never got over_limit=latest")
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(600 * time.Millisecond) // refill
	if got := count(100, 110); len(got) != 2 || got[0] != 100 || got[1] != 109 {
		t.Fatalf("latest: got %v, want [100 109]", got)
	}
}
