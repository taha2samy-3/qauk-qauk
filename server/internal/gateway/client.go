package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/metrics"
)

const (
	// maxQueuedFrames bounds memory per socket. It must exceed the largest
	// history replay (points <= 1000) plus live traffic.
	maxQueuedFrames = 2048
	writeTimeout    = 10 * time.Second
	pingInterval    = 30 * time.Second
	readLimit       = 64 << 10
)

// client is one WebSocket connection with a bounded outbound queue. Frames
// are written by a single goroutine, so enqueue order is wire order.
type client struct {
	id   string
	kind string
	ws   *websocket.Conn

	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	queue  [][]byte
	notify chan struct{}

	closeOnce sync.Once
}

func newClient(parent context.Context, ws *websocket.Conn, id, kind string) *client {
	ctx, cancel := context.WithCancel(parent)
	if ws != nil {
		ws.SetReadLimit(readLimit)
	}
	return &client{id: id, kind: kind, ws: ws, ctx: ctx, cancel: cancel, notify: make(chan struct{}, 1)}
}

// Send enqueues a frame without blocking. A client that falls too far behind
// is disconnected with 1013 (try again later) instead of slowing fan-out.
func (c *client) Send(frame []byte) {
	c.mu.Lock()
	if len(c.queue) >= maxQueuedFrames {
		c.mu.Unlock()
		metrics.Dropped.WithLabelValues("slow_consumer").Inc()
		c.kill(websocket.StatusTryAgainLater, "slow consumer")
		return
	}
	c.queue = append(c.queue, frame)
	c.mu.Unlock()
	metrics.FramesOut.WithLabelValues(c.kind).Inc()
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

// kill closes the socket with a code and reason, once, without blocking the caller.
func (c *client) kill(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		go func() {
			if c.ws != nil {
				_ = c.ws.Close(code, reason)
			}
			c.cancel()
		}()
	})
}

func (c *client) writeLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.notify:
		}
		c.mu.Lock()
		batch := c.queue
		c.queue = nil
		c.mu.Unlock()
		for _, frame := range batch {
			ctx, cancel := context.WithTimeout(c.ctx, writeTimeout)
			err := c.ws.Write(ctx, websocket.MessageText, frame)
			cancel()
			if err != nil {
				c.cancel()
				return
			}
		}
	}
}

func (c *client) pingLoop() {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(c.ctx, pingInterval/2)
			err := c.ws.Ping(ctx)
			cancel()
			if err != nil && c.ctx.Err() == nil {
				c.kill(websocket.StatusGoingAway, "ping timeout")
				return
			}
		}
	}
}

// deviceClient is a device (or Node-RED) socket.
type deviceClient struct {
	*client
	deviceID uuid.UUID
	keyID    uuid.UUID

	mu      sync.Mutex
	name    string
	allowed map[uuid.UUID]struct{}

	limiter
}

// limiter is a token bucket of `rate` msgs/s with burst = rate. It is only
// touched by the socket's read goroutine, so it needs no locking.
type limiter struct {
	rate   float64
	tokens float64
	last   time.Time
}

func (d *deviceClient) Name() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.name
}

func (d *deviceClient) SetName(n string) { d.mu.Lock(); d.name = n; d.mu.Unlock() }

func (d *deviceClient) Owns(id uuid.UUID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.allowed[id]
	return ok
}

func (d *deviceClient) Allow(id uuid.UUID)    { d.mu.Lock(); d.allowed[id] = struct{}{}; d.mu.Unlock() }
func (d *deviceClient) Disallow(id uuid.UUID) { d.mu.Lock(); delete(d.allowed, id); d.mu.Unlock() }

func (d *deviceClient) Elements() []uuid.UUID {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]uuid.UUID, 0, len(d.allowed))
	for id := range d.allowed {
		out = append(out, id)
	}
	return out
}

func (l *limiter) allowMessage(now time.Time) bool {
	if l.rate <= 0 {
		return true
	}
	if !l.last.IsZero() {
		l.tokens += now.Sub(l.last).Seconds() * l.rate
	} else {
		l.tokens = l.rate
	}
	l.last = now
	if l.tokens > l.rate {
		l.tokens = l.rate
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

// browserClient is a logged-in user's dashboard socket.
type browserClient struct {
	*client
	userID      int64
	username    string
	sessionHash []byte

	mu   sync.Mutex
	subs map[uuid.UUID]string // element -> permission (R / RC)

	limiter
}

func (b *browserClient) Perm(id uuid.UUID) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.subs[id]
	return p, ok
}

func (b *browserClient) SetPerm(id uuid.UUID, p string) { b.mu.Lock(); b.subs[id] = p; b.mu.Unlock() }
func (b *browserClient) DropPerm(id uuid.UUID)          { b.mu.Lock(); delete(b.subs, id); b.mu.Unlock() }

func (b *browserClient) Subscriptions() []uuid.UUID {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]uuid.UUID, 0, len(b.subs))
	for id := range b.subs {
		out = append(out, id)
	}
	return out
}
