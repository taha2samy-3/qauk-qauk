package gateway

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// Close codes kept from the legacy consumers.
const closeRevoked websocket.StatusCode = 4000

func (g *Gateway) controlWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-g.ctrlCh:
			var c events.ControlChanged
			if err := ev.DecodeData(&c); err != nil {
				g.log.Warn("gateway: bad control event", "id", ev.ID, "err", err)
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			g.applyControl(cctx, c)
			cancel()
		}
	}
}

func (g *Gateway) applyControl(ctx context.Context, c events.ControlChanged) {
	switch c.Kind {
	case events.KindDevice:
		if c.DeviceID != nil {
			g.deviceChanged(ctx, *c.DeviceID, c.Op)
		}
	case events.KindElement:
		if c.ElementID != nil {
			g.elementChanged(ctx, *c.ElementID, c.Op)
		}
	case events.KindJWTKey:
		keyID, err := uuid.Parse(c.ID)
		if err != nil {
			return
		}
		// Any key change forces its devices to reconnect (legacy rotation semantics).
		for _, d := range g.hub.AllDeviceClients() {
			if d.keyID == keyID {
				d.kill(websocket.StatusNormalClosure, "key changed")
			}
		}
	case events.KindPermission:
		if c.ElementID == nil {
			return
		}
		for _, b := range g.hub.Subscribers(*c.ElementID) {
			if c.UserID == nil || *c.UserID == b.userID {
				g.reevaluate(ctx, b, *c.ElementID)
			}
		}
	case events.KindGroupMembership:
		if c.UserID != nil {
			for _, b := range g.hub.UserClients(*c.UserID) {
				for _, id := range b.Subscriptions() {
					g.reevaluate(ctx, b, id)
				}
			}
		}
	case events.KindUser:
		uid, err := strconv.ParseInt(c.ID, 10, 64)
		if err != nil {
			return
		}
		for _, b := range g.hub.UserClients(uid) {
			if _, err := store.SessionUser(ctx, g.pool, b.sessionHash); err != nil {
				b.kill(closeRevoked, "session revoked")
				continue
			}
			for _, id := range b.Subscriptions() {
				g.reevaluate(ctx, b, id)
			}
		}
	}
}

func (g *Gateway) deviceChanged(ctx context.Context, deviceID uuid.UUID, op string) {
	clients := g.hub.DeviceClients(deviceID)
	if len(clients) == 0 {
		return
	}
	if op == events.OpDelete {
		for _, d := range clients {
			d.kill(closeRevoked, "device deleted")
		}
		return
	}
	auth, err := store.GetDeviceAuth(ctx, g.pool, deviceID)
	for _, d := range clients {
		switch {
		case errors.Is(err, store.ErrNotFound):
			d.kill(closeRevoked, "device key removed")
		case err != nil:
			g.log.Warn("gateway: reload device", "device", deviceID, "err", err)
		case auth.KeyID != d.keyID || !auth.KeyActive:
			d.kill(closeRevoked, "device key changed")
		default:
			d.SetName(auth.DeviceName)
		}
	}
}

func (g *Gateway) elementChanged(ctx context.Context, elementID uuid.UUID, op string) {
	if op == events.OpDelete {
		g.hub.RemoveElement(elementID)
		return
	}
	el, err := store.GetElement(ctx, g.pool, elementID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			g.hub.RemoveElement(elementID)
		}
		return
	}
	info := elementInfo{ID: el.ID, DeviceID: el.DeviceID, Points: el.Points}
	if op == events.OpCreate {
		g.hub.AddElement(info)
	} else {
		g.hub.UpdateElement(info)
	}
}
