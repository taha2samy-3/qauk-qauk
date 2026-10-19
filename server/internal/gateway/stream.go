package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// The integration stream (/api/v1/stream): live values, presence, alerts
// and commands for API tokens (and sessions), in its own versioned protocol.
// A stream is a browserClient with a streamState, so the hub, replay,
// presence, permission re-checks, drain and shutdown treat it like a
// dashboard socket; render turns the hub's frames into stream frames.
// docs/04_api_reference/integration_api.md#stream

const (
	streamProtocol  = 1
	streamAuthWait  = 10 * time.Second
	streamRecheck   = time.Minute // a token's expiry and revocation, at the latest
	closeAuthFailed = websocket.StatusCode(4001)
)

// streamState is what a stream adds to a dashboard socket.
type streamState struct {
	token     *store.TokenPrincipal // nil for a session
	tokenHash []byte

	mu      sync.Mutex
	all     bool                 // follows every element it may read, new ones too
	alerts  bool                 // wants alert events
	visible map[uuid.UUID]bool   // elements it may read (alerts, "all")
	since   *time.Time           // replay bound while a subscribe runs
	cmds    map[uuid.UUID]string // command id -> the client's request id, for status updates
}

// maxStreamCommands bounds the request ids a stream remembers.
const maxStreamCommands = 1024

func (s *streamState) rememberCommand(id uuid.UUID, req string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmds == nil {
		s.cmds = map[uuid.UUID]string{}
	}
	if len(s.cmds) >= maxStreamCommands {
		for k := range s.cmds { // drop any one: status updates come within seconds
			delete(s.cmds, k)
			break
		}
	}
	s.cmds[id] = req
}

func (s *streamState) requestOf(id uuid.UUID) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmds[id]
}

func (s *streamState) has(scope string) bool { return s.token == nil || s.token.Has(scope) }

func (s *streamState) scopes() []string {
	if s.token == nil {
		return []string{store.ScopeRead, store.ScopeControl, store.ScopeAlerts}
	}
	return s.token.Scopes
}

// --- frames ---

type streamValueFrame struct {
	Type      string          `json:"type"`
	ElementID string          `json:"element_id"`
	Time      string          `json:"time"`
	Source    string          `json:"source"` // device or user
	Actor     string          `json:"actor"`
	Message   json.RawMessage `json:"message"`
	Replay    bool            `json:"replay,omitempty"`
}

// render turns a hub frame (browser protocol) into a stream frame; nil drops it.
func (s *streamState) render(frame []byte, replay bool) []byte {
	var f struct {
		Type        string          `json:"type"`
		ElementID   string          `json:"element_id"`
		Message     json.RawMessage `json:"message"`
		Auth        *authJSON       `json:"auth"`
		LastEditAt  string          `json:"last_edit_at"`
		Status      string          `json:"status"`
		Reason      string          `json:"reason"`
		Permissions string          `json:"permissions"`
		ErrorCode   string          `json:"error_code"`
		Description string          `json:"description"`
	}
	if json.Unmarshal(frame, &f) != nil {
		return nil
	}
	switch f.Type {
	case "message_element":
		v := streamValueFrame{Type: "value", ElementID: f.ElementID, Time: f.LastEditAt, Source: events.SourceDevice, Message: f.Message, Replay: replay}
		if f.Auth != nil {
			v.Actor = f.Auth.Username
			if len(f.Auth.UserID) > 0 && f.Auth.UserID[0] != '"' { // a number: a user
				v.Source = events.SourceUser
			}
		}
		return mustJSON(v)
	case "element_connection_status":
		return mustJSON(struct {
			Type      string `json:"type"`
			ElementID string `json:"element_id"`
			Connected bool   `json:"connected"`
		}{"presence", f.ElementID, f.Status == "connected"})
	case "unsubscribe":
		return mustJSON(struct {
			Type     string   `json:"type"`
			Elements []string `json:"elements"`
			Reason   string   `json:"reason,omitempty"`
		}{"unsubscribed", []string{f.ElementID}, f.Reason})
	case "permissions_update":
		return mustJSON(struct {
			Type       string `json:"type"`
			ElementID  string `json:"element_id"`
			Permission string `json:"permission"`
		}{"permission", f.ElementID, f.Permissions})
	case "error":
		return streamError("", f.ErrorCode, f.Description, f.ElementID)
	}
	return nil // "subscribe": the stream sends one "subscribed" per request
}

