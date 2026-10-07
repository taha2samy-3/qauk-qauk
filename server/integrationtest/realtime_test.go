//go:build integration

package integrationtest

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type realtimeFixture struct {
	key    deviceKey
	device store.Device
	elem   store.Element
	cookie func(base string) string
}

func setupRealtime(t *testing.T, prefix string, points int) realtimeFixture {
	ctx := context.Background()
	s := svc()
	k := newECKey(t)
	key, err := s.CreateKey(ctx, service.SystemActor, prefix+"-key", k.pem, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDevice(ctx, service.SystemActor, service.DeviceInput{Name: prefix + "-dev", PublicKeyID: &key.ID})
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.CreateElement(ctx, service.SystemActor, service.ElementInput{DeviceID: d.ID, Name: prefix + "-el", Points: points})
	if err != nil {
		t.Fatal(err)
	}
	mkUser(t, prefix+"-user", false)
	u, _ := store.GetUserByUsername(ctx, pool, prefix+"-user")
	if _, err := s.SetPermission(ctx, service.SystemActor, service.PermissionInput{ElementID: e.ID, UserID: &u.ID, Permission: "RC"}); err != nil {
		t.Fatal(err)
	}
	return realtimeFixture{key: k, device: d, elem: e, cookie: func(base string) string {
		c := newClient(t, base)
		c.login(prefix+"-user", prefix+"-user-password")
		return c.cookieHeader()
	}}
}

func browserHeader(cookie, origin string) http.Header {
	return http.Header{"Cookie": {cookie}, "Origin": {origin}}
}

func isMsg(el uuid.UUID, v float64) func(map[string]any) bool {
	return func(m map[string]any) bool {
		msg, _ := m["message"].(map[string]any)
		return m["type"] == "message_element" && m["element_id"] == el.String() && msg != nil && msg["value"] == v
	}
}

// Two gateway instances share Redpanda: a device on gw1 reaches a browser on
// gw2, commands flow back, and presence crosses instances.
func TestCrossInstanceDelivery(t *testing.T) {
	gw1 := startInstance(t, "gw-x1", false)
	gw2 := startInstance(t, "gw-x2", false)
	f := setupRealtime(t, "xinst", 10)

	br, err := dialWS(t, wsURL(gw2.URL, "/browser/simple/"), browserHeader(f.cookie(gw2.URL), gw2.URL))
	if err != nil {
		t.Fatal(err)
	}
	br.send(map[string]any{"type": "subscribe", "element_id": f.elem.ID})
	br.expect("subscribe confirm", func(m map[string]any) bool { return m["type"] == "subscribe" && m["connected"] == false })

	dev, err := dialWS(t, wsURL(gw1.URL, "/device/node_red/"), deviceHeader(t, f.key, f.device.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	br.expect("connected status via presence topic", func(m map[string]any) bool {
		return m["type"] == "element_connection_status" && m["status"] == "connected"
	})
	start := time.Now()
	dev.send(map[string]any{"element_id": f.elem.ID, "message": map[string]any{"value": 41.5}})
	got := br.expect("device value on other instance", isMsg(f.elem.ID, 41.5))
	t.Logf("cross-instance device->browser latency: %s", time.Since(start))
	if auth := got["auth"].(map[string]any); auth["user_id"] != f.device.ID.String() || auth["username"] != f.device.Name {
		t.Fatalf("actor not stamped from device identity: %v", auth)
	}

	br.send(map[string]any{"type": "message_element", "element_id": f.elem.ID, "message": map[string]any{"value": 1.0}})
	cmd := dev.expect("command on device via other instance", func(m map[string]any) bool {
		msg, _ := m["message"].(map[string]any)
		return msg != nil && msg["value"] == 1.0
	})
	if _, hasType := cmd["type"]; hasType {
		t.Fatal("device frame must not have a type field")
	}

	_ = dev.c.CloseNow()
	br.expect("disconnected status via presence topic", func(m map[string]any) bool {
		return m["type"] == "element_connection_status" && m["status"] == "disconnected"
	})
}

// History survives a full gateway restart: the ring buffer is gone, so the
// replay must come from TimescaleDB (written by the ingester).
func TestHistoryFromTSDBAfterRestart(t *testing.T) {
	f := setupRealtime(t, "hist", 5)
	gwA := startInstance(t, "gw-h1", true)
	dev, err := dialWS(t, wsURL(gwA.URL, "/device/node_red/"), deviceHeader(t, f.key, f.device.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 8; i++ {
		dev.send(map[string]any{"element_id": f.elem.ID, "message": map[string]any{"value": float64(i)}})
	}
	ctx := context.Background()
	deadline := time.Now().Add(15 * time.Second)
	for {
		rows, _ := store.LastDeviceEvents(ctx, pool, f.elem.ID, 100)
		if len(rows) == 8 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ingester wrote %d/8 rows", len(rows))
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = dev.c.CloseNow()

	gwB := startInstance(t, "gw-h2", false) // fresh process: empty ring buffers
	br, err := dialWS(t, wsURL(gwB.URL, "/browser/simple/"), browserHeader(f.cookie(gwB.URL), gwB.URL))
	if err != nil {
		t.Fatal(err)
	}
	br.send(map[string]any{"type": "subscribe", "element_id": f.elem.ID})
	br.expect("confirm", func(m map[string]any) bool { return m["type"] == "subscribe" })
	for want := 4.0; want <= 8; want++ {
		m := br.expect(fmt.Sprintf("history %v", want), func(m map[string]any) bool { return m["type"] == "message_element" })
		if v := m["message"].(map[string]any)["value"]; v != want {
			t.Fatalf("history out of order: got %v want %v", v, want)
		}
	}

	// History API agrees, raw and aggregated.
	c := newClient(t, gwB.URL)
	c.login("hist-user", "hist-user-password")
	var raw struct {
		Events []struct{ Value *float64 } `json:"events"`
	}
	c.must(200, "GET", "/api/v1/elements/"+f.elem.ID.String()+"/history?step=raw", nil, &raw)
	if len(raw.Events) != 8 {
		t.Fatalf("raw history: %d events", len(raw.Events))
	}
	var agg struct {
		Buckets []struct {
			Avg *float64
			N   int64
		} `json:"buckets"`
	}
	c.must(200, "GET", "/api/v1/elements/"+f.elem.ID.String()+"/history?step=1m", nil, &agg)
	var n int64
	for _, b := range agg.Buckets {
		n += b.N
	}
	if n != 8 {
		t.Fatalf("aggregated history counts %d values (real-time aggregation off?)", n)
	}
	// Someone without permission gets 404, not data.
	mkUser(t, "hist-stranger", false)
	s := newClient(t, gwB.URL)
	s.login("hist-stranger", "hist-stranger-password")
	s.must(404, "GET", "/api/v1/elements/"+f.elem.ID.String()+"/history", nil, nil)
}

// Deactivating a user closes their live sockets (session revoked).
func TestDeactivationClosesBrowserSocket(t *testing.T) {
	in := startInstance(t, "gw-deact", false)
	f := setupRealtime(t, "deact", 1)
	br, err := dialWS(t, wsURL(in.URL, "/browser/simple/"), browserHeader(f.cookie(in.URL), in.URL))
	if err != nil {
		t.Fatal(err)
	}
	u, _ := store.GetUserByUsername(context.Background(), pool, "deact-user")
	off := false
	if _, err := svc().UpdateUser(context.Background(), service.SystemActor, u.ID, service.UserPatch{IsActive: &off}); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-br.ch:
		for ok {
			_, ok = <-br.ch
		}
	case <-time.After(5 * time.Second):
		t.Fatal("socket of deactivated user still open")
	}
}

// A crashed gateway's leases expire and devices are reported disconnected (B9).
func TestPresenceSweepAfterCrash(t *testing.T) {
	in := startInstance(t, "gw-sweep", false)
	f := setupRealtime(t, "sweep", 1)
	ctx := context.Background()
	// Simulate a lease left behind by a gateway that died.
	if err := store.InsertPresence(ctx, pool, f.device.ID, "gw-dead", "c-1"); err != nil {
		t.Fatal(err)
	}
	br, err := dialWS(t, wsURL(in.URL, "/browser/simple/"), browserHeader(f.cookie(in.URL), in.URL))
	if err != nil {
		t.Fatal(err)
	}
	br.send(map[string]any{"type": "subscribe", "element_id": f.elem.ID})
	br.expect("confirm says connected (stale lease)", func(m map[string]any) bool { return m["type"] == "subscribe" && m["connected"] == true })
	br.expect("sweeper reports disconnected", func(m map[string]any) bool {
		return m["type"] == "element_connection_status" && m["status"] == "disconnected"
	})
	if ok, _ := store.DeviceConnected(ctx, pool, f.device.ID, 3*time.Second); ok {
		t.Fatal("stale lease still present")
	}
}

// GET /device/elements lists the calling device's elements with the socket's JWT.
func TestDeviceElementsEndpoint(t *testing.T) {
	inst := startInstance(t, "gw-del", false)
	f := setupRealtime(t, "delist", 5)
	other := setupRealtime(t, "delist-other", 5)
	c := newClient(t, inst.URL)

	if r := c.do("GET", "/device/elements", nil); r.Status != http.StatusForbidden {
		t.Fatalf("no token: status %d", r.Status)
	}
	if r := c.do("GET", "/device/elements", nil, "Authorization", "Bearer nope"); r.Status != http.StatusForbidden {
		t.Fatalf("bad token: status %d", r.Status)
	}
	// someone else's key for this device id is rejected
	if r := c.do("GET", "/device/elements", nil, "Authorization", deviceHeader(t, other.key, f.device.ID.String()).Get("Authorization")); r.Status != http.StatusForbidden {
		t.Fatalf("wrong key: status %d", r.Status)
	}

	r := c.do("GET", "/device/elements", nil, "Authorization", deviceHeader(t, f.key, f.device.ID.String()).Get("Authorization"))
	if r.Status != http.StatusOK {
		t.Fatalf("status %d: %s", r.Status, r.Body)
	}
	var out struct {
		Device   struct{ ID, Name string }
		Elements []struct {
			ID, Name string
			Points   int
		}
	}
	r.JSON(t, &out)
	if out.Device.ID != f.device.ID.String() || out.Device.Name != "delist-dev" {
		t.Fatalf("device = %+v", out.Device)
	}
	if len(out.Elements) != 1 || out.Elements[0].ID != f.elem.ID.String() || out.Elements[0].Name != "delist-el" || out.Elements[0].Points != 5 {
		t.Fatalf("elements = %+v", out.Elements)
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control %q", r.Header.Get("Cache-Control"))
	}
}
