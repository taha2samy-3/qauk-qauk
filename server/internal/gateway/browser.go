package gateway

import (
	"context"
	"encoding/json"
	"errors"

	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/store"
)

const (
	msgPermissionDenied = "You do not have permission to access this element."
	msgUnauthorized     = "You do not have permission to send messages to this element."
)

// BrowserHandler serves /browser/simple/ for logged-in users (session cookie).
func (g *Gateway) BrowserHandler() http.Handler {
	return http.HandlerFunc(g.handleBrowser)
}

func (g *Gateway) handleBrowser(w http.ResponseWriter, r *http.Request) {
	if !g.origins.Allowed(r) {
		metrics.WSRejected.WithLabelValues("browser", "origin").Inc()
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	cookie, err := r.Cookie(authn.SessionCookie)
	if err != nil {
		metrics.WSRejected.WithLabelValues("browser", "no_session").Inc()
		http.Error(w, "authentication required", http.StatusForbidden)
		return
	}
	hash := authn.HashToken(cookie.Value)
	user, err := store.SessionUser(r.Context(), g.pool, hash)
	if err != nil {
		metrics.WSRejected.WithLabelValues("browser", "bad_session").Inc()
		http.Error(w, "authentication required", http.StatusForbidden)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	b := &browserClient{
		client: newClient(g.ctx, ws, g.nextConnID(), "browser"),
		userID: user.ID, username: user.Username, sessionHash: hash,
		subs:    map[uuid.UUID]string{},
		limiter: limiter{rate: g.cfg.BrowserMsgRate},
	}
	g.hub.AddBrowser(b)
	metrics.WSConnections.WithLabelValues("browser").Inc()

	go b.writeLoop()
	go b.pingLoop()
	for {
		typ, data, err := ws.Read(b.ctx)
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			continue
		}
		metrics.MessagesIn.WithLabelValues("browser").Inc()
		if !b.allowMessage(time.Now()) {
			metrics.Dropped.WithLabelValues("browser_rate_limit").Inc()
			b.Send(errorFrame("rate_limited", "Too many messages; slow down.", ""))
			continue
		}
		g.onBrowserFrame(b, data)
	}
	b.cancel()
	g.hub.RemoveBrowser(b)
	metrics.WSConnections.WithLabelValues("browser").Dec()
}

type browserFrameIn struct {
	Type      *string         `json:"type"`
	ElementID *string         `json:"element_id"`
	Message   json.RawMessage `json:"message"`
}

func (g *Gateway) onBrowserFrame(b *browserClient, data []byte) {
	var f browserFrameIn
	if err := json.Unmarshal(data, &f); err != nil {
		b.Send(errorFrame("invalid_format", "invalid JSON message", ""))
		return
	}
	typ := "None"
	if f.Type != nil {
		typ = *f.Type
	}
	switch typ {
	case "subscribe", "unsubscribe", "message_element":
	default:
		b.Send(errorFrame("unknown_type", "Unknown message type: "+typ, ""))
		return
	}
	if f.ElementID == nil {
		b.Send(errorFrame("invalid_format", "'element_id'", ""))
		return
	}
	raw := *f.ElementID
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer cancel()
	switch typ {
	case "subscribe":
		g.subscribe(ctx, b, raw)
	case "unsubscribe":
		if id, err := uuid.Parse(raw); err == nil {
			g.hub.Unsubscribe(b, id)
			b.DropPerm(id)
		}
		b.Send(unsubscribeFrame(raw, ""))
	case "message_element":
		g.browserPublish(b, raw, f.Message)
	}
}

func (g *Gateway) subscribe(ctx context.Context, b *browserClient, raw string) {
	id, err := uuid.Parse(raw)
	if err != nil {
		b.Send(errorFrame("permission_denied", msgPermissionDenied, raw))
		return
	}
	el, err := store.GetElement(ctx, g.pool, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			g.log.Error("gateway: load element", "element", id, "err", err)
		}
		b.Send(errorFrame("permission_denied", msgPermissionDenied, raw))
		return
	}
	perm, err := store.MaxPermission(ctx, g.pool, b.userID, id)
	if err != nil || perm == "" {
		b.Send(errorFrame("permission_denied", msgPermissionDenied, raw))
		return
	}
	connected, err := store.DeviceConnected(ctx, g.pool, el.DeviceID, g.cfg.PresenceTTL)
	if err != nil {
		g.log.Warn("gateway: presence lookup", "err", err)
	}
	info := elementInfo{ID: el.ID, DeviceID: el.DeviceID, Points: el.Points}
	var stored []ringEntry
	loaded := false
	if g.hub.NeedsHistory(info) {
		stored, loaded = g.loadHistory(ctx, info)
	}
	b.SetPerm(id, perm)
	g.hub.Subscribe(b, info, subscribeFrame(id.String(), perm, el.Details, connected), stored, loaded)
}

func (g *Gateway) browserPublish(b *browserClient, raw string, message json.RawMessage) {
	id, err := uuid.Parse(raw)
	perm := ""
	if err == nil {
		perm, _ = b.Perm(id)
	}
	if perm != store.PermReadWrite {
		b.Send(errorFrame("unauthorized", msgUnauthorized, raw))
		return
	}
	if message == nil {
		b.Send(errorFrame("invalid_format", "'message'", raw))
		return
	}
	if err := history.ValidateMessage(message); err != nil {
		metrics.Dropped.WithLabelValues("unstorable").Inc()
		b.Send(errorFrame("invalid_format", "'message' "+err.Error(), raw))
		return
	}
	st := g.hub.get(id)
	if st == nil {
		return
	}
	g.publishElement(&events.ElementMessage{
		ElementID: id,
		DeviceID:  st.deviceID,
		Source:    events.SourceUser,
		Actor:     events.Actor{ID: strconv.FormatInt(b.userID, 10), Name: b.username},
		Origin:    events.Origin{GatewayID: g.cfg.GatewayID, ConnID: b.id},
		Message:   message,
	}, b.id)
}

// reevaluate re-checks one subscription after a permission change (B6/B7/B8).
func (g *Gateway) reevaluate(ctx context.Context, b *browserClient, elementID uuid.UUID) {
	old, ok := b.Perm(elementID)
	if !ok {
		return
	}
	perm, err := store.MaxPermission(ctx, g.pool, b.userID, elementID)
	if err != nil {
		g.log.Warn("gateway: permission re-evaluation", "err", err)
		return
	}
	switch {
	case perm == "":
		g.hub.Unsubscribe(b, elementID)
		b.DropPerm(elementID)
		b.Send(unsubscribeFrame(elementID.String(), "Permission revoked"))
	case perm != old:
		b.SetPerm(elementID, perm)
		b.Send(permissionsFrame(elementID.String(), perm))
	}
}
