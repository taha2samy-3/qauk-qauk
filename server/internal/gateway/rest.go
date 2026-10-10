package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/registry"
)

// REST device adapter (/device/v1/...). Same JWT and rules as the WebSocket;
// see docs/04_api_reference/device_rest_api.md.

const (
	restMaxBody  = 1 << 20
	restMaxItems = 500
	// restOrigin marks messages that came over REST (there is no socket to skip).
	restOrigin = "rest"
	// DeviceHeader lets an ingress route all requests of a device to one
	// instance (hash on it), which keeps local rate limits exact.
	DeviceHeader = "X-Quack-Device"
	senmlType    = "application/senml+json"
)

// restItem is one message in POST /device/v1/messages.
type restItem struct {
	Element string          `json:"element"`
	Message json.RawMessage `json:"message"`
	ID      string          `json:"id,omitempty"`
	TS      *time.Time      `json:"ts,omitempty"`
}

type restResult struct {
	Index     int    `json:"index"`
	ElementID string `json:"element_id,omitempty"`
	Status    string `json:"status"`
	EventID   string `json:"event_id,omitempty"`
	Error     string `json:"error,omitempty"`
	Code      string `json:"code,omitempty"`
}

type restProblem struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func restError(w http.ResponseWriter, status int, code, detail string) {
	writeJSON(w, status, restProblem{Status: status, Code: code, Detail: detail})
}

func retryAfterHeader(w http.ResponseWriter, d time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(d.Seconds())))))
}

// restAuth authenticates a REST request and checks the routing header.
func (g *Gateway) restAuth(w http.ResponseWriter, r *http.Request) (*registry.Device, bool) {
	token, ok := authn.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		metrics.WSRejected.WithLabelValues("rest", "no_token").Inc()
		restError(w, http.StatusUnauthorized, "unauthenticated", "missing bearer token")
		return nil, false
	}
	dev, _, err := g.authenticateDevice(r.Context(), token)
	if err != nil {
		metrics.WSRejected.WithLabelValues("rest", "auth").Inc()
		g.log.Info("gateway: rest device rejected", "err", err)
		restError(w, http.StatusUnauthorized, "unauthenticated", "device authentication failed")
		return nil, false
	}
	if h := r.Header.Get(DeviceHeader); h != "" && h != dev.ID.String() {
		metrics.WSRejected.WithLabelValues("rest", "device_header").Inc()
		restError(w, http.StatusForbidden, "device_header_mismatch", DeviceHeader+" must be the id in the token")
		return nil, false
	}
	g.rest.touch(g, dev.ID, "rest", r.RemoteAddr, r.UserAgent())
	return dev, true
}

// RESTMessagesHandler serves POST /device/v1/messages.
func (g *Gateway) RESTMessagesHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dev, ok := g.restAuth(w, r)
		if !ok {
			return
		}
		metrics.MessagesIn.WithLabelValues("rest").Inc()
		body, err := readBody(w, r)
		if err != nil {
			return
		}
		var items []restItem
		if isSenML(r.Header.Get("Content-Type")) {
			items, err = parseSenML(body, time.Now())
		} else {
			items, err = parseItems(body)
		}
		if err != nil {
			restError(w, http.StatusUnprocessableEntity, "invalid_body", err.Error())
			return
		}
		if len(items) == 0 || len(items) > restMaxItems {
			restError(w, http.StatusUnprocessableEntity, "invalid_body", fmt.Sprintf("send 1 to %d messages", restMaxItems))
			return
		}
		if err := g.allowDevice(dev.ID, len(items)); err != nil {
			var rl *RateLimitedError
			errors.As(err, &rl)
			retryAfterHeader(w, rl.RetryAfter)
			restError(w, http.StatusTooManyRequests, "rate_limited", err.Error())
			return
		}
		results, limited, wait := g.publishItems(dev, restOrigin, items)
		if limited == len(items) {
			retryAfterHeader(w, wait)
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"results": results})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	})
}

