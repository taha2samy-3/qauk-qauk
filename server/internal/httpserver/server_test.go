package httpserver

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/gateway"
)

func TestProbes(t *testing.T) {
	cfg := &config.Config{
		Roles: []string{"gateway"},
	}
	gw := gateway.New(t.Context(), cfg, nil, nil, nil, slog.Default())
	handler := New(cfg, nil, nil, gw, slog.Default())

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
	cfg.DrainDuration = 10 * time.Millisecond
	cfg.DrainPropagationWait = 0
	go gw.Drain(t.Context())
	// wait briefly for draining flag to flip
	time.Sleep(5 * time.Millisecond)

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
