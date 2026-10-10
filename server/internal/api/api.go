// Package api is the REST API (OpenAPI 3.1 via huma). Errors are RFC 9457
// Problem Details. Authentication is a session cookie; see internal/authn.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type API struct {
	cfg     *config.Config
	pool    *pgxpool.Pool
	hist    history.Store
	histSem chan struct{} // bounds concurrent history queries
	svc     *service.Service
	log     *slog.Logger
	limiter *loginLimiter
}

func New(cfg *config.Config, pool *pgxpool.Pool, hist history.Store, log *slog.Logger) *API {
	svc := service.New(pool)
	svc.AllowInsecureTLS = cfg.AllowInsecureTLS
	if cfg.ElementMsgRateMax > 0 {
		svc.ElementRateMax = cfg.ElementMsgRateMax
	}
	return &API{cfg: cfg, pool: pool, hist: hist, histSem: make(chan struct{}, max(cfg.HistoryMaxQueries, 1)),
		svc: svc, log: log, limiter: newLoginLimiter(10, 5*time.Minute)}
}

type ctxKey int

const (
	userKey ctxKey = iota
	sessionKey
	requestKey
)

// SessionMiddleware resolves the session cookie (if any) into the request context.
func (a *API) SessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), requestKey, r)
		if c, err := r.Cookie(authn.SessionCookie); err == nil && c.Value != "" {
			hash := authn.HashToken(c.Value)
			if u, err := store.SessionUser(ctx, a.pool, hash); err == nil {
				ctx = context.WithValue(ctx, userKey, u)
				ctx = context.WithValue(ctx, sessionKey, hash)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func currentUser(ctx context.Context) (store.User, error) {
	if u, ok := ctx.Value(userKey).(store.User); ok {
		return u, nil
	}
	return store.User{}, huma.Error401Unauthorized("authentication required")
}

func requireAdmin(ctx context.Context) (store.User, error) {
	u, err := currentUser(ctx)
	if err != nil {
		return u, err
	}
	if !u.IsAdmin {
		return u, huma.Error403Forbidden("admin privileges required")
	}
	return u, nil
}

func requestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(requestKey).(*http.Request)
	return r
}

// fail maps service/store errors to Problem Details.
func (a *API) fail(err error) error {
	if v, ok := service.IsValidation(err); ok {
		return huma.Error422UnprocessableEntity(v.Error(), &huma.ErrorDetail{Location: "body." + v.Field, Message: v.Msg})
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return huma.Error404NotFound("not found")
	case errors.Is(err, store.ErrConflict):
		return huma.Error409Conflict("already exists")
	}
	a.log.Error("api: internal error", "err", err)
	return huma.Error500InternalServerError("internal error")
}

// Register mounts every operation.
func (a *API) Register(api huma.API) {
	a.registerAuth(api)
	a.registerMe(api)
	a.registerAdminIdentity(api)
	a.registerAdminDevices(api)
	a.registerAdminOps(api)
	a.registerDashboards(api)
	a.registerAdminMQTT(api)
}

func Config() huma.Config {
	cfg := huma.DefaultConfig("Quack Quack API", "1.0.0")
	cfg.Info.Description = "Control-plane REST API. Realtime data uses the WebSocket protocol described in server/api/asyncapi.yaml."
	cfg.OpenAPIPath = "/api/openapi"
	cfg.DocsPath = "/api/docs"
	cfg.SchemasPath = "/api/schemas"
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"session": {Type: "apiKey", In: "cookie", Name: authn.SessionCookie},
	}
	cfg.Security = []map[string][]string{{"session": {}}}
	return cfg
}