// replay renders the values a subscribe replays: those after since, or
// else the newest one only.
func (s *streamState) replay(entries []ringEntry) [][]byte {
	s.mu.Lock()
	since := s.since
	s.mu.Unlock()
	if since == nil && len(entries) > 1 {
		entries = entries[len(entries)-1:]
	}
	var out [][]byte
	for _, e := range entries {
		if since != nil && !e.at.After(*since) {
			continue
		}
		if f := s.render(e.frame, true); f != nil {
			out = append(out, f)
		}
	}
	return out
}

func streamError(id, code, description, elementID string) []byte {
	return mustJSON(struct {
		Type        string `json:"type"`
		ID          string `json:"id,omitempty"`
		Code        string `json:"code"`
		Description string `json:"description"`
		ElementID   string `json:"element_id,omitempty"`
	}{"error", id, code, description, elementID})
}

// --- connection ---

// StreamHandler serves /api/v1/stream.
func (g *Gateway) StreamHandler() http.Handler { return http.HandlerFunc(g.handleStream) }

type streamPrincipal struct {
	user      store.User
	token     *store.TokenPrincipal
	tokenHash []byte
	session   []byte
}

// streamAuth authenticates the upgrade: a bearer API token, or a session
// cookie from an allowed origin. ok false with no error: authenticate with
// the first frame instead (browsers can't set headers on WebSockets).
func (g *Gateway) streamAuth(r *http.Request) (p streamPrincipal, ok bool, err error) {
	if tok, has := authn.BearerToken(r.Header.Get("Authorization")); has {
		return g.tokenPrincipal(r.Context(), tok)
	}
	if c, err := r.Cookie(authn.SessionCookie); err == nil && c.Value != "" {
		if !g.origins.Allowed(r) {
			return p, false, errors.New("origin not allowed")
		}
		hash := authn.HashToken(c.Value)
		u, err := store.SessionUser(r.Context(), g.pool, hash)
		if err != nil {
			return p, false, errors.New("authentication required")
		}
		return streamPrincipal{user: u, session: hash}, true, nil
	}
	return p, false, nil
}

func (g *Gateway) tokenPrincipal(ctx context.Context, tok string) (streamPrincipal, bool, error) {
	hash := authn.HashToken(tok)
	tp, err := store.TokenUser(ctx, g.pool, hash)
	if err != nil {
		return streamPrincipal{}, false, errors.New("invalid, expired or revoked API token")
	}
	return streamPrincipal{user: tp.User, token: &tp, tokenHash: hash}, true, nil
}

func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request) {
	if g.draining.Load() {
		metrics.WSRejected.WithLabelValues("stream", "draining").Inc()
		http.Error(w, "server draining", http.StatusServiceUnavailable)
		return
	}
	p, authed, err := g.streamAuth(r)
	if err != nil {
		metrics.WSRejected.WithLabelValues("stream", "auth").Inc()
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	ws.SetReadLimit(readLimit)
	if !authed {
		// the first frame: {"type": "auth", "token": "qk_…"}
		actx, cancel := context.WithTimeout(g.ctx, streamAuthWait)
		_, data, err := ws.Read(actx)
		cancel()
		var f struct {
			Type  string `json:"type"`
			Token string `json:"token"`
		}
		if err == nil && json.Unmarshal(data, &f) == nil && f.Type == "auth" {
			p, authed, _ = g.tokenPrincipal(g.ctx, f.Token)
		}
		if !authed {
			metrics.WSRejected.WithLabelValues("stream", "auth").Inc()
			_ = ws.Close(closeAuthFailed, "authentication required")
			return
		}
	}

	name := p.user.Username
	if p.token != nil {
		name += " (token " + p.token.TokenName + ")"
	}
	b := &browserClient{
		client: newClient(g.ctx, ws, g.nextConnID(), "stream"),
		userID: p.user.ID, username: name, sessionHash: p.session,
		subs:    map[uuid.UUID]string{},
		limiter: limiter{rate: g.cfg.BrowserMsgRate},
		stream:  &streamState{token: p.token, tokenHash: p.tokenHash},
	}
	g.hub.AddBrowser(b)
	metrics.WSConnections.WithLabelValues("stream").Inc()
	go b.writeLoop()
	go b.pingLoop()
	if p.token != nil {
		go g.streamRecheckLoop(b)
	}
	b.client.Send(mustJSON(struct {
		Type     string   `json:"type"`
		Protocol int      `json:"protocol"`
		User     string   `json:"user"`
		Scopes   []string `json:"scopes"`
	}{"welcome", streamProtocol, p.user.Username, b.stream.scopes()}))

	for {
		typ, data, err := ws.Read(b.ctx)
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			continue
		}
		metrics.MessagesIn.WithLabelValues("stream").Inc()
		if !b.allowMessage(time.Now()) {
			metrics.Dropped.WithLabelValues("stream_rate_limit").Inc()
			b.client.Send(streamError("", "rate_limited", "Too many frames; slow down.", ""))
			continue
		}
		g.onStreamFrame(b, data)
	}
	b.cancel()
	g.hub.RemoveBrowser(b)
	metrics.WSConnections.WithLabelValues("stream").Dec()
}

