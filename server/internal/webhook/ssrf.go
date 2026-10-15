package webhook

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Requests go only to public internet addresses: an admin must not be able to
// make the server call (and read back) its own network, the cloud metadata
// service or other internal hosts. The address is checked after DNS
// resolution, on the connection itself, so a name can't resolve to a public
// address at check time and a private one at connect time (DNS rebinding).
// Redirects are not followed. allowPrivate (QUACK_WEBHOOK_ALLOW_PRIVATE, for
// development) lifts both rules and allows plain http.

// RequestTimeout bounds one webhook request.
const RequestTimeout = 10 * time.Second

// MaxResponseBytes is how much of a response body is read.
const MaxResponseBytes = 2 << 10

var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
		"192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
		"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "64:ff9b::/96", "100::/64", "2001:db8::/32", "fc00::/7", "fe80::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// publicAddr reports whether ip is a public unicast address.
func publicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsMulticast() ||
		ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// NewHTTPClient returns the client webhooks are sent with.
func NewHTTPClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil || !publicAddr(ip) {
				return fmt.Errorf("%s is not a public address", host)
			}
			return nil
		}
	}
	transport := &http.Transport{
		Proxy:                 nil, // a proxy would bypass the address check
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: RequestTimeout,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Timeout:   RequestTimeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse // a 3xx is the endpoint's answer, not a new target
		},
	}
}

// ValidateURL checks a webhook URL when it is saved. The address check
// happens again on every connection.
func ValidateURL(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return errors.New("must be an absolute http(s) URL without credentials")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !allowPrivate {
			return errors.New("must use https (http is allowed only with QUACK_WEBHOOK_ALLOW_PRIVATE)")
		}
	default:
		return errors.New("must use https")
	}
	if allowPrivate {
		return nil
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil {
		if !publicAddr(ip) {
			return fmt.Errorf("%s is not a public address", host)
		}
		return nil
	}
	if h := strings.ToLower(host); h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") ||
		strings.HasSuffix(h, ".internal") || !strings.Contains(h, ".") {
		return fmt.Errorf("%s is not a public host name", host)
	}
	return nil
}

// cleanError describes a failed request without the URL (which can hold a
// token).
func cleanError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
}

// RedactURL shows where a URL points without the parts that may be secret
// (path and query often carry tokens): scheme://host/…/last 4 characters.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "…"
	}
	tail := strings.TrimRight(u.Path, "/")
	if len(tail) > 4 {
		tail = tail[len(tail)-4:]
	}
	if tail == "" && u.RawQuery == "" {
		return u.Scheme + "://" + u.Host
	}
	return u.Scheme + "://" + u.Host + "/…" + tail
}
