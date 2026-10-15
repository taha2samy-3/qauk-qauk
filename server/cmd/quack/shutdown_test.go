package main

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"
)

type steps struct {
	mu  sync.Mutex
	log []string
}

func (s *steps) add(name string) {
	s.mu.Lock()
	s.log = append(s.log, name)
	s.mu.Unlock()
}

func plan(s *steps, propagation time.Duration, wait func(context.Context)) shutdownPlan {
	return shutdownPlan{
		PropagationWait: propagation,
		HTTPTimeout:     time.Second,
		WaitTimeout:     time.Second,
		StartDraining:   func() { s.add("draining") },
		DrainSockets:    func(context.Context) { s.add("drain sockets") },
		StopHTTP:        func(context.Context) error { s.add("stop http"); return nil },
		CancelRun:       func() { s.add("cancel run") },
		WaitRun:         wait,
		Log:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// In-flight requests finish (HTTP stops) before the background context that
// produces their records is cancelled, and shutdown waits for the loops.
func TestShutdownOrder(t *testing.T) {
	var s steps
	plan(&s, 10*time.Millisecond, func(context.Context) { s.add("wait run") }).run(context.Background())
	want := []string{"draining", "drain sockets", "stop http", "cancel run", "wait run"}
	if !slices.Equal(s.log, want) {
		t.Fatalf("order %v, want %v", s.log, want)
	}
}

// A second signal cuts the propagation wait and the wait for the loops.
func TestShutdownAbort(t *testing.T) {
	var s steps
	abort, cancel := context.WithCancel(context.Background())
	waited := make(chan time.Duration, 1)
	p := plan(&s, time.Hour, func(ctx context.Context) {
		start := time.Now()
		<-ctx.Done() // a loop that never stops on its own
		waited <- time.Since(start)
	})
	p.WaitTimeout = time.Hour
	time.AfterFunc(20*time.Millisecond, cancel)
	start := time.Now()
	p.run(abort)
	if took := time.Since(start); took > time.Second {
		t.Fatalf("an aborted shutdown took %v", took)
	}
	if !slices.Contains(s.log, "cancel run") {
		t.Fatal("the loops must still be cancelled on abort")
	}
	if d := <-waited; d > time.Second {
		t.Fatalf("the wait wasn't cut: %v", d)
	}
}

func TestWaitGroupHonoursContext(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	waitGroup(ctx, &wg)
	if time.Since(start) > time.Second {
		t.Fatal("waitGroup must stop at the deadline")
	}
	wg.Done()
}