// streamRecheckLoop closes a token's stream once the token expires or is
// revoked (revocations also arrive at once as control events).
func (g *Gateway) streamRecheckLoop(b *browserClient) {
	t := time.NewTicker(streamRecheck)
	defer t.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
			g.recheckStreamToken(ctx, b)
			cancel()
		}
	}
}

// recheckStreamToken closes the stream if its token no longer works. It
// reports whether the stream is still open.
func (g *Gateway) recheckStreamToken(ctx context.Context, b *browserClient) bool {
	tp, err := store.TokenUser(ctx, g.pool, b.stream.tokenHash)
	switch {
	case err == nil && tp.ID == b.userID:
		return true
	case err != nil && !errors.Is(err, store.ErrNotFound):
		g.log.Warn("gateway: stream token check", "err", err)
		return true // a database blip doesn't close streams
	}
	b.kill(closeRevoked, "token revoked or expired")
	return false
}

// --- frames from the client ---

type streamFrameIn struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Elements  []uuid.UUID     `json:"elements"`
	Devices   []uuid.UUID     `json:"devices"`
	All       bool            `json:"all"`
	Alerts    bool            `json:"alerts"`
	Since     *time.Time      `json:"since"`
	ElementID string          `json:"element_id"`
	Message   json.RawMessage `json:"message"`
}

func (g *Gateway) onStreamFrame(b *browserClient, data []byte) {
	var f streamFrameIn
	if err := json.Unmarshal(data, &f); err != nil {
		b.client.Send(streamError("", "invalid_format", "invalid JSON frame: "+err.Error(), ""))
		return
	}
	ctx, cancel := context.WithTimeout(b.ctx, 10*time.Second)
	defer cancel()
	switch f.Type {
	case "subscribe":
		g.streamSubscribe(ctx, b, f)
	case "unsubscribe":
		g.streamUnsubscribe(b, f)
	case "command":
		g.streamCommand(ctx, b, f)
	default:
		b.client.Send(streamError(f.ID, "unknown_type", "unknown frame type "+strconv.Quote(f.Type), ""))
	}
}

type streamElement struct {
	ID         uuid.UUID `json:"id"`
	Name       string    `json:"name"`
	DeviceID   uuid.UUID `json:"device_id"`
	DeviceName string    `json:"device_name"`
	Permission string    `json:"permission"`
	Connected  bool      `json:"connected"`
}

func (g *Gateway) streamSubscribe(ctx context.Context, b *browserClient, f streamFrameIn) {
	s := b.stream
	wantsValues := f.All || len(f.Elements) > 0 || len(f.Devices) > 0
	switch {
	case !wantsValues && !f.Alerts:
		b.client.Send(streamError(f.ID, "invalid_format", "subscribe to elements, devices, all, or alerts", ""))
		return
	case wantsValues && !s.has(store.ScopeRead):
		b.client.Send(streamError(f.ID, "forbidden", "the API token lacks the read scope", ""))
		return
	case f.Alerts && !s.has(store.ScopeAlerts):
		b.client.Send(streamError(f.ID, "forbidden", "the API token lacks the alerts scope", ""))
		return
	}
	visible, err := store.ListVisibleElements(ctx, g.pool, b.userID)
	if err != nil {
		g.log.Error("gateway: stream subscribe", "err", err)
		b.client.Send(streamError(f.ID, "internal", "try again", ""))
		return
	}
	set := make(map[uuid.UUID]bool, len(visible))
	for _, e := range visible {
		set[e.ID] = true
	}
	var chosen []store.VisibleElement
	for _, e := range visible {
		if f.All || slices.Contains(f.Elements, e.ID) || slices.Contains(f.Devices, e.DeviceID) {
			chosen = append(chosen, e)
		}
	}
	var denied []uuid.UUID // asked for, but not readable (or not there)
	for _, id := range f.Elements {
		if !set[id] {
			denied = append(denied, id)
		}
	}
	s.mu.Lock()
	s.visible = set
	s.all = s.all || f.All
	s.alerts = s.alerts || f.Alerts
	s.mu.Unlock()

	connected := map[uuid.UUID]bool{}
	out := make([]streamElement, 0, len(chosen))
	for _, e := range chosen {
		c, seen := connected[e.DeviceID]
		if !seen {
			c, _ = store.DeviceConnected(ctx, g.pool, e.DeviceID, g.cfg.PresenceTTL)
			connected[e.DeviceID] = c
		}
		out = append(out, streamElement{ID: e.ID, Name: e.Name, DeviceID: e.DeviceID, DeviceName: e.DeviceName, Permission: e.Permission, Connected: c})
	}
	b.client.Send(mustJSON(struct {
		Type     string          `json:"type"`
		ID       string          `json:"id,omitempty"`
		Elements []streamElement `json:"elements"`
		Denied   []uuid.UUID     `json:"denied,omitempty"`
		Alerts   bool            `json:"alerts"`
	}{"subscribed", f.ID, out, denied, s.alerts}))
	s.mu.Lock()
	s.since = f.Since
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.since = nil; s.mu.Unlock() }()
	for _, e := range chosen {
		g.streamAttach(ctx, b, e)
	}
}

