package main

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// shutdownPlan is the order `quack serve` stops in (docs/02_architecture.md,
// "Graceful drain"):
//
//  1. readiness turns 503 and new connections are refused (every role);
//  2. wait PropagationWait so load balancers stop routing here;
//  3. close the gateway's sockets and streams, spread over the drain duration;
//  4. stop the HTTP server: in-flight requests (REST publishes, gRPC unary)
//     finish, so what a device was told is accepted is produced;
//  5. cancel the background loops (bus consumers, MQTT, outbox, workers);
//  6. wait for them (MQTT leaves the member list, held messages are flushed,
//     webhook requests in flight are recorded).
//
// The producer's bounded flush and the pool's close run after (deferred in
// serve). A second signal (abort) cuts every wait short.
type shutdownPlan struct {
	PropagationWait time.Duration
	HTTPTimeout     time.Duration
	WaitTimeout     time.Duration
	StartDraining   func()
	DrainSockets    func(context.Context) // nil without the gateway role
	StopHTTP        func(context.Context) error
	CancelRun       func()
	WaitRun         func(context.Context)
	Log             *slog.Logger
}

func (p shutdownPlan) run(abort context.Context) {
	p.StartDraining()
	if p.PropagationWait > 0 {
		p.Log.Info("shutdown: not ready; waiting for load balancers", "wait", p.PropagationWait)
		select {
		case <-time.After(p.PropagationWait):
		case <-abort.Done():
		}
	}
	if p.DrainSockets != nil {
		p.DrainSockets(abort)
	}
	hctx, cancel := context.WithTimeout(abort, p.HTTPTimeout)
	if err := p.StopHTTP(hctx); err != nil {
		p.Log.Warn("shutdown: HTTP requests still running were cut", "err", err)
	}
	cancel()
	p.CancelRun()
	wctx, cancel := context.WithTimeout(abort, p.WaitTimeout)
	defer cancel()
	p.WaitRun(wctx)
	if abort.Err() != nil {
		p.Log.Warn("shutdown: aborted by a second signal")
	}
}

// waitGroup waits for wg until ctx ends.
func waitGroup(ctx context.Context, wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
