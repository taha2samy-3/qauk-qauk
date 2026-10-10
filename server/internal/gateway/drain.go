package gateway

import (
	"context"
	"math/rand/v2"
	"time"

	"github.com/coder/websocket"
)

// Drain smoothly closes all active WebSocket connections over the configured
// duration with randomized jitter, preventing a reconnect storm on peer instances.
// It also pauses for DrainPropagationWait so that upstream load balancers remove
// this instance from active endpoints before sockets begin disconnecting.
func (g *Gateway) Drain(ctx context.Context) {
	if g.draining.Swap(true) {
		return // already draining
	}

	g.log.Info("gateway: beginning graceful drain",
		"propagation_wait", g.cfg.DrainPropagationWait,
		"drain_duration", g.cfg.DrainDuration)

	// Step 1: Wait for load balancers / Ingress to observe /readyz failure and update routing.
	if g.cfg.DrainPropagationWait > 0 {
		select {
		case <-time.After(g.cfg.DrainPropagationWait):
		case <-ctx.Done():
			g.log.Warn("gateway: drain interrupted during propagation wait")
			g.shutdown()
			return
		}
	}

	// Step 2: Gather all active sockets.
	devs := g.hub.AllDeviceClients()
	brows := g.hub.AllBrowserClients()
	total := len(devs) + len(brows)
	if total == 0 {
		g.log.Info("gateway: drain complete (no active clients)")
		return
	}

	g.log.Info("gateway: draining active clients", "devices", len(devs), "browsers", len(brows), "total", total)

	if g.cfg.DrainDuration <= 0 {
		g.shutdown()
		return
	}

	type killable interface {
		kill(code websocket.StatusCode, reason string)
	}

	clients := make([]killable, 0, total)
	for _, d := range devs {
		clients = append(clients, d)
	}
	for _, b := range brows {
		clients = append(clients, b)
	}

	// Shuffle clients to avoid closing sockets in a deterministic or biased order.
	rand.Shuffle(len(clients), func(i, j int) {
		clients[i], clients[j] = clients[j], clients[i]
	})

	// WebSocket code 1012 (StatusServiceRestart): standard code indicating service restart.
	const closeCode = websocket.StatusCode(1012)
	const closeReason = "server restarting"

	drainTimer := time.NewTimer(g.cfg.DrainDuration)
	defer drainTimer.Stop()

	// Launch staggered disconnections with randomized jitter
	for _, c := range clients {
		delay := time.Duration(rand.Int64N(int64(g.cfg.DrainDuration)))
		target := c
		time.AfterFunc(delay, func() {
			target.kill(closeCode, closeReason)
		})
	}

	select {
	case <-drainTimer.C:
		g.log.Info("gateway: graceful drain duration elapsed")
	case <-ctx.Done():
		g.log.Warn("gateway: drain context deadline exceeded; killing remaining clients")
		g.shutdown()
	}
}
