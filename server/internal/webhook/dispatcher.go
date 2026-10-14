package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/secrets"
	"github.com/taha2samy/quackquack/server/internal/store"
)

const (
	// BaseRetryDelay is the first retry delay; each retry doubles it (±20 %).
	BaseRetryDelay = 10 * time.Second
	// MaxRetryDelay caps a retry delay (and a Retry-After).
	MaxRetryDelay = time.Hour
	// leaseFor is how long a claimed delivery stays claimed (a request takes
	// at most RequestTimeout).
	leaseFor = time.Minute
)

// Dispatcher sends notifications to endpoints.
type Dispatcher struct {
	client *http.Client
	box    *secrets.Box
}

// NewDispatcher returns a dispatcher; allowPrivate is QUACK_WEBHOOK_ALLOW_PRIVATE.
func NewDispatcher(box *secrets.Box, allowPrivate bool) *Dispatcher {
	return &Dispatcher{client: NewHTTPClient(allowPrivate), box: box}
}

// ComputeBackoff is the delay before retry attempt+1: BaseRetryDelay · 2^attempt,
// capped at MaxRetryDelay, ±20 %.
func ComputeBackoff(attempt int) time.Duration {
	d := math.Min(float64(BaseRetryDelay)*math.Pow(2, float64(attempt)), float64(MaxRetryDelay))
	return time.Duration(d * (0.8 + rand.Float64()*0.4))
}

// Target opens an endpoint's secrets.
func (d *Dispatcher) Target(ep store.WebhookEndpoint) (Target, error) {
	t := Target{Format: ep.Format, Headers: map[string]string{}}
	var err error
	if t.URL, err = d.box.Open(ep.URL); err != nil {
		return t, fmt.Errorf("url: %w", err)
	}
	if t.Secret, err = d.box.Open(ep.Secret); err != nil {
		return t, fmt.Errorf("secret: %w", err)
	}
	var stored map[string]string
	if len(ep.Headers) > 0 {
		if err := json.Unmarshal(ep.Headers, &stored); err != nil {
			return t, fmt.Errorf("headers: %w", err)
		}
	}
	for k, v := range stored {
		if t.Headers[k], err = d.box.Open(v); err != nil {
			return t, fmt.Errorf("header %s: %w", k, err)
		}
	}
	if ep.CustomTemplate != nil {
		t.Template = *ep.CustomTemplate
	}
	var opts Options
	if len(ep.Options) > 0 {
		_ = json.Unmarshal(ep.Options, &opts)
	}
	t.ChatID = opts.ChatID
	return t, nil
}

// Result is the outcome of one request.
type Result struct {
	StatusCode int
	LatencyMs  int
	Body       string        // the start of the response body
	RetryAfter time.Duration // from Retry-After on 429/503
	Err        error         // nil: 2xx
	Terminal   bool          // retrying won't help (4xx other than 408 and 429, or a broken endpoint)
}

// Send posts an event to a target once.
func (d *Dispatcher) Send(ctx context.Context, t Target, evt NotificationEvent) Result {
	body, headers, err := FormatPayload(t, evt, time.Now())
	if err != nil {
		return Result{Err: err, Terminal: true}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(body))
	if err != nil {
		return Result{Err: errors.New("invalid URL"), Terminal: true}
	}
	req.Header = headers
	start := time.Now()
	resp, err := d.client.Do(req)
	res := Result{LatencyMs: int(time.Since(start).Milliseconds())}
	if err != nil {
		res.Err = errors.New(cleanError(err))
		return res
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes))
	res.StatusCode, res.Body = resp.StatusCode, string(b)
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return res
	case code == http.StatusTooManyRequests || code == http.StatusServiceUnavailable:
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			res.RetryAfter = min(time.Duration(s)*time.Second, MaxRetryDelay)
		}
	case code >= 400 && code < 500 && code != http.StatusRequestTimeout:
		res.Terminal = true
	}
	msg := res.Body
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	res.Err = fmt.Errorf("HTTP %d: %s", res.StatusCode, msg)
	return res
}

// SendTest sends a sample notification to an endpoint at once (nothing is
// enqueued).
func (d *Dispatcher) SendTest(ctx context.Context, ep store.WebhookEndpoint, severity Severity) Result {
	t, err := d.Target(ep)
	if err != nil {
		return Result{Err: err, Terminal: true}
	}
	evt := SampleEvent(severity)
	evt.ID = uuid.New()
	return d.Send(ctx, t, evt)
}

