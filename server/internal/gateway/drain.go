package gateway

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/coder/websocket"
)

// StartDrain makes the gateway refuse new device and browser connections,
// gRPC streams and REST long-polls (readiness turns 503); Drain then closes
// the open ones. The caller waits between the two so that load balancers
// stop routing here first (QUACK_DRAIN_PROPAGATION_WAIT).
func (g *Gateway) StartDrain() {
	g.draining.Store(true)
	g.drainMu.Do(func() {
		if g.drainCh != nil {
			close(g.drainCh)
		}
	})
}

// Drain closes the open sockets and streams spread over QUACK_DRAIN_DURATION
// (random order and times, close code 1012 "service restart") so that clients
// reconnect to other instances gradually instead of all at once. It returns
// as soon as none is left. If ctx ends first, or time runs out, the rest are
// closed at once.
func (g *Gateway) Drain(ctx context.Context) {
	g.StartDrain()
	devs, brows := g.hub.AllDeviceClients(), g.hub.AllBrowserClients()
	type killable interface {
		kill(code websocket.StatusCode, reason string)
	}
	clients := make([]killable, 0, len(devs)+len(brows))
	for _, d := range devs {
		clients = append(clients, d)
	}
	for _, b := range brows {
		clients = append(clients, b)
	}
	if len(clients) == 0 {
		g.log.Info("gateway: drain complete (no active clients)")
		return
	}
	if g.cfg.DrainDuration <= 0 {
		g.shutdown()
		return
	}
	g.log.Info("gateway: draining active clients", "devices", len(devs), "browsers", len(brows), "duration", g.cfg.DrainDuration)
	rand.Shuffle(len(clients), func(i, j int) { clients[i], clients[j] = clients[j], clients[i] })
	timers := make([]*time.Timer, 0, len(clients))
	for _, c := range clients {
		timers = append(timers, time.AfterFunc(time.Duration(rand.Int64N(int64(g.cfg.DrainDuration))), func() {
			c.kill(websocket.StatusCode(1012), "server restarting")
		}))
	}
	defer func() {
		for _, t := range timers {
			t.Stop()
		}
	}()
	end := time.NewTimer(g.cfg.DrainDuration)
	defer end.Stop()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-poll.C:
			if len(g.hub.AllDeviceClients())+len(g.hub.AllBrowserClients()) == 0 {
				g.log.Info("gateway: drain complete")
				return
			}
		case <-end.C:
			g.log.Info("gateway: drain duration elapsed; closing the rest")
			g.shutdown()
			return
		case <-ctx.Done():
			g.log.Warn("gateway: drain cut short; closing the rest")
			g.shutdown()
			return
		}
	}
}