// publishItems runs a batch through the device core. It returns the results,
// how many were rate limited, and the longest retry-after among them.
func (g *Gateway) publishItems(dev *registry.Device, origin string, items []restItem) ([]restResult, int, time.Duration) {
	results := make([]restResult, len(items))
	limited := 0
	var wait time.Duration
	for i, it := range items {
		res := restResult{Index: i}
		if it.ID != "" && !validClientID(it.ID) {
			res.Status, res.Code, res.Error = "rejected", "invalid_message", "id must be at most 128 characters without '/' or NUL"
			results[i] = res
			continue
		}
		pr, err := g.publishDeviceMessage(dev, origin, DeviceMessage{Element: it.Element, ByName: true,
			Message: it.Message, ClientTS: it.TS, ClientID: it.ID})
		if pr.ElementID != uuid.Nil {
			res.ElementID = pr.ElementID.String()
		}
		res.Status, res.EventID = pr.Status, pr.EventID
		if err != nil {
			var ep *elementpipe.ErrPipeline
			if errors.As(err, &ep) {
				res.Status, res.Code, res.Error = "pipeline_failed", "pipeline_failed", ep.Reason
			} else {
				res.Status, res.Code, res.Error = "rejected", errorCode(err), err.Error()
				var rl *RateLimitedError
				if errors.As(err, &rl) {
					limited++
					wait = max(wait, rl.RetryAfter)
				}
			}
		}
		results[i] = res
	}
	return results, limited, wait
}

func errorCode(err error) string {
	var rl *RateLimitedError
	var us *UnstorableError
	var ep *elementpipe.ErrPipeline
	switch {
	case errors.As(err, &ep):
		return "pipeline_failed"
	case errors.As(err, &rl):
		return "rate_limited"
	case errors.As(err, &us):
		return "unstorable"
	case errors.Is(err, ErrUnknownElement):
		return "unknown_element"
	case errors.Is(err, ErrMessageTooLarge):
		return "too_large"
	default:
		return "invalid_message"
	}
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, restMaxBody))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			restError(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("body is over %d bytes", restMaxBody))
		} else {
			restError(w, http.StatusBadRequest, "bad_request", "could not read the body")
		}
		return nil, err
	}
	return body, nil
}

// parseItems accepts one message object or an array of them.
func parseItems(body []byte) ([]restItem, error) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '{' {
		var it restItem
		if err := json.Unmarshal(body, &it); err != nil {
			return nil, fmt.Errorf("body: %v", err)
		}
		return []restItem{it}, nil
	}
	var items []restItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("body must be a message object or an array of them: %v", err)
	}
	return items, nil
}

func isSenML(ct string) bool {
	return len(ct) >= len(senmlType) && ct[:len(senmlType)] == senmlType
}

// senmlRecord is one SenML (RFC 8428) JSON record.
type senmlRecord struct {
	BaseName  string   `json:"bn"`
	BaseTime  *float64 `json:"bt"`
	BaseUnit  string   `json:"bu"`
	BaseValue *float64 `json:"bv"`
	Name      string   `json:"n"`
	Unit      string   `json:"u"`
	Value     *float64 `json:"v"`
	StrValue  *string  `json:"vs"`
	BoolValue *bool    `json:"vb"`
	DataValue *string  `json:"vd"`
	Sum       *float64 `json:"s"`
	Time      *float64 `json:"t"`
}

// senmlRelative: SenML times below 2^28 are relative to now (RFC 8428 §4.5.3).
const senmlRelative = 1 << 28

