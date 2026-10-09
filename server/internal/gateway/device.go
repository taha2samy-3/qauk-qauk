package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/registry"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// DeviceHandler serves /device/node_red/ (legacy device protocol v1).
func (g *Gateway) DeviceHandler() http.Handler {
	return http.HandlerFunc(g.handleDevice)
}

func (g *Gateway) handleDevice(w http.ResponseWriter, r *http.Request) {
	token, ok := authn.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		g.rejectDevice(w, "no_token", nil)
		return
	}
	dev, dk, err := g.authenticateDevice(r.Context(), token)
	if err != nil {
		g.rejectDevice(w, "auth", err)
		return
	}
	// Devices authenticate with a JWT, not cookies, so Origin is irrelevant here.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	d := &deviceClient{
		client:   newClient(g.ctx, ws, g.nextConnID(), "device"),
		deviceID: dk.DeviceID, keyID: dk.KeyID, name: dk.DeviceName,
		allowed: map[uuid.UUID]struct{}{},
	}
	g.hub.AddDevice(d, deviceElementInfos(dev))
	metrics.WSConnections.WithLabelValues("device").Inc()
	connAudit := g.deviceConnected(d.deviceID, d.id, connDetails(r, d.id))

	go d.writeLoop()
	go d.pingLoop()
	for {
		typ, data, err := ws.Read(d.ctx)
		if err != nil {
			break
		}
		if typ != websocket.MessageText {
			continue
		}
		metrics.MessagesIn.WithLabelValues("device").Inc()
		if g.allowDevice(d.deviceID, 1) != nil {
			continue
		}
		g.onDeviceFrame(d, data)
	}
	d.cancel()
	g.hub.RemoveDevice(d)
	metrics.WSConnections.WithLabelValues("device").Dec()
	g.deviceDisconnected(d.deviceID, d.id, connAudit)
}

func deviceElementInfos(dev *registry.Device) []elementInfo {
	infos := make([]elementInfo, len(dev.Elements))
	for i, e := range dev.Elements {
		infos[i] = elementInfo{ID: e.ID, DeviceID: dev.ID, Points: e.Points}
	}
	return infos
}

// connDetails is the audit record of a new device connection.
func connDetails(r *http.Request, connID string) map[string]any {
	return map[string]any{"client": r.RemoteAddr, "path": r.URL.Path, "user_agent": r.UserAgent(), "conn_id": connID}
}

type deviceElement struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Points      int             `json:"points"`
	Details     json.RawMessage `json:"details"`
	// The element's effective rate limit (server defaults applied), so
	// clients can enforce it themselves and report overruns.
	Rate      float64 `json:"rate"`
	Burst     int     `json:"burst"`
	OverLimit string  `json:"over_limit"`
}

