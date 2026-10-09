// Package httpserver assembles the HTTP surface for `quack serve`.
package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/taha2samy/quackquack/server/internal/api"
	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/gateway"
	"github.com/taha2samy/quackquack/server/internal/history"
)

func New(cfg *config.Config, pool *pgxpool.Pool, hist history.Store, gw *gateway.Gateway, log *slog.Logger) http.Handler {
	r := chi.NewRouter()
	// No RealIP: X-Forwarded-For is client-controlled unless a trusted proxy strips it,
	// and the login rate limiter keys on the peer address.
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		if gw != nil && !gw.Ready() {
			http.Error(w, "device registry loading", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	r.Handle("/metrics", promhttp.Handler())

	if gw != nil {
		for _, p := range []string{"/device/node_red/", "/device/node_red"} {
			r.Handle(p, gw.DeviceHandler())
		}
		r.Get("/device/elements", gw.DeviceElementsHandler().ServeHTTP)
		// REST device adapter (docs/04_api_reference/device_rest_api.md)
		r.Get("/device/v1/elements", gw.DeviceElementsHandler().ServeHTTP)
		r.Post("/device/v1/messages", gw.RESTMessagesHandler().ServeHTTP)
		r.Get("/device/v1/sync", gw.RESTSyncHandler().ServeHTTP)
		for _, p := range []string{"/browser/simple/", "/browser/simple"} {
			r.Handle(p, gw.BrowserHandler())
		}
	}

	if cfg.HasRole("api") {
		a := api.New(cfg, pool, hist, log)
		// CSRF: reject cross-origin unsafe requests (Sec-Fetch-Site / Origin based).
		cop := http.NewCrossOriginProtection()
		for _, o := range cfg.AllowedOrigins {
			if o = strings.TrimSpace(o); o != "" {
				if err := cop.AddTrustedOrigin(o); err != nil {
					log.Warn("ignoring invalid allowed origin", "origin", o, "err", err)
				}
			}
		}
		r.Group(func(r chi.Router) {
			r.Use(cop.Handler, a.SessionMiddleware)
			humaAPI := humachi.New(r, api.Config())
			a.Register(humaAPI)
		})
		if dir := cfg.WebDir; dir != "" {
			if st, err := os.Stat(dir); err == nil && st.IsDir() {
				r.Handle("/*", spa(dir))
			}
		}
	}
	return r
}

// spa serves static files and falls back to index.html for client-side routes.
func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err != nil || st.IsDir() && r.URL.Path != "/" {
			if _, err := os.Stat(filepath.Join(p, "index.html")); err != nil {
				http.ServeFile(w, r, filepath.Join(dir, "index.html"))
				return
			}
		}
		fs.ServeHTTP(w, r)
	})
}