// parseSenML maps a SenML pack to messages: the element is bn+n, the message
// is {"value": v|vs|vb|vd|s (+bv), "unit": u|bu if any}, and the time
// (bt+t) becomes the client timestamp.
func parseSenML(body []byte, now time.Time) ([]restItem, error) {
	var recs []senmlRecord
	if err := json.Unmarshal(body, &recs); err != nil {
		return nil, fmt.Errorf("senml: %v", err)
	}
	var bn, bu string
	var bt, bv float64
	items := make([]restItem, 0, len(recs))
	for i, r := range recs {
		if r.BaseName != "" {
			bn = r.BaseName
		}
		if r.BaseTime != nil {
			bt = *r.BaseTime
		}
		if r.BaseUnit != "" {
			bu = r.BaseUnit
		}
		if r.BaseValue != nil {
			bv = *r.BaseValue
		}
		var value any
		switch {
		case r.Value != nil:
			value = *r.Value + bv
		case r.StrValue != nil:
			value = *r.StrValue
		case r.BoolValue != nil:
			value = *r.BoolValue
		case r.DataValue != nil:
			value = *r.DataValue
		case r.Sum != nil:
			value = *r.Sum + bv
		default:
			if r.Name == "" {
				continue // a record with only base fields
			}
			return nil, fmt.Errorf("senml record %d has no value", i)
		}
		name := bn + r.Name
		if name == "" {
			return nil, fmt.Errorf("senml record %d has no name", i)
		}
		msg := map[string]any{"value": value}
		if u := firstNonEmpty(r.Unit, bu); u != "" {
			msg["unit"] = u
		}
		raw, _ := json.Marshal(msg)
		t := bt
		if r.Time != nil {
			t += *r.Time
		}
		var ts time.Time
		switch {
		case t == 0:
			ts = now
		case math.Abs(t) < senmlRelative:
			ts = now.Add(time.Duration(t * float64(time.Second)))
		default:
			sec, frac := math.Modf(t)
			ts = time.Unix(int64(sec), int64(frac*1e9))
		}
		items = append(items, restItem{Element: name, Message: raw, TS: &ts})
	}
	return items, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// --- sync (long-poll) ---

// RESTSyncHandler serves GET /device/v1/sync?cursor=&wait=: the newest value
// per element written by others (users) since the cursor. With wait, it
// holds the request until one arrives or the wait ends.
func (g *Gateway) RESTSyncHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dev, ok := g.restAuth(w, r)
		if !ok {
			return
		}
		cursor, err := decodeCursor(r.URL.Query().Get("cursor"))
		if err != nil {
			restError(w, http.StatusUnprocessableEntity, "invalid_cursor", "cursor is not one this server returned")
			return
		}
		maxWait := g.cfg.SyncMaxWait
		if maxWait <= 0 {
			maxWait = time.Minute
		}
		wait, err := parseWait(r.URL.Query().Get("wait"), maxWait)
		if err != nil {
			restError(w, http.StatusUnprocessableEntity, "invalid_wait", err.Error())
			return
		}
		ids := make([]uuid.UUID, len(dev.Elements))
		for i, e := range dev.Elements {
			ids[i] = e.ID
		}
		var signal <-chan struct{}
		if wait > 0 {
			ch, cancel := g.hub.WaitCommands(dev.ID)
			defer cancel()
			signal = ch
		}
		deadline := time.NewTimer(wait)
		defer deadline.Stop()
		for {
			found := g.hub.CommandsSince(ids, cursor)
			if len(found) > 0 || wait <= 0 {
				writeJSON(w, http.StatusOK, syncResponse(dev, found, cursor))
				return
			}
			select {
			case <-signal:
			case <-deadline.C:
				wait = 0
			case <-r.Context().Done():
				return
			case <-g.ctx.Done():
				wait = 0
			}
		}
	})
}