type deviceElementsResponse struct {
	Device struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"device"`
	Elements []deviceElement `json:"elements"`
	// DeviceRate is the per-device guard (messages per second, 0 = none).
	DeviceRate float64 `json:"device_rate"`
}

// DeviceElementsHandler serves GET /device/elements: the calling device's own
// elements, authenticated with the same bearer JWT as the socket. It lets
// device-side tools (the Node-RED nodes) address elements by name.
func (g *Gateway) DeviceElementsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := authn.BearerToken(r.Header.Get("Authorization"))
		if !ok {
			g.rejectDevice(w, "no_token", nil)
			return
		}
		dk, err := g.verifier.Verify(r.Context(), token)
		if err != nil {
			g.rejectDevice(w, "auth", err)
			return
		}
		// Descriptions and widget details aren't in the registry; this is a
		// setup-time call, not the message path, so read them from the database.
		elems, err := store.ListElements(r.Context(), g.pool, &dk.DeviceID)
		if err != nil {
			g.log.Error("gateway: list device elements", "device", dk.DeviceID, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		var out deviceElementsResponse
		out.Device.ID, out.Device.Name = dk.DeviceID, dk.DeviceName
		out.DeviceRate = g.cfg.DeviceMsgRate
		out.Elements = make([]deviceElement, len(elems))
		for i, e := range elems {
			details := e.Details
			if len(details) == 0 {
				details = json.RawMessage("null")
			}
			lim := g.elementLimit(registry.Element{Burst: derefInt(e.MsgBurst), Rate: derefFloat(e.MsgRate)})
			burst := lim.Burst
			if burst < 1 {
				burst = max(1, int(lim.Rate))
			}
			out.Elements[i] = deviceElement{ID: e.ID, Name: e.Name, Description: e.Description, Points: e.Points, Details: details,
				Rate: lim.Rate, Burst: burst, OverLimit: e.OverLimit}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(out)
	})
}

func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func (g *Gateway) rejectDevice(w http.ResponseWriter, reason string, err error) {
	metrics.WSRejected.WithLabelValues("device", reason).Inc()
	g.log.Info("gateway: device rejected", "reason", reason, "err", err)
	http.Error(w, "Device authentication failed", http.StatusForbidden)
}

type deviceFrameIn struct {
	ElementID  string          `json:"element_id"`
	Message    json.RawMessage `json:"message"`
	LastEditAt *string         `json:"last_edit_at"`
	// "auth" is intentionally ignored: the actor is stamped from the
	// authenticated device identity (B3).
}

func (g *Gateway) onDeviceFrame(d *deviceClient, data []byte) {
	var f deviceFrameIn
	if err := json.Unmarshal(data, &f); err != nil || f.ElementID == "" || f.Message == nil {
		g.log.Warn("gateway: invalid device frame", "device", d.deviceID, "err", err)
		return
	}
	dev, ok := g.registry.Lookup(d.deviceID)
	if !ok {
		return // deleted: the socket is being closed
	}
	in := DeviceMessage{Element: f.ElementID, Message: f.Message}
	if f.LastEditAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *f.LastEditAt); err == nil {
			in.ClientTS = &t
		}
	}
	// Protocol v1 has no device error frames: rejected messages are dropped
	// (and counted in quack_dropped_total by reason).
	if _, err := g.publishDeviceMessage(dev, d.id, in); err != nil {
		g.log.Debug("gateway: device message dropped", "device", d.deviceID, "element_id", f.ElementID, "err", err)
	}
}

// --- Presence ---

// deviceConnected opens a presence lease and a connection audit row for one
// device connection (a socket, a stream, or REST activity) and announces the
// device if it wasn't connected anywhere.
func (g *Gateway) deviceConnected(deviceID uuid.UUID, connID string, info map[string]any) uuid.UUID {
	if g.pool == nil { // unit tests: no database
		return uuid.Nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	was, err := store.DeviceConnected(ctx, g.pool, deviceID, g.cfg.PresenceTTL)
	if err != nil {
		g.log.Warn("gateway: presence check", "err", err)
	}
	if err := store.InsertPresence(ctx, g.pool, deviceID, g.cfg.GatewayID, connID); err != nil {
		g.log.Warn("gateway: presence insert", "err", err)
	}
	info["gateway_id"] = g.cfg.GatewayID
	details, _ := json.Marshal(info)
	audit := uuid.Must(uuid.NewV7())
	if err := store.InsertConnection(ctx, g.pool, store.Connection{ID: audit, DeviceID: deviceID,
		GatewayID: g.cfg.GatewayID, Details: details}); err != nil {
		g.log.Warn("gateway: connection audit", "err", err)
	}
	if !was {
		g.emitPresence(deviceID, true)
	}
	return audit
}

func (g *Gateway) deviceDisconnected(deviceID uuid.UUID, connID string, audit uuid.UUID) {
	if g.pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.DeletePresence(ctx, g.pool, deviceID, g.cfg.GatewayID, connID); err != nil {
		g.log.Warn("gateway: presence delete", "err", err)
	}
	_ = store.CloseConnection(ctx, g.pool, audit)
	still, err := store.DeviceConnected(ctx, g.pool, deviceID, g.cfg.PresenceTTL)
	if err == nil && !still {
		g.emitPresence(deviceID, false)
	}
}

func (g *Gateway) emitPresence(deviceID uuid.UUID, connected bool) {
	g.hub.PresenceChanged(deviceID, connected)
	ev, err := events.New(events.TypeDevicePresence, events.GatewaySource(g.cfg.GatewayID), deviceID.String(),
		events.DevicePresence{DeviceID: deviceID, Connected: connected, GatewayID: g.cfg.GatewayID})
	if err == nil {
		g.producer.Publish(g.ctx, events.TopicPresence, ev)
	}
}

// presenceLoop heartbeats this gateway's leases and sweeps expired leases of
// crashed gateways (B9). One sweeper runs at a time via an advisory lock.
func (g *Gateway) presenceLoop(ctx context.Context) {
	hb := time.NewTicker(g.cfg.PresenceHeartbeat)
	defer hb.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
		}
		if err := store.HeartbeatPresence(ctx, g.pool, g.cfg.GatewayID); err != nil && ctx.Err() == nil {
			g.log.Warn("gateway: heartbeat", "err", err)
		}
		g.sweep(ctx)
	}
}

const sweepLockKey = 0x717561636b // "quack"

func (g *Gateway) sweep(ctx context.Context) {
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, sweepLockKey).Scan(&locked); err != nil || !locked {
		return
	}
	gone, err := store.SweepPresence(ctx, tx, g.cfg.PresenceTTL)
	if err != nil || tx.Commit(ctx) != nil {
		return
	}
	for _, id := range gone {
		g.log.Info("gateway: device lease expired", "device", id)
		g.emitPresence(id, false)
	}
}
