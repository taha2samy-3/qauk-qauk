package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/ratelimit"
	"github.com/taha2samy/quackquack/server/internal/registry"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// Device core: the transport-agnostic rules every device transport
// (WebSocket, REST, gRPC) goes through. See docs/refactor/DEVICE_ADAPTERS_PLAN.md.

var (
	// ErrInvalidMessage: the message is missing or not JSON.
	ErrInvalidMessage = errors.New("invalid message")
	// ErrUnknownElement: no such element on this device.
	ErrUnknownElement = errors.New("unknown element")
)

// UnstorableError: the message can't be stored (e.g. NUL or a lone surrogate).
type UnstorableError struct{ Err error }

func (e *UnstorableError) Error() string { return "unstorable message: " + e.Err.Error() }
func (e *UnstorableError) Unwrap() error { return e.Err }

// RateLimitedError: over the device guard or the element's limit.
type RateLimitedError struct {
	Scope      string // "device" or "element"
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("%s rate limit exceeded, retry after %s", e.Scope, e.RetryAfter.Round(time.Millisecond))
}

// Publish outcomes.
const (
	StatusAccepted  = "accepted"  // published
	StatusDuplicate = "duplicate" // same client id seen recently; not published again
	StatusCoalesced = "coalesced" // over the limit with over_limit=latest: held, newest wins
)

// DeviceMessage is one message a device sends, on any transport.
type DeviceMessage struct {
	// Element is the element id, or (when ByName) its id or name.
	Element string
	ByName  bool
	Message json.RawMessage
	// ClientTS is the device's own timestamp (optional).
	ClientTS *time.Time
	// ClientID makes retries safe: the same id from the same device within
	// dedupeTTL is acknowledged without publishing again (optional).
	ClientID string
}

type PublishResult struct {
	ElementID uuid.UUID
	EventID   string
	Status    string
}

// authenticateDevice verifies a device JWT against the registry.
func (g *Gateway) authenticateDevice(ctx context.Context, token string) (*registry.Device, *authn.DeviceKey, error) {
	dk, err := g.verifier.Verify(ctx, token)
	if err != nil {
		return nil, nil, err
	}
	dev, ok := g.registry.Lookup(dk.DeviceID)
	if !ok {
		return nil, nil, fmt.Errorf("%w: device %s vanished", authn.ErrDeviceAuth, dk.DeviceID)
	}
	return dev, dk, nil
}

// loadDeviceKey is the verifier's key loader: the registry, which falls
// back to the database on a miss.
func (g *Gateway) loadDeviceKey(ctx context.Context, id uuid.UUID) (*authn.DeviceKey, error) {
	dev, err := g.registry.Get(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("device %s: %w", id, err)
	}
	if dev.Key == nil {
		return nil, fmt.Errorf("device %s has no key", id)
	}
	return &authn.DeviceKey{DeviceID: dev.ID, DeviceName: dev.Name, KeyID: dev.Key.ID, PEM: dev.Key.PEM,
		Algorithm: dev.Key.Algorithm, KeyActive: dev.Key.Active, Public: dev.Key.Public}, nil
}

// registryLoader reads a device snapshot from the database (registry miss).
func registryLoader(pool store.DBTX) registry.Loader {
	return func(ctx context.Context, id uuid.UUID) (events.DeviceConfig, error) {
		cfg, err := store.DeviceConfig(ctx, pool, id)
		if errors.Is(err, store.ErrNotFound) {
			return cfg, registry.ErrNotFound
		}
		return cfg, err
	}
}

// allowDevice applies the per-device guard to n messages.
func (g *Gateway) allowDevice(deviceID uuid.UUID, n int) error {
	ok, wait := g.limits.Allow("d:"+deviceID.String(), ratelimit.Limit{Rate: g.cfg.DeviceMsgRate}, n)
	if !ok {
		metrics.Dropped.WithLabelValues("device_rate_limit").Inc()
		return &RateLimitedError{Scope: "device", RetryAfter: wait}
	}
	return nil
}

