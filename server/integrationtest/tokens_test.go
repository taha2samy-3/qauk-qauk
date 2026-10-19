//go:build integration

package integrationtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// API tokens act as their machine user, within their scopes, and only on
// the endpoints that accept tokens.
func TestAPITokens(t *testing.T) {
	ctx := context.Background()
	inst := startInstance(t, "gw-tokens", false)
	fix := setupRealtime(t, "tokens", 10)

	mkUser(t, "tok-admin", true)
	admin := newClient(t, inst.URL)
	admin.login("tok-admin", "tok-admin-password")

	// a machine user, granted the element read-only
	var mu store.User
	admin.must(http.StatusCreated, http.MethodPost, "/api/v1/admin/machine-users", map[string]any{"name": "wallboard"}, &mu)
	if mu.Kind != store.UserService || mu.IsAdmin {
		t.Fatalf("machine user: %+v", mu)
	}
	if _, err := svc().SetPermission(ctx, service.SystemActor, service.PermissionInput{ElementID: fix.elem.ID, UserID: &mu.ID, Permission: "R"}); err != nil {
		t.Fatal(err)
	}
	newToken := func(scopes []string, expires *time.Time) (string, store.APIToken) {
		t.Helper()
		var out struct {
			store.APIToken
			Token string `json:"token"`
		}
		body := map[string]any{"name": "t", "scopes": scopes}
		if expires != nil {
			body["expires_at"] = expires
		}
		admin.must(http.StatusCreated, http.MethodPost, fmt.Sprintf("/api/v1/admin/machine-users/%d/tokens", mu.ID), body, &out)
		if !strings.HasPrefix(out.Token, service.TokenPrefix) || !strings.HasPrefix(out.Token, out.Prefix) {
			t.Fatalf("token %q prefix %q", out.Token, out.Prefix)
		}
		return out.Token, out.APIToken
	}
	readTok, readMeta := newToken([]string{"read"}, nil)

	c := newClient(t, inst.URL) // no session
	bearer := func(tok string) []string { return []string{"Authorization", "Bearer " + tok} }

	// read: its elements and their history
	r := c.do(http.MethodGet, "/api/v1/me/elements", nil, bearer(readTok)...)
	var els []store.UserElement
	if r.Status != http.StatusOK || json.Unmarshal(r.Body, &els) != nil || len(els) != 1 || els[0].ID != fix.elem.ID || els[0].Permission != "R" {
		t.Fatalf("elements: %d %s", r.Status, r.Body)
	}
	if r := c.do(http.MethodGet, "/api/v1/elements/"+fix.elem.ID.String()+"/history", nil, bearer(readTok)...); r.Status != http.StatusOK {
		t.Fatalf("history: %d %s", r.Status, r.Body)
	}

	// default deny: endpoints that don't accept tokens, admin above all
	for _, p := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodGet, "/api/v1/dashboards"},
		{http.MethodGet, "/api/v1/admin/users"},
		{http.MethodGet, "/api/v1/admin/machine-users"},
	} {
		if r := c.do(p.method, p.path, nil, bearer(readTok)...); r.Status != http.StatusForbidden {
			t.Errorf("%s %s with a token: %d %s", p.method, p.path, r.Status, r.Body)
		}
	}
	// a cross-site write with a token isn't a CSRF (no cookie): it reaches the
	// handler, which refuses tokens there
	r = c.do(http.MethodPost, "/api/v1/dashboards", map[string]any{"name": "x"},
		append(bearer(readTok), "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site")...)
	if r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), "API tokens") {
		t.Fatalf("cross-site POST with a token: %d %s", r.Status, r.Body)
	}

	// scopes
	ctlTok, _ := newToken([]string{"control"}, nil)
	if r := c.do(http.MethodGet, "/api/v1/me/elements", nil, bearer(ctlTok)...); r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), "read scope") {
		t.Fatalf("token without read: %d %s", r.Status, r.Body)
	}

	// expired, revoked, unknown, deactivated user: 401
	soon := time.Now().Add(time.Second)
	expTok, _ := newToken([]string{"read"}, &soon)
	time.Sleep(1500 * time.Millisecond)
	admin.must(http.StatusNoContent, http.MethodDelete, fmt.Sprintf("/api/v1/admin/machine-users/%d/tokens/%s", mu.ID, readMeta.ID), nil, nil)
	for name, tok := range map[string]string{"expired": expTok, "revoked": readTok, "unknown": service.TokenPrefix + strings.Repeat("A", 43)} {
		if r := c.do(http.MethodGet, "/api/v1/me/elements", nil, bearer(tok)...); r.Status != http.StatusUnauthorized {
			t.Errorf("%s token: %d %s", name, r.Status, r.Body)
		}
	}
	liveTok, _ := newToken([]string{"read"}, nil)
	admin.must(http.StatusOK, http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", mu.ID), map[string]any{"is_active": false}, nil)
	if r := c.do(http.MethodGet, "/api/v1/me/elements", nil, bearer(liveTok)...); r.Status != http.StatusUnauthorized {
		t.Errorf("deactivated machine user: %d %s", r.Status, r.Body)
	}

	// a machine user has no password, can't be admin, can't log in
	if r := admin.do(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", mu.ID), map[string]any{"password": "longenough1"}); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("password on a machine user: %d %s", r.Status, r.Body)
	}
	if r := admin.do(http.MethodPatch, fmt.Sprintf("/api/v1/admin/users/%d", mu.ID), map[string]any{"is_admin": true}); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("admin machine user: %d %s", r.Status, r.Body)
	}
	if r := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "wallboard", "password": "anything-goes"}); r.Status != http.StatusUnauthorized {
		t.Errorf("machine user login: %d %s", r.Status, r.Body)
	}
	// tokens belong to machine users only
	human, err := store.GetUserByUsername(ctx, pool, "tok-admin")
	if err != nil {
		t.Fatal(err)
	}
	if r := admin.do(http.MethodPost, fmt.Sprintf("/api/v1/admin/machine-users/%d/tokens", human.ID), map[string]any{"name": "t", "scopes": []string{"read"}}); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("token for a human: %d %s", r.Status, r.Body)
	}
	// the audit log names the token actions, never the secret
	var audit []struct {
		Action string          `json:"action"`
		Entity string          `json:"entity"`
		Data   json.RawMessage `json:"data"`
	}
	admin.must(http.StatusOK, http.MethodGet, "/api/v1/admin/audit?limit=50", nil, &audit)
	created := 0
	for _, e := range audit {
		if e.Entity == "api_token" && e.Action == "create" {
			created++
		}
		if strings.Contains(string(e.Data), service.TokenPrefix) && !strings.Contains(string(e.Data), `"prefix"`) {
			t.Errorf("a token secret in the audit log: %s", e.Data)
		}
		if strings.Contains(string(e.Data), readTok) {
			t.Errorf("the token in the audit log: %s", e.Data)
		}
	}
	if created < 4 {
		t.Errorf("want the token creations audited, got %d", created)
	}
}
