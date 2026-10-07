package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/taha2samy/quackquack/server/internal/authn"

	"github.com/taha2samy/quackquack/server/internal/store"
)

type MeBody struct {
	store.User
	Groups []store.Group `json:"groups"`
}

type LoginInput struct {
	Body struct {
		Username string `json:"username" minLength:"1" maxLength:"150"`
		Password string `json:"password" minLength:"1" maxLength:"1024"`
	}
}

type LoginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      MeBody
}

type LogoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type MeOutput struct{ Body MeBody }

type PasswordInput struct {
	Body struct {
		OldPassword string `json:"old_password" minLength:"1"`
		NewPassword string `json:"new_password" minLength:"8" maxLength:"1024"`
	}
}

var noSecurity = []map[string][]string{}

func (a *API) registerAuth(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "login", Method: http.MethodPost, Path: "/api/v1/auth/login", Tags: []string{"auth"},
		Summary: "Log in with username and password; sets the session cookie", Security: noSecurity,
	}, a.login)

	huma.Register(api, huma.Operation{
		OperationID: "logout", Method: http.MethodPost, Path: "/api/v1/auth/logout", Tags: []string{"auth"},
		Summary: "End the current session", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, _ *struct{}) (*LogoutOutput, error) {
		if hash, ok := ctx.Value(sessionKey).([]byte); ok {
			_ = store.DeleteSession(ctx, a.pool, hash)
		}
		return &LogoutOutput{SetCookie: a.sessionCookie("", time.Unix(0, 0))}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "me", Method: http.MethodGet, Path: "/api/v1/auth/me", Tags: []string{"auth"},
		Summary: "The logged-in user",
	}, func(ctx context.Context, _ *struct{}) (*MeOutput, error) {
		u, err := currentUser(ctx)
		if err != nil {
			return nil, err
		}
		body, err := a.meBody(ctx, u)
		return &MeOutput{Body: body}, err
	})

	huma.Register(api, huma.Operation{
		OperationID: "change-password", Method: http.MethodPost, Path: "/api/v1/auth/password", Tags: []string{"auth"},
		Summary: "Change your password (ends all your sessions)", DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *PasswordInput) (*struct{}, error) {
		u, err := currentUser(ctx)
		if err != nil {
			return nil, err
		}
		if err := a.svc.ChangeOwnPassword(ctx, u, in.Body.OldPassword, in.Body.NewPassword); err != nil {
			return nil, a.fail(err)
		}
		return nil, nil
	})
}

func (a *API) login(ctx context.Context, in *LoginInput) (*LoginOutput, error) {
	r := requestFrom(ctx)
	ip := store.ClientIP(r.RemoteAddr)
	if !a.limiter.allow(ip + "|" + in.Body.Username) {
		return nil, huma.Error429TooManyRequests("too many login attempts, try again later")
	}
	invalid := huma.Error401Unauthorized("invalid username or password")
	u, err := store.GetUserByUsername(ctx, a.pool, in.Body.Username)
	if err != nil {
		authn.DummyVerify(in.Body.Password)
		return nil, invalid
	}
	ok, rehash, err := authn.VerifyPassword(u.PasswordHash, in.Body.Password)
	if err != nil || !ok || !u.IsActive {
		return nil, invalid
	}
	if rehash {
		if h, err := authn.HashPassword(in.Body.Password); err == nil {
			_ = store.SetPasswordHash(ctx, a.pool, u.ID, h)
		}
	}
	token, hash, err := authn.NewSessionToken()
	if err != nil {
		return nil, a.fail(err)
	}
	expires := time.Now().Add(a.cfg.SessionTTL)
	if err := store.InsertSession(ctx, a.pool, hash, u.ID, expires, ip, r.UserAgent()); err != nil {
		return nil, a.fail(err)
	}
	_ = store.TouchLogin(ctx, a.pool, u.ID)
	a.limiter.reset(ip + "|" + in.Body.Username)
	body, err := a.meBody(ctx, u)
	if err != nil {
		return nil, err
	}
	return &LoginOutput{SetCookie: a.sessionCookie(token, expires), Body: body}, nil
}

func (a *API) meBody(ctx context.Context, u store.User) (MeBody, error) {
	groups, err := store.ListUserGroups(ctx, a.pool, u.ID)
	if err != nil {
		return MeBody{}, a.fail(err)
	}
	return MeBody{User: u, Groups: groups}, nil
}

func (a *API) sessionCookie(value string, expires time.Time) http.Cookie {
	c := http.Cookie{
		Name: authn.SessionCookie, Value: value, Path: "/", Expires: expires,
		HttpOnly: true, Secure: a.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	}
	if value == "" {
		c.MaxAge = -1
	}
	return c
}

// loginLimiter is a fixed-window attempt counter per ip|username.
type loginLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*window
}

type window struct {
	start time.Time
	n     int
}

func newLoginLimiter(max int, w time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: w, hits: map[string]*window{}}
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.hits) > 100_000 {
		for k, w := range l.hits {
			if now.Sub(w.start) > l.window {
				delete(l.hits, k)
			}
		}
	}
	w := l.hits[key]
	if w == nil || now.Sub(w.start) > l.window {
		w = &window{start: now}
		l.hits[key] = w
	}
	w.n++
	return w.n <= l.max
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.hits, key)
	l.mu.Unlock()
}