func (g *Gateway) elementLimit(el registry.Element) ratelimit.Limit {
	l := ratelimit.Limit{Rate: el.Rate, Burst: el.Burst}
	if l.Rate <= 0 {
		l.Rate = g.cfg.ElementMsgRate
	}
	return l
}

// resolveElement finds the element a message is for.
func resolveElement(dev *registry.Device, ref string, byName bool) (registry.Element, bool) {
	if id, err := uuid.Parse(ref); err == nil {
		if el, ok := dev.Element(id); ok {
			return el, true
		}
	}
	if byName {
		return dev.ElementByName(ref)
	}
	return registry.Element{}, false
}

// publishDeviceMessage runs one device message through the core rules, in
// order: validation, ownership, client-id dedupe, element limit, publish.
// The per-device guard is the caller's (allowDevice), because transports
// apply it at different points (per frame, per batch).
func (g *Gateway) publishDeviceMessage(dev *registry.Device, origin string, in DeviceMessage) (PublishResult, error) {
	if len(in.Message) == 0 || !json.Valid(in.Message) {
		metrics.Dropped.WithLabelValues("invalid").Inc()
		return PublishResult{}, ErrInvalidMessage
	}
	if err := history.ValidateMessage(in.Message); err != nil {
		metrics.Dropped.WithLabelValues("unstorable").Inc()
		return PublishResult{}, &UnstorableError{Err: err}
	}
	el, ok := resolveElement(dev, in.Element, in.ByName)
	if !ok {
		metrics.Dropped.WithLabelValues("foreign_element").Inc()
		return PublishResult{}, ErrUnknownElement
	}
	res := PublishResult{ElementID: el.ID, Status: StatusAccepted}
	var dedupeKey string
	if in.ClientID != "" {
		dedupeKey = dev.ID.String() + "/" + in.ClientID
		if prev, seen := g.dedupe.claim(dedupeKey); seen {
			res.EventID, res.Status = prev, StatusDuplicate
			return res, nil
		}
	}
	m := &events.ElementMessage{
		ElementID: el.ID,
		DeviceID:  dev.ID,
		Source:    events.SourceDevice,
		Actor:     events.Actor{ID: dev.ID.String(), Name: dev.Name},
		Origin:    events.Origin{GatewayID: g.cfg.GatewayID, ConnID: origin},
		Message:   in.Message,
	}
	if in.ClientTS != nil {
		m.ClientTS = &events.Time{Time: in.ClientTS.UTC()}
	}
	latest := el.OverLimit == store.OverLimitLatest
	// While a held message is pending, newer ones replace it (so order is kept).
	if latest && g.coalesce.replace(el.ID, m, origin) {
		metrics.Dropped.WithLabelValues("element_rate_coalesced").Inc()
		res.Status = StatusCoalesced
		g.dedupe.put(dedupeKey, "")
		return res, nil
	}
	ok, wait := g.limits.Allow("e:"+el.ID.String(), g.elementLimit(el), 1)
	if !ok {
		if latest {
			g.coalesce.hold(el, m, origin, wait)
			metrics.Dropped.WithLabelValues("element_rate_coalesced").Inc()
			res.Status = StatusCoalesced
			g.dedupe.put(dedupeKey, "")
			return res, nil
		}
		metrics.Dropped.WithLabelValues("element_rate_limit").Inc()
		g.dedupe.release(dedupeKey) // not published: a retry must go through
		return res, &RateLimitedError{Scope: "element", RetryAfter: wait}
	}
	res.EventID = g.publishElement(m, origin)
	g.dedupe.put(dedupeKey, res.EventID)
	return res, nil
}

// --- over_limit = latest -------------------------------------------------------

