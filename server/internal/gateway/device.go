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
	dk, err := g.verifier.Verify(r.Context(), token)
	if err != nil {
		g.rejectDevice(w, "auth", err)
		return
	}
	elems, err := store.ListElements(r.Context(), g.pool, &dk.DeviceID)
	if err != nil {
		g.log.Error("gateway: load device elements", "device", dk.DeviceID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
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
		allowed: map[uuid.UUID]struct{}{}, limiter: limiter{rate: g.cfg.DeviceMsgRate},
	}
	infos := make([]elementInfo, len(elems))
	for i, e := range elems {
		infos[i] = elementInfo{ID: e.ID, DeviceID: e.DeviceID, Points: e.Points}
	}
	g.hub.AddDevice(d, infos)
	metrics.WSConnections.WithLabelValues("device").Inc()
	connAudit := g.deviceConnected(d, r)

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
		if !d.allowMessage(time.Now()) {
			metrics.Dropped.WithLabelValues("device_rate_limit").Inc()
			continue
		}
		g.onDeviceFrame(d, data)
	}
	d.cancel()
	g.hub.RemoveDevice(d)
	metrics.WSConnections.WithLabelValues("device").Dec()
	g.deviceDisconnected(d, connAudit)
}

type deviceElement struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Points      int             `json:"points"`
	Details     json.RawMessage `json:"details"`
}

type deviceElementsResponse struct {
	Device struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	} `json:"device"`
	Elements []deviceElement `json:"elements"`
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
		elems, err := store.ListElements(r.Context(), g.pool, &dk.DeviceID)
		if err != nil {
			g.log.Error("gateway: list device elements", "device", dk.DeviceID, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		var out deviceElementsResponse
		out.Device.ID, out.Device.Name = dk.DeviceID, dk.DeviceName
		out.Elements = make([]deviceElement, len(elems))
		for i, e := range elems {
			details := e.Details
			if len(details) == 0 {
				details = json.RawMessage("null")
			}
			out.Elements[i] = deviceElement{ID: e.ID, Name: e.Name, Description: e.Description, Points: e.Points, Details: details}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(out)
	})
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
	elementID, err := uuid.Parse(f.ElementID)
	if err != nil || !d.Owns(elementID) {
		g.log.Warn("gateway: device sent data for an unauthorized element", "device", d.deviceID, "element_id", f.ElementID)
		metrics.Dropped.WithLabelValues("foreign_element").Inc()
		return
	}
	m := &events.ElementMessage{
		ElementID: elementID,
		DeviceID:  d.deviceID,
		Source:    events.SourceDevice,
		Actor:     events.Actor{ID: d.deviceID.String(), Name: d.Name()},
		Origin:    events.Origin{GatewayID: g.cfg.GatewayID, ConnID: d.id},
		Message:   f.Message,
	}
	if f.LastEditAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *f.LastEditAt); err == nil {
			m.ClientTS = &events.Time{Time: t.UTC()}
		}
	}
	g.publishElement(m, d.id)
}

// --- Presence ---

func (g *Gateway) deviceConnected(d *deviceClient, r *http.Request) uuid.UUID {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	was, err := store.DeviceConnected(ctx, g.pool, d.deviceID, g.cfg.PresenceTTL)
	if err != nil {
		g.log.Warn("gateway: presence check", "err", err)
	}
	if err := store.InsertPresence(ctx, g.pool, d.deviceID, g.cfg.GatewayID, d.id); err != nil {
		g.log.Warn("gateway: presence insert", "err", err)
	}
	details, _ := json.Marshal(map[string]any{
		"client": r.RemoteAddr, "path": r.URL.Path, "user_agent": r.UserAgent(),
		"gateway_id": g.cfg.GatewayID, "conn_id": d.id,
	})
	audit := uuid.Must(uuid.NewV7())
	if err := store.InsertConnection(ctx, g.pool, store.Connection{ID: audit, DeviceID: d.deviceID,
		GatewayID: g.cfg.GatewayID, Details: details}); err != nil {
		g.log.Warn("gateway: connection audit", "err", err)
	}
	if !was {
		g.emitPresence(d.deviceID, true)
	}
	return audit
}

func (g *Gateway) deviceDisconnected(d *deviceClient, audit uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.DeletePresence(ctx, g.pool, d.deviceID, g.cfg.GatewayID, d.id); err != nil {
		g.log.Warn("gateway: presence delete", "err", err)
	}
	_ = store.CloseConnection(ctx, g.pool, audit)
	still, err := store.DeviceConnected(ctx, g.pool, d.deviceID, g.cfg.PresenceTTL)
	if err == nil && !still {
		g.emitPresence(d.deviceID, false)
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
