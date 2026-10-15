package httpserver

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/gateway"
)

func TestProbes(t *testing.T) {
	cfg := &config.Config{
		Roles: []string{"gateway"},
	}
	gw := gateway.New(t.Context(), cfg, nil, nil, nil, slog.Default())
	handler := New(cfg, nil, nil, gw, &Lifecycle{}, slog.Default())

	// Liveness: /livez and /healthz
	for _, p := range []string{"/livez", "/healthz"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s returned %d, want 200", p, rec.Code)
		}
		if rec.Body.String() != "ok" {
			t.Errorf("%s body = %q, want 'ok'", p, rec.Body.String())
		}
	}

	// Startup probe: /startupz
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/startupz", nil)
		handler.ServeHTTP(rec, req)
		// registry is not ready yet in an unstarted test gateway, so it returns 503 gateway initializing
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("/startupz before init returned %d, want 503", rec.Code)
		}
	}

	// Readiness: initially not ready (unstarted gateway registry)
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		handler.ServeHTTP(rec, req)
		// registry is not ready yet in an unstarted test gateway, so it returns 503 gateway not ready
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("/readyz returned %d, want 503 (registry loading)", rec.Code)
		}
	}

	// When draining, /readyz must return 503 "server draining"
	gw.StartDrain()

	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("/readyz during drain returned %d, want 503", rec.Code)
		}
		if rec.Body.String() != "server draining\n" {
			t.Errorf("/readyz body during drain = %q, want 'server draining\\n'", rec.Body.String())
		}
	}

	// Incoming device and browser WebSockets must immediately be rejected with 503 when draining
	for _, path := range []string{"/device/node_red/", "/browser/simple/"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s during drain returned %d, want 503", path, rec.Code)
		}
	}

	// Liveness must remain 200 even while draining!
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/livez", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("/livez during drain returned %d, want 200", rec.Code)
		}
	}
}

func probe(t *testing.T, h http.Handler, path string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code, rec.Body.String()
}

// An api-only server is ready until it drains (the readiness flag is shared
// by every role, not only gateways).
func TestAPIRoleProbes(t *testing.T) {
	cfg := &config.Config{Roles: []string{"api"}}
	lc := &Lifecycle{}
	h := New(cfg, nil, nil, nil, lc, slog.Default())
	for _, p := range []string{"/readyz", "/startupz", "/livez"} {
		if code, _ := probe(t, h, p); code != http.StatusOK {
			t.Fatalf("%s = %d, want 200 when ready", p, code)
		}
	}
	lc.StartDraining()
	if code, body := probe(t, h, "/readyz"); code != http.StatusServiceUnavailable || body != "server draining\n" {
		t.Fatalf("/readyz while draining = %d %q", code, body)
	}
	if code, _ := probe(t, h, "/livez"); code != http.StatusOK {
		t.Fatal("liveness stays 200 while draining")
	}
}