// streamAttach subscribes a stream to one element it may read, replaying
// per its since.
func (g *Gateway) streamAttach(ctx context.Context, b *browserClient, e store.VisibleElement) {
	if _, ok := b.Perm(e.ID); ok {
		b.SetPerm(e.ID, e.Permission)
		return // already subscribed
	}
	info := elementInfo{ID: e.ID, DeviceID: e.DeviceID, Points: e.Points}
	var stored []ringEntry
	loaded := false
	if g.hub.NeedsHistory(info) {
		stored, loaded = g.loadHistory(ctx, info)
	}
	b.SetPerm(e.ID, e.Permission)
	g.hub.Subscribe(b, info, nil, stored, loaded)
}

func (g *Gateway) streamUnsubscribe(b *browserClient, f streamFrameIn) {
	s := b.stream
	ids := f.Elements
	if f.All {
		ids = b.Subscriptions()
	}
	for _, id := range b.Subscriptions() {
		if !f.All && slices.Contains(f.Devices, g.deviceOf(id)) {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		g.hub.Unsubscribe(b, id)
		b.DropPerm(id)
	}
	s.mu.Lock()
	if f.All {
		s.all = false
	}
	if f.Alerts {
		s.alerts = false
	}
	s.mu.Unlock()
	if ids == nil {
		ids = []uuid.UUID{}
	}
	b.client.Send(mustJSON(struct {
		Type     string      `json:"type"`
		ID       string      `json:"id,omitempty"`
		Elements []uuid.UUID `json:"elements"`
		Alerts   bool        `json:"alerts"`
	}{"unsubscribed", f.ID, ids, s.alerts}))
}

func (g *Gateway) deviceOf(elementID uuid.UUID) uuid.UUID {
	if st := g.hub.get(elementID); st != nil {
		return st.deviceID
	}
	return uuid.Nil
}

// streamCommand sends a command like a dashboard does, and answers it.
func (g *Gateway) streamCommand(ctx context.Context, b *browserClient, f streamFrameIn) {
	fail := func(code, desc string) { b.client.Send(streamError(f.ID, code, desc, f.ElementID)) }
	if !b.stream.has(store.ScopeControl) {
		fail("forbidden", "the API token lacks the control scope")
		return
	}
	id, err := uuid.Parse(f.ElementID)
	if err != nil {
		fail("not_found", "no such element")
		return
	}
	perm, ok := b.Perm(id)
	if !ok {
		if perm, err = store.MaxPermission(ctx, g.pool, b.userID, id); err != nil {
			fail("internal", "try again")
			return
		}
	}
	switch perm {
	case "":
		fail("not_found", "no such element")
		return
	case store.PermReadWrite:
	default:
		fail("forbidden", "sending commands needs the RC permission on the element")
		return
	}
	if len(f.Message) == 0 || string(f.Message) == "null" {
		fail("invalid_format", "'message' is required")
		return
	}
	if err := history.ValidateMessage(f.Message); err != nil {
		fail("invalid_format", "'message' "+err.Error())
		return
	}
	deviceID := g.deviceOf(id)
	if deviceID == uuid.Nil {
		el, err := store.GetElement(ctx, g.pool, id)
		if err != nil {
			fail("not_found", "no such element")
			return
		}
		deviceID = el.DeviceID
	}
	if pc := g.pipelineOf(deviceID, id); pc.pipe.Invertible() {
		if _, err := inverseMessage(f.Message, pc.pipe, elementpipe.Context{Element: pc.element, Device: pc.device, Time: time.Now().UTC()}); err != nil {
			metrics.PipelineFailed.WithLabelValues("inverse").Inc()
			fail("pipeline_failed", "the command can't be converted for the device: "+pipelineReason(err))
			return
		}
	}
	m := &events.ElementMessage{
		ElementID: id, DeviceID: deviceID, Source: events.SourceUser,
		Actor:   events.Actor{ID: strconv.FormatInt(b.userID, 10), Name: b.username},
		Origin:  events.Origin{GatewayID: g.cfg.GatewayID, ConnID: b.id},
		Message: f.Message, Track: true,
	}
	ev, err := events.New(events.TypeElementMessage, events.GatewaySource(g.cfg.GatewayID), id.String(), m)
	if err != nil {
		fail("internal", "try again")
		return
	}
	eventID := ev.ID
	cmdID := uuid.MustParse(eventID)
	uid := b.userID
	// recorded before it is published, so no delivery report can come first
	if err := store.InsertCommand(ctx, g.pool, store.Command{ID: cmdID, ElementID: id, UserID: &uid, Actor: b.username, Message: f.Message}); err != nil {
		g.log.Warn("gateway: record stream command", "err", err)
		fail("internal", "try again")
		return
	}
	b.stream.rememberCommand(cmdID, f.ID)
	g.publishElementEvent(ev, m, b.id, nil, 0)
	if b.stream.token != nil { // what tokens do is audited
		data, _ := json.Marshal(map[string]any{"command_id": eventID, "message": f.Message, "via": "stream"})
		if err := store.InsertAudit(ctx, g.pool, store.AuditEntry{ActorUserID: &uid, ActorName: b.username, Action: "command",
			Entity: "element", EntityID: id.String(), Data: data}); err != nil {
			g.log.Warn("gateway: audit stream command", "err", err)
		}
	}
	b.client.Send(mustJSON(struct {
		Type      string `json:"type"`
		ID        string `json:"id,omitempty"`
		ElementID string `json:"element_id"`
		CommandID string `json:"command_id"`
		Status    string `json:"status"`
	}{"command_result", f.ID, id.String(), eventID, "accepted"}))
}

// streamCommandStatus tells the sender's streams what became of a command.
func (g *Gateway) streamCommandStatus(cs events.CommandStatus) {
	uid, err := strconv.ParseInt(cs.UserID, 10, 64)
	if err != nil {
		return
	}
	for _, b := range g.streamClientsOf(&uid) {
		b.client.Send(mustJSON(struct {
			Type      string `json:"type"`
			ID        string `json:"id,omitempty"`
			ElementID string `json:"element_id"`
			CommandID string `json:"command_id"`
			Status    string `json:"status"`
			Reason    string `json:"reason,omitempty"`
		}{"command_result", b.stream.requestOf(cs.CommandID), cs.ElementID.String(), cs.CommandID.String(), cs.Status, cs.Reason}))
	}
}

// --- alerts and permission changes ---

// streamAlert tells the streams that asked for alerts and may read the element.
func (g *Gateway) streamAlert(a events.AlertChanged) {
	frame := mustJSON(struct {
		Type string `json:"type"`
		events.AlertChanged
	}{"alert", a})
	for _, b := range g.hub.AllBrowserClients() {
		s := b.stream
		if s == nil {
			continue
		}
		s.mu.Lock()
		ok := s.alerts && s.visible[a.ElementID]
		s.mu.Unlock()
		if ok {
			b.client.Send(frame)
		}
	}
}

// refreshStream re-reads what a stream may see after a permission change:
// alerts follow it, and an "all" stream subscribes to newly granted elements.
func (g *Gateway) refreshStream(ctx context.Context, b *browserClient) {
	s := b.stream
	s.mu.Lock()
	follow := s.all || s.alerts
	s.mu.Unlock()
	if !follow {
		return
	}
	visible, err := store.ListVisibleElements(ctx, g.pool, b.userID)
	if err != nil {
		g.log.Warn("gateway: refresh stream", "err", err)
		return
	}
	set := make(map[uuid.UUID]bool, len(visible))
	for _, e := range visible {
		set[e.ID] = true
	}
	s.mu.Lock()
	s.visible = set
	all := s.all
	s.mu.Unlock()
	if !all || !s.has(store.ScopeRead) {
		return
	}
	for _, e := range visible {
		if _, ok := b.Perm(e.ID); !ok {
			g.streamAttach(ctx, b, e)
		}
	}
}

// streamClientsOf lists the streams of a user (or of everyone when userID is nil).
func (g *Gateway) streamClientsOf(userID *int64) []*browserClient {
	var all []*browserClient
	if userID != nil {
		all = g.hub.UserClients(*userID)
	} else {
		all = g.hub.AllBrowserClients()
	}
	out := all[:0:0]
	for _, b := range all {
		if b.stream != nil {
			out = append(out, b)
		}
	}
	return out
}
