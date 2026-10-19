//go:build integration

package integrationtest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/alert"
	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/service"
)

func isType(typ string) func(map[string]any) bool {
	return func(m map[string]any) bool { return m["type"] == typ }
}

// waitClosed waits until the server closes the socket.
func (w *wsConn) waitClosed(desc string) {
	w.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-w.ch:
			if !ok {
				return
			}
		case <-deadline:
			w.t.Fatalf("%s: still open", desc)
		}
	}
}

// The integration stream on one gateway sees what happens on another: device
// values, alerts; its commands reach the device; scopes, new grants and
// revocations apply live.
func TestIntegrationStream(t *testing.T) {
	ctx := context.Background()
	gw1 := startInstance(t, "gw-stream-1", false)
	gw2 := startInstance(t, "gw-stream-2", false)
	fix := setupRealtime(t, "stream", 10)
	s := svc()

	bot, err := s.CreateMachineUser(ctx, service.SystemActor, service.MachineUserInput{Name: "stream-bot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPermission(ctx, service.SystemActor, service.PermissionInput{ElementID: fix.elem.ID, UserID: &bot.ID, Permission: "RC"}); err != nil {
		t.Fatal(err)
	}
	full, tok, err := s.CreateAPIToken(ctx, service.SystemActor, bot.ID, service.TokenInput{Name: "full", Scopes: []string{"read", "control", "alerts"}})
	if err != nil {
		t.Fatal(err)
	}
	_, roTok, _ := s.CreateAPIToken(ctx, service.SystemActor, bot.ID, service.TokenInput{Name: "ro", Scopes: []string{"read"}})
	if _, err := s.SaveAlertRule(ctx, service.SystemActor, fix.elem.ID, service.AlertRuleInput{Name: "Hot", Condition: "above",
		Threshold: 30, Severity: "critical"}); err != nil {
		t.Fatal(err)
	}
	ectx, stopEngine := context.WithCancel(ctx)
	defer stopEngine()
	go func() {
		_ = (&alert.Engine{Pool: pool, Brokers: brokers, Group: bus.TopicName("quack-alerts-it-" + uuid.NewString()[:8]), Log: logger}).Run(ectx)
	}()
	time.Sleep(3 * time.Second) // the engine joins its group

	// a token in the header; subscribe to everything it may read, and alerts
	st, err := dialWS(t, wsURL(gw2.URL, "/api/v1/stream"), http.Header{"Authorization": {"Bearer " + tok}})
	if err != nil {
		t.Fatal(err)
	}
	if w := st.expect("welcome", isType("welcome")); w["protocol"] != 1.0 || w["user"] != "stream-bot" {
		t.Fatalf("welcome: %v", w)
	}
	st.send(map[string]any{"type": "subscribe", "id": "1", "all": true, "alerts": true})
	sub := st.expect("subscribed", isType("subscribed"))
	els, _ := sub["elements"].([]any)
	if sub["id"] != "1" || len(els) != 1 || els[0].(map[string]any)["id"] != fix.elem.ID.String() ||
		els[0].(map[string]any)["permission"] != "RC" || sub["alerts"] != true {
		t.Fatalf("subscribed: %v", sub)
	}

	// a device value on gw1 reaches the stream on gw2
	c1 := newClient(t, gw1.URL)
	dev := deviceHeader(t, fix.key, fix.device.ID.String()).Get("Authorization")
	publish := func(v float64) {
		t.Helper()
		r := c1.do(http.MethodPost, "/device/v1/messages", []any{map[string]any{"element": fix.elem.Name, "message": map[string]any{"value": v}}},
			"Authorization", dev)
		if r.Status != http.StatusOK {
			t.Fatalf("publish: %d %s", r.Status, r.Body)
		}
	}
	publish(25)
	v := st.expect("a device value", func(m map[string]any) bool {
		msg, _ := m["message"].(map[string]any)
		return m["type"] == "value" && m["element_id"] == fix.elem.ID.String() && msg["value"] == 25.0
	})
	if v["source"] != "device" || v["time"] == "" {
		t.Fatalf("value: %v", v)
	}

	// an alert fires: the stream hears it, with the attribute and value
	publish(35)
	a := st.expect("an alert", isType("alert"))
	if a["state"] != "firing" || a["rule_name"] != "Hot" || a["field"] != "value" || a["value"] != 35.0 || a["severity"] != "critical" {
		t.Fatalf("alert: %v", a)
	}

	// a command from the stream (gw2) reaches the device (REST long-poll, gw1)
	done := make(chan resp, 1)
	go func() { done <- c1.do(http.MethodGet, "/device/v1/sync?wait=8s", nil, "Authorization", dev) }()
	time.Sleep(300 * time.Millisecond)
	st.send(map[string]any{"type": "command", "id": "c1", "element_id": fix.elem.ID, "message": map[string]any{"value": 1}})
	if r := st.expect("command result", isType("command_result")); r["id"] != "c1" || r["status"] != "accepted" || r["command_id"] == "" {
		t.Fatalf("command_result: %v", r)
	}
	select {
	case r := <-done:
		if !strings.Contains(string(r.Body), fix.elem.Name) {
			t.Fatalf("device got: %s", r.Body)
		}
	case <-time.After(9 * time.Second):
		t.Fatal("the stream's command didn't reach the device")
	}
	// its delivery is reported back on the stream, with the request id
	if d := st.expect("delivery report", func(m map[string]any) bool {
		return m["type"] == "command_result" && m["status"] == "delivered"
	}); d["id"] != "c1" || !strings.Contains(d["reason"].(string), "REST poll") {
		t.Fatalf("delivered: %v", d)
	}

	// a device connected over WebSocket: handed to its connection
	dws, err := dialWS(t, wsURL(gw1.URL, "/device/node_red/"), deviceHeader(t, fix.key, fix.device.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	st.send(map[string]any{"type": "command", "id": "c2", "element_id": fix.elem.ID, "message": map[string]any{"value": 0}})
	dws.expect("the command on the device socket", func(m map[string]any) bool { return m["element_id"] == fix.elem.ID.String() })
	if d := st.expect("delivery to the socket", func(m map[string]any) bool {
		return m["type"] == "command_result" && m["id"] == "c2" && m["status"] == "delivered"
	}); !strings.Contains(d["reason"].(string), "device connection") {
		t.Fatalf("delivered: %v", d)
	}

	// scopes: a read-only token can't command nor hear alerts
	ro, err := dialWS(t, wsURL(gw1.URL, "/api/v1/stream"), http.Header{"Authorization": {"Bearer " + roTok}})
	if err != nil {
		t.Fatal(err)
	}
	ro.expect("welcome", isType("welcome"))
	ro.send(map[string]any{"type": "command", "id": "x", "element_id": fix.elem.ID, "message": map[string]any{"value": 1}})
	if e := ro.expect("refused command", isType("error")); e["id"] != "x" || e["code"] != "forbidden" {
		t.Fatalf("read-only command: %v", e)
	}
	ro.send(map[string]any{"type": "subscribe", "id": "y", "alerts": true})
	if e := ro.expect("refused alerts", isType("error")); e["id"] != "y" || e["code"] != "forbidden" {
		t.Fatalf("read-only alerts: %v", e)
	}

	// a subscribe replays the newest value (marked replay), then goes live
	ro.send(map[string]any{"type": "subscribe", "id": "z", "elements": []string{fix.elem.ID.String(), uuid.NewString()}})
	if sz := ro.expect("subscribed", isType("subscribed")); len(sz["denied"].([]any)) != 1 {
		t.Fatalf("an unknown element must be denied: %v", sz)
	}
	rp := ro.expect("the replayed newest value", isType("value"))
	if msg, _ := rp["message"].(map[string]any); rp["replay"] != true || msg["value"] != 35.0 {
		t.Fatalf("replay: %v", rp)
	}

	// browsers authenticate with a first frame; a bad token is closed
	ff, err := dialWS(t, wsURL(gw1.URL, "/api/v1/stream"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ff.send(map[string]any{"type": "auth", "token": roTok})
	ff.expect("welcome after auth frame", isType("welcome"))
	bad, err := dialWS(t, wsURL(gw1.URL, "/api/v1/stream"), nil)
	if err != nil {
		t.Fatal(err)
	}
	bad.send(map[string]any{"type": "auth", "token": service.TokenPrefix + strings.Repeat("x", 43)})
	bad.waitClosed("a bad token")
	if r := c1.do(http.MethodGet, "/api/v1/stream", nil, "Authorization", "Bearer "+service.TokenPrefix+strings.Repeat("x", 43)); r.Status != http.StatusUnauthorized {
		t.Fatalf("bad token upgrade: %d", r.Status)
	}

	// a stream on "all" follows a new grant
	e2, err := s.CreateElement(ctx, service.SystemActor, service.ElementInput{DeviceID: fix.device.ID, Name: "stream-el-2", Points: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPermission(ctx, service.SystemActor, service.PermissionInput{ElementID: e2.ID, UserID: &bot.ID, Permission: "R"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // the control event reaches gw2
	r := c1.do(http.MethodPost, "/device/v1/messages", []any{map[string]any{"element": "stream-el-2", "message": map[string]any{"value": 7}}}, "Authorization", dev)
	if r.Status != http.StatusOK {
		t.Fatalf("publish e2: %d %s", r.Status, r.Body)
	}
	st.expect("a value of the newly granted element", func(m map[string]any) bool {
		return m["type"] == "value" && m["element_id"] == e2.ID.String()
	})

	// revoking the token closes its stream at once
	if err := s.RevokeAPIToken(ctx, service.SystemActor, bot.ID, full.ID); err != nil {
		t.Fatal(err)
	}
	st.waitClosed("a revoked token's stream")
}