// Worker delivers queued notifications: Workers deliveries at a time, each
// endpoint's in order. Several instances can run (one per api-role server):
// claims are leased rows, taken with SKIP LOCKED.
type Worker struct {
	DB         store.DBTX
	Dispatcher *Dispatcher
	Workers    int
	Poll       time.Duration
	Log        *slog.Logger
}

// Run delivers until ctx ends, then waits for the requests in flight
// (bounded by RequestTimeout) so their results are recorded.
func (w *Worker) Run(ctx context.Context) {
	workers := max(w.Workers, 1)
	poll := w.Poll
	if poll <= 0 {
		poll = 2 * time.Second
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	tick := time.NewTicker(poll)
	defer tick.Stop()
	gauge := time.NewTicker(15 * time.Second)
	defer gauge.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-gauge.C:
			if n, err := store.CountOpenWebhookDeliveries(ctx, w.DB); err == nil {
				metrics.WebhookQueue.Set(float64(n))
			}
			continue
		case <-tick.C:
		}
		free := workers - len(sem)
		if free <= 0 {
			continue
		}
		claimed, err := store.ClaimWebhookDeliveries(ctx, w.DB, free, leaseFor)
		if err != nil {
			if ctx.Err() == nil {
				w.Log.Warn("webhook: claim failed", "err", err)
			}
			continue
		}
		for _, dlv := range claimed {
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() { <-sem; wg.Done() }()
				// a delivery in flight finishes (and is recorded) even when
				// the server is stopping
				w.process(context.WithoutCancel(ctx), dlv)
			}()
		}
	}
}

func (w *Worker) process(ctx context.Context, dlv store.WebhookDelivery) {
	record := func(f func(context.Context) error) {
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := f(rctx); err != nil {
			w.Log.Warn("webhook: recording a delivery failed", "delivery", dlv.ID, "err", err)
		}
	}
	ep, err := store.GetWebhookEndpoint(ctx, w.DB, dlv.EndpointID)
	if err != nil {
		reason := "endpoint unavailable"
		if errors.Is(err, store.ErrNotFound) {
			reason = "endpoint deleted"
		}
		metrics.WebhookDeliveries.WithLabelValues("failed").Inc()
		record(func(c context.Context) error {
			return store.RecordDeliveryFailure(c, w.DB, dlv.ID, nil, reason, 0, ComputeBackoff(dlv.Attempts), errors.Is(err, store.ErrNotFound))
		})
		return
	}
	var evt NotificationEvent
	if err := json.Unmarshal(dlv.Payload, &evt); err != nil {
		record(func(c context.Context) error {
			return store.RecordDeliveryFailure(c, w.DB, dlv.ID, nil, "unreadable payload", 0, 0, true)
		})
		return
	}
	var res Result
	if t, err := w.Dispatcher.Target(ep); err != nil {
		res = Result{Err: err, Terminal: true}
	} else {
		reqCtx, cancel := context.WithTimeout(ctx, RequestTimeout+time.Second)
		res = w.Dispatcher.Send(reqCtx, t, evt)
		cancel()
	}
	if res.Err == nil {
		metrics.WebhookDeliveries.WithLabelValues("delivered").Inc()
		record(func(c context.Context) error {
			return store.RecordDeliverySuccess(c, w.DB, dlv.ID, res.StatusCode, res.LatencyMs)
		})
		return
	}
	last := res.Terminal || dlv.Attempts+1 >= dlv.MaxAttempts
	if last {
		metrics.WebhookDeliveries.WithLabelValues("failed").Inc()
		w.Log.Warn("webhook: delivery failed", "endpoint", ep.ID, "delivery", dlv.ID, "attempts", dlv.Attempts+1, "err", res.Err)
	} else {
		metrics.WebhookDeliveries.WithLabelValues("retry").Inc()
	}
	var code *int
	if res.StatusCode > 0 {
		code = &res.StatusCode
	}
	delay := max(ComputeBackoff(dlv.Attempts), res.RetryAfter)
	record(func(c context.Context) error {
		return store.RecordDeliveryFailure(c, w.DB, dlv.ID, code, res.Err.Error(), res.LatencyMs, delay, res.Terminal)
	})
}

// Purge deletes finished deliveries older than retention, hourly, until ctx ends.
func Purge(ctx context.Context, db store.DBTX, retention time.Duration, log *slog.Logger) {
	if retention <= 0 {
		return
	}
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		if n, err := store.PurgeWebhookDeliveries(ctx, db, retention); err != nil && ctx.Err() == nil {
			log.Warn("webhook: purge failed", "err", err)
		} else if n > 0 {
			log.Info("webhook: purged old deliveries", "deleted", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