// syncResponse renders the found commands (as WebSocket device frames, plus
// the element name) and the next cursor.
func syncResponse(dev *registry.Device, found map[uuid.UUID]ringEntry, cursor map[uuid.UUID]uuid.UUID) map[string]any {
	next := make(map[uuid.UUID]uuid.UUID, len(cursor)+len(found))
	for id, ev := range cursor {
		if _, ok := dev.Element(id); ok {
			next[id] = ev
		}
	}
	msgs := make([]json.RawMessage, 0, len(found))
	for _, el := range dev.Elements { // stable order: the device's element order
		e, ok := found[el.ID]
		if !ok {
			continue
		}
		next[el.ID] = e.id
		var frame map[string]json.RawMessage
		if json.Unmarshal(e.frame, &frame) == nil {
			frame["element"], _ = json.Marshal(el.Name)
			frame["event_id"], _ = json.Marshal(e.id.String())
			b, _ := json.Marshal(frame)
			msgs = append(msgs, b)
		}
	}
	return map[string]any{"cursor": encodeCursor(next), "messages": msgs}
}

// The cursor is base64url(JSON {element id: last event id}); opaque to clients.
func encodeCursor(c map[uuid.UUID]uuid.UUID) string {
	if len(c) == 0 {
		return ""
	}
	m := make(map[string]string, len(c))
	for k, v := range c {
		m[k.String()] = v.String()
	}
	b, _ := json.Marshal(m)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(s string) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	if s == "" {
		return out, nil
	}
	if len(s) > 64<<10 {
		return nil, errors.New("cursor too long")
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for k, v := range m {
		ek, err1 := uuid.Parse(k)
		ev, err2 := uuid.Parse(v)
		if err1 != nil || err2 != nil {
			return nil, errors.New("bad cursor entry")
		}
		out[ek] = ev
	}
	return out, nil
}

// parseWait accepts seconds ("30") or a Go duration ("30s"), capped at maxWait.
func parseWait(s string, maxWait time.Duration) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		sec, err2 := strconv.ParseFloat(s, 64)
		if err2 != nil || sec < 0 || math.IsNaN(sec) || math.IsInf(sec, 0) {
			return 0, errors.New("wait must be seconds (30) or a duration (30s)")
		}
		d = time.Duration(sec * float64(time.Second))
	}
	if d < 0 {
		return 0, errors.New("wait must not be negative")
	}
	return min(d, maxWait), nil
}

// --- presence of connectionless devices ---

// restPresence tracks devices active over request/response calls (REST, and
// unary gRPC) or over MQTT on this instance. A device
// counts as connected for QUACK_PRESENCE_TTL after its last request; the
// lease is written once (the gateway heartbeat refreshes it), not per request.
type restPresence struct {
	mu   sync.Mutex
	last map[uuid.UUID]restLease
}

type restLease struct {
	seen  time.Time
	audit uuid.UUID
}

func newRESTPresence() *restPresence { return &restPresence{last: map[uuid.UUID]restLease{}} }

func restConnID(deviceID uuid.UUID) string { return "rest-" + deviceID.String()[:8] }

func (p *restPresence) touch(g *Gateway, deviceID uuid.UUID, transport, client, userAgent string) {
	p.mu.Lock()
	l, ok := p.last[deviceID]
	l.seen = time.Now()
	p.last[deviceID] = l
	p.mu.Unlock()
	if ok {
		return
	}
	info := map[string]any{"client": client, "transport": transport, "user_agent": userAgent, "conn_id": restConnID(deviceID)}
	audit := g.deviceConnected(deviceID, restConnID(deviceID), info)
	p.mu.Lock()
	if cur, ok := p.last[deviceID]; ok {
		cur.audit = audit
		p.last[deviceID] = cur
	}
	p.mu.Unlock()
}

// expired removes and returns devices idle for longer than ttl.
func (p *restPresence) expired(ttl time.Duration) map[uuid.UUID]restLease {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[uuid.UUID]restLease{}
	now := time.Now()
	for id, l := range p.last {
		if now.Sub(l.seen) > ttl {
			out[id] = l
			delete(p.last, id)
		}
	}
	return out
}

func (g *Gateway) restPresenceLoop(ctx context.Context) {
	t := time.NewTicker(max(g.cfg.PresenceHeartbeat, time.Second))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		for id, l := range g.rest.expired(g.cfg.PresenceTTL) {
			g.deviceDisconnected(id, restConnID(id), l.audit)
		}
	}
}