// coalescer holds the newest over-limit message per element and publishes
// it as soon as the element's bucket has a token again.
type coalescer struct {
	g  *Gateway
	mu sync.Mutex
	p  map[uuid.UUID]*heldMessage
}

type heldMessage struct {
	el     registry.Element
	m      *events.ElementMessage
	origin string
	timer  *time.Timer
}

func newCoalescer(g *Gateway) *coalescer { return &coalescer{g: g, p: map[uuid.UUID]*heldMessage{}} }

// replace swaps the pending message of an element, if there is one.
func (c *coalescer) replace(id uuid.UUID, m *events.ElementMessage, origin string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.p[id]
	if ok {
		h.m, h.origin = m, origin
	}
	return ok
}

func (c *coalescer) hold(el registry.Element, m *events.ElementMessage, origin string, wait time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.p[el.ID]; ok {
		h.m, h.origin = m, origin
		return
	}
	h := &heldMessage{el: el, m: m, origin: origin}
	c.p[el.ID] = h
	h.timer = time.AfterFunc(max(wait, time.Millisecond), func() { c.flush(el.ID) })
}

func (c *coalescer) flush(id uuid.UUID) {
	c.mu.Lock()
	h, ok := c.p[id]
	if !ok {
		c.mu.Unlock()
		return
	}
	if c.g.ctx.Err() != nil {
		delete(c.p, id)
		c.mu.Unlock()
		return
	}
	el := h.el
	if dev, ok := c.g.registry.Lookup(h.m.DeviceID); ok { // limits may have changed
		if cur, ok := dev.Element(id); ok {
			el = cur
		}
	}
	allowed, wait := c.g.limits.Allow("e:"+id.String(), c.g.elementLimit(el), 1)
	if !allowed {
		h.timer.Reset(max(wait, time.Millisecond))
		c.mu.Unlock()
		return
	}
	delete(c.p, id)
	c.mu.Unlock()
	c.g.publishElement(h.m, h.origin)
}

// pending reports whether an element has a held message (tests).
func (c *coalescer) pending(id uuid.UUID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.p[id]
	return ok
}

// --- client message ids ----------------------------------------------------------

// dedupeTTL is how long a client message id is remembered. With device-sticky
// routing (X-Quack-Device), retries reach the instance that saw the original.
const (
	dedupeTTL = 10 * time.Minute
	dedupeMax = 200_000
)

type dedupeCache struct {
	mu      sync.Mutex
	m       map[string]dedupeEntry
	sweptAt time.Time
}

type dedupeEntry struct {
	eventID string
	exp     time.Time
}

func newDedupeCache() *dedupeCache { return &dedupeCache{m: map[string]dedupeEntry{}} }

// claim reports a live entry for key, or atomically reserves it, so two
// concurrent retries can't both publish.
func (d *dedupeCache) claim(key string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	if e, ok := d.m[key]; ok && now.Before(e.exp) {
		return e.eventID, true
	}
	d.setLocked(key, "", now)
	return "", false
}

func (d *dedupeCache) release(key string) {
	if key == "" {
		return
	}
	d.mu.Lock()
	delete(d.m, key)
	d.mu.Unlock()
}

func (d *dedupeCache) put(key, eventID string) {
	if key == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.setLocked(key, eventID, time.Now())
}

func (d *dedupeCache) setLocked(key, eventID string, now time.Time) {
	if len(d.m) >= dedupeMax || now.Sub(d.sweptAt) > time.Minute {
		d.sweptAt = now
		for k, e := range d.m {
			if now.After(e.exp) {
				delete(d.m, k)
			}
		}
		if len(d.m) >= dedupeMax { // still full: forget the lot rather than grow
			clear(d.m)
		}
	}
	d.m[key] = dedupeEntry{eventID: eventID, exp: now.Add(dedupeTTL)}
}

// validClientID bounds client message ids.
func validClientID(id string) bool {
	return len(id) <= 128 && !strings.ContainsAny(id, "\x00/")
}
