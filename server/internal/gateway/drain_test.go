package gateway

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/config"
)

func TestGracefulDrain(t *testing.T) {
	cfg := &config.Config{
		DrainPropagationWait: 10 * time.Millisecond,
		DrainDuration:        50 * time.Millisecond,
	}
	g := &Gateway{
		cfg: cfg,
		hub: NewHub(),
		log: slog.Default(),
	}

	dev := uuid.New()
	d1 := testDevice(dev, "d1")
	d2 := testDevice(dev, "d2")
	b1 := testBrowser(1, "b1")

	g.hub.AddDevice(d1, nil)
	g.hub.AddDevice(d2, nil)
	g.hub.AddBrowser(b1)

	if g.IsDraining() {
		t.Fatal("expected IsDraining() to be false initially")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	g.Drain(ctx)
	elapsed := time.Since(start)

	if !g.IsDraining() {
		t.Fatal("expected IsDraining() to be true after Drain()")
	}

	if elapsed < 60*time.Millisecond {
		t.Fatalf("drain finished too fast: %v", elapsed)
	}

	// Check that all clients were killed with StatusServiceRestart (1012)
	for _, c := range []*client{d1.client, d2.client, b1.client} {
		select {
		case <-c.ctx.Done():
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("client %s was not cancelled", c.id)
		}

		c.mu.Lock()
		code := c.closedCode
		reason := c.closedReason
		c.mu.Unlock()

		if code != websocket.StatusCode(1012) {
			t.Errorf("client %s closed with code %v, want 1012", c.id, code)
		}
		if reason != "server restarting" {
			t.Errorf("client %s closed with reason %q, want 'server restarting'", c.id, reason)
		}
	}
}

func TestDrainIdempotent(t *testing.T) {
	cfg := &config.Config{
		DrainPropagationWait: 5 * time.Millisecond,
		DrainDuration:        10 * time.Millisecond,
	}
	g := &Gateway{
		cfg: cfg,
		hub: NewHub(),
		log: slog.Default(),
	}

	g.Drain(context.Background())
	if !g.IsDraining() {
		t.Fatal("expected IsDraining() to be true")
	}

	done := make(chan struct{})
	go func() {
		g.Drain(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("second call to Drain() did not return immediately")
	}
}

func TestDrainContextCancel(t *testing.T) {
	cfg := &config.Config{
		DrainPropagationWait: 500 * time.Millisecond,
		DrainDuration:        500 * time.Millisecond,
	}
	g := &Gateway{
		cfg: cfg,
		hub: NewHub(),
		log: slog.Default(),
	}

	dev := uuid.New()
	d1 := testDevice(dev, "d1")
	g.hub.AddDevice(d1, nil)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after 15ms, well before the 500ms propagation wait completes
	time.AfterFunc(15*time.Millisecond, cancel)

	start := time.Now()
	g.Drain(ctx)
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Fatalf("Drain did not respect context cancellation, took %v", elapsed)
	}

	// Verify client was cancelled
	select {
	case <-d1.ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected client to be closed on cancelled drain")
	}
}
