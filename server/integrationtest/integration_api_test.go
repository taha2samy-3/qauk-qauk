//go:build integration

package integrationtest

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// An API token does what a dashboard does: list its elements with their
// newest values, read alerts, and send commands that reach the device.
func TestIntegrationAPI(t *testing.T) {
	ctx := context.Background()
	inst := startInstance(t, "gw-integ", true) // with the ingester: newest values come from history
	fix := setupRealtime(t, "integ", 10)
	s := svc()

	bot, err := s.CreateMachineUser(ctx, service.SystemActor, service.MachineUserInput{Name: "integ-bot"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPermission(ctx, service.SystemActor, service.PermissionInput{ElementID: fix.elem.ID, UserID: &bot.ID, Permission: "RC"}); err != nil {
		t.Fatal(err)
	}
	_, full, err := s.CreateAPIToken(ctx, service.SystemActor, bot.ID, service.TokenInput{Name: "full", Scopes: []string{"read", "control", "alerts"}})
	if err != nil {
		t.Fatal(err)
	}
	_, readOnly, _ := s.CreateAPIToken(ctx, service.SystemActor, bot.ID, service.TokenInput{Name: "ro", Scopes: []string{"read"}})
	// a second machine user that may only read the element
	viewer, _ := s.CreateMachineUser(ctx, service.SystemActor, service.MachineUserInput{Name: "integ-viewer"})
	if _, err := s.SetPermission(ctx, service.SystemActor, service.PermissionInput{ElementID: fix.elem.ID, UserID: &viewer.ID, Permission: "R"}); err != nil {
		t.Fatal(err)
	}
	_, viewerCtl, _ := s.CreateAPIToken(ctx, service.SystemActor, viewer.ID, service.TokenInput{Name: "ctl", Scopes: []string{"control"}})

	c := newClient(t, inst.URL)
	as := func(tok string) []string { return []string{"Authorization", "Bearer " + tok} }
	dev := deviceHeader(t, fix.key, fix.device.ID.String()).Get("Authorization")

	// the device reports; the newest value shows in the list
	r := c.do(http.MethodPost, "/device/v1/messages", []any{map[string]any{"element": fix.elem.Name,
		"message": map[string]any{"value": 21.5, "climate": map[string]any{"humidity": 40}}}}, "Authorization", dev)
	if r.Status != http.StatusOK {
		t.Fatalf("device publish: %d %s", r.Status, r.Body)
	}
	type element struct {
		ID         string `json:"id"`
		DeviceName string `json:"device_name"`
		Permission string `json:"permission"`
		Latest     *struct {
			Message json.RawMessage `json:"message"`
			Value   *float64        `json:"value"`
		} `json:"latest"`
	}
	var els []element
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		r = c.do(http.MethodGet, "/api/v1/elements?latest=true", nil, as(readOnly)...)
		if r.Status != http.StatusOK {
			t.Fatalf("elements: %d %s", r.Status, r.Body)
		}
		els = nil
		r.JSON(t, &els)
		if len(els) == 1 && els[0].Latest != nil || time.Now().After(deadline) {
			break
		}
	}
	if len(els) != 1 || els[0].ID != fix.elem.ID.String() || els[0].DeviceName != fix.device.Name || els[0].Permission != "RC" ||
		els[0].Latest == nil || els[0].Latest.Value == nil || *els[0].Latest.Value != 21.5 {
		t.Fatalf("elements with latest: %s", r.Body)
	}
	r = c.do(http.MethodGet, "/api/v1/elements/"+fix.elem.ID.String()+"/latest", nil, as(readOnly)...)
	if r.Status != http.StatusOK || !strings.Contains(string(r.Body), `"humidity"`) {
		t.Fatalf("latest: %d %s", r.Status, r.Body)
	}

	// alerts: the rules of its elements, with their state (alerts scope)
	if _, err := s.SaveAlertRule(ctx, service.SystemActor, fix.elem.ID, service.AlertRuleInput{Name: "Dry", Field: "climate.humidity",
		Condition: "below", Threshold: 30, Severity: "warning"}); err != nil {
		t.Fatal(err)
	}
	var alerts []store.VisibleAlert
	c.do(http.MethodGet, "/api/v1/alerts?state=all", nil, as(full)...).JSON(t, &alerts)
	if len(alerts) != 1 || alerts[0].Name != "Dry" || alerts[0].Field != "climate.humidity" || alerts[0].Firing || alerts[0].DeviceName != fix.device.Name {
		t.Fatalf("alerts: %+v", alerts)
	}
	if r := c.do(http.MethodGet, "/api/v1/alerts", nil, as(full)...); r.Status != http.StatusOK || strings.TrimSpace(string(r.Body)) != "[]" {
		t.Fatalf("firing alerts: %d %s", r.Status, r.Body)
	}
	if r := c.do(http.MethodGet, "/api/v1/alerts", nil, as(readOnly)...); r.Status != http.StatusForbidden {
		t.Fatalf("alerts without the scope: %d %s", r.Status, r.Body)
	}

	// a command reaches the device (REST long-poll), like a dashboard's
	done := make(chan resp, 1)
	go func() { done <- c.do(http.MethodGet, "/device/v1/sync?wait=8s", nil, "Authorization", dev) }()
	time.Sleep(300 * time.Millisecond)
	r = c.do(http.MethodPost, "/api/v1/elements/"+fix.elem.ID.String()+"/commands", map[string]any{"message": map[string]any{"value": 1}}, as(full)...)
	var acc struct {
		CommandID string `json:"command_id"`
		Status    string `json:"status"`
	}
	if r.Status != http.StatusAccepted || json.Unmarshal(r.Body, &acc) != nil || acc.Status != "accepted" || acc.CommandID == "" {
		t.Fatalf("command: %d %s", r.Status, r.Body)
	}
	select {
	case r := <-done:
		var out struct{ Messages []map[string]any }
		r.JSON(t, &out)
		if len(out.Messages) != 1 || out.Messages[0]["element"] != fix.elem.Name {
			t.Fatalf("device got: %s", r.Body)
		}
		if msg, _ := out.Messages[0]["message"].(map[string]any); msg["value"] != 1.0 {
			t.Fatalf("device got: %s", r.Body)
		}
	case <-time.After(9 * time.Second):
		t.Fatal("the command didn't reach the device")
	}
	// what became of it: delivered, by the REST poll; only its sender sees it
	var status struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		r := c.do(http.MethodGet, "/api/v1/commands/"+acc.CommandID, nil, as(full)...)
		if r.Status != http.StatusOK {
			t.Fatalf("command status: %d %s", r.Status, r.Body)
		}
		r.JSON(t, &status)
		if status.Status != "accepted" || time.Now().After(deadline) {
			break
		}
	}
	if status.Status != "delivered" || !strings.Contains(status.Reason, "REST poll") {
		t.Fatalf("command status: %+v", status)
	}
	if r := c.do(http.MethodGet, "/api/v1/commands/"+acc.CommandID, nil, as(viewerCtl)...); r.Status != http.StatusNotFound {
		t.Fatalf("another account's command: %d", r.Status)
	}

	// refusals: no control scope, only R, a bad message
	for name, tc := range map[string]struct {
		tok    string
		body   any
		status int
	}{
		"read-only token": {readOnly, map[string]any{"message": map[string]any{"value": 1}}, http.StatusForbidden},
		"R grant only":    {viewerCtl, map[string]any{"message": map[string]any{"value": 1}}, http.StatusForbidden},
		"null message":    {full, map[string]any{"message": nil}, http.StatusUnprocessableEntity},
	} {
		if r := c.do(http.MethodPost, "/api/v1/elements/"+fix.elem.ID.String()+"/commands", tc.body, as(tc.tok)...); r.Status != tc.status {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}

	// the command is audited with the token that sent it
	var actor string
	if err := pool.QueryRow(ctx, `SELECT actor_name FROM audit_log WHERE action = 'command' AND entity_id = $1`, fix.elem.ID.String()).Scan(&actor); err != nil || actor != "integ-bot (token full)" {
		t.Fatalf("audit: %q %v", actor, err)
	}

	// commands are rate-limited per caller (10/s, burst 20 by default)
	limited := false
	for i := 0; i < 30 && !limited; i++ {
		r := c.do(http.MethodPost, "/api/v1/elements/"+fix.elem.ID.String()+"/commands", map[string]any{"message": map[string]any{"value": i}}, as(full)...)
		limited = r.Status == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("30 commands at once must hit the rate limit")
	}
}
