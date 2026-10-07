package gateway

import (
	"net/http"
	"net/url"
	"strings"
)

// OriginPolicy guards cookie-authenticated browser sockets against
// Cross-Site WebSocket Hijacking (B4).
type OriginPolicy struct {
	allowed map[string]struct{}
}

func NewOriginPolicy(origins []string) *OriginPolicy {
	p := &OriginPolicy{allowed: map[string]struct{}{}}
	for _, o := range origins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			p.allowed[strings.ToLower(o)] = struct{}{}
		}
	}
	return p
}

// Allowed accepts a missing Origin (non-browser clients cannot be hijacked
// through a victim's browser), a same-host Origin, or an allow-listed Origin.
func (p *OriginPolicy) Allowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	_, ok := p.allowed[strings.ToLower(u.Scheme+"://"+u.Host)]
	return ok
}
