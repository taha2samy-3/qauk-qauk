// Package alert evaluates element alert rules on live device values and
// enqueues webhook notifications when a rule starts firing or resolves.
//
// It consumes element-events.v1 in a consumer group (each element's
// partition is evaluated by one instance, in order), so running it on several
// api servers neither duplicates nor misses alerts. Whether a rule is firing
// is kept in element_alert_state and changes only together with the
// deliveries it causes, in one transaction: a restart neither repeats an
// alert nor loses a recovery, and a batch that fails is retried without
// side effects.
package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/db"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/store"
	"github.com/taha2samy/quackquack/server/internal/webhook"
)

// RulesRefresh is how often rule changes are picked up.
const RulesRefresh = 5 * time.Second

// Next returns whether a rule fires after seeing value v, given whether it
// was firing. Hysteresis keeps a firing rule firing until the value is back
// past the threshold by that much (so a value hovering at the threshold
// doesn't flap).
func Next(r store.ElementAlertRule, firing bool, v float64) bool {
	h := math.Max(r.Hysteresis, 0)
	switch r.Condition {
	case "above":
		if firing {
			return v > r.Threshold-h
		}
		return v > r.Threshold
	case "below":
		if firing {
			return v < r.Threshold+h
		}
		return v < r.Threshold
	case "outside_range":
		hi := r.Threshold
		if r.ThresholdMax != nil {
			hi = *r.ThresholdMax
		}
		if firing {
			return v < r.Threshold+h || v > hi-h
		}
		return v < r.Threshold || v > hi
	case "equals":
		eps := 1e-9 * math.Max(1, math.Abs(r.Threshold))
		if firing {
			return math.Abs(v-r.Threshold) <= math.Max(h, eps)
		}
		return math.Abs(v-r.Threshold) <= eps
	}
	return firing
}

// Values returns the numeric attributes of a device message, named and read
// exactly as history stores them (history.Points), so a rule watches the same
// series its chart shows: "value" (the element's value: a bare number,
// message.value, or a chart's y), "temperature", "gps.lat", "sensors[0].t".
// Numbers, booleans (1/0) and numeric strings count.
func Values(msg json.RawMessage) map[string]float64 {
	pts := history.Points(history.Event{Payload: msg, Value: history.NumericValue(msg)})
	out := make(map[string]float64, len(pts))
	for _, p := range pts {
		out[p.Field] = p.Value
	}
	return out
}

// Engine evaluates rules on the element events of its partitions.
type Engine struct {
	Pool    *pgxpool.Pool
	Brokers []string
	Group   string
	Log     *slog.Logger

	mu     sync.RWMutex
	rules  map[uuid.UUID][]store.ActiveAlertRule // by element
	loaded time.Time
}

// Run consumes until ctx ends.
func (e *Engine) Run(ctx context.Context) error {
	gc, err := bus.NewLatestGroupConsumer(e.Brokers, e.Group, events.TopicElementEvents)
	if err != nil {
		return err
	}
	return gc.Run(ctx, e.Log, 500, e.process)
}

func (e *Engine) rulesFor(ctx context.Context, elementID uuid.UUID) ([]store.ActiveAlertRule, error) {
	e.mu.RLock()
	fresh := time.Since(e.loaded) < RulesRefresh
	rules := e.rules[elementID]
	e.mu.RUnlock()
	if fresh {
		return rules, nil
	}
	all, err := store.ListActiveAlertRules(ctx, e.Pool)
	if err != nil {
		return nil, err
	}
	byElement := map[uuid.UUID][]store.ActiveAlertRule{}
	for _, r := range all {
		byElement[r.ElementID] = append(byElement[r.ElementID], r)
	}
	e.mu.Lock()
	e.rules, e.loaded = byElement, time.Now()
	e.mu.Unlock()
	return byElement[elementID], nil
}

// process evaluates a batch. An error retries the whole batch: transitions
// already committed don't repeat (the state no longer differs).
func (e *Engine) process(ctx context.Context, recs []*kgo.Record) error {
	for _, rec := range recs {
		var ev events.Event
		if err := json.Unmarshal(rec.Value, &ev); err != nil || ev.Type != events.TypeElementMessage {
			continue
		}
		var m events.ElementMessage
		if err := ev.DecodeData(&m); err != nil || m.Source != events.SourceDevice {
			continue // commands aren't readings
		}
		rules, err := e.rulesFor(ctx, m.ElementID)
		if err != nil {
			metrics.AlertErrors.Inc()
			return err
		}
		if len(rules) == 0 {
			continue
		}
		values := Values(m.Message)
		for _, r := range rules {
			v, ok := values[r.Field]
			if !ok {
				continue // this message doesn't carry the rule's attribute
			}
			if err := e.evaluate(ctx, r, v, ev.Time.Time); err != nil {
				metrics.AlertErrors.Inc()
				return fmt.Errorf("rule %s: %w", r.ID, err)
			}
		}
	}
	return nil
}

func (e *Engine) evaluate(ctx context.Context, r store.ActiveAlertRule, v float64, at time.Time) error {
	firing, err := store.AlertFiring(ctx, e.Pool, r.ID)
	if err != nil {
		return err
	}
	next := Next(r.ElementAlertRule, firing, v)
	if next == firing {
		return nil
	}
	return db.InTx(ctx, e.Pool, func(tx pgx.Tx) error {
		changed, err := store.SetAlertFiring(ctx, tx, r.ID, next, v)
		if err != nil || !changed {
			return err // !changed: already moved (a retried batch)
		}
		state := webhook.StateResolved
		if next {
			state = webhook.StateFiring
		}
		metrics.AlertTransitions.WithLabelValues(state).Inc()
		return Enqueue(ctx, tx, r, state, v, at)
	})
}

// Enqueue builds a rule's notification and queues it for every enabled
// endpoint subscribed to the rule's severity.
func Enqueue(ctx context.Context, tx store.DBTX, r store.ActiveAlertRule, state string, v float64, at time.Time) error {
	ruleID, elementID, deviceID := r.ID, r.ElementID, r.DeviceID
	evt := webhook.NotificationEvent{
		ID: uuid.Must(uuid.NewV7()), Type: "io.quack.alert.threshold", Severity: webhook.Severity(r.Severity), State: state,
		RuleID: &ruleID, RuleName: r.Name, DeviceID: &deviceID, DeviceName: r.DeviceName,
		ElementID: &elementID, ElementName: r.ElementName, Field: r.Field, Value: v, Timestamp: at.UTC(),
	}
	// what was measured: "Cold room temperature", or "Climate humidity" for an attribute
	what := r.DeviceName + " " + r.ElementName
	subject := r.ElementName
	if r.Field != "" && r.Field != history.ValueField {
		what += " " + r.Field
		subject += " " + r.Field
	}
	num := strconv.FormatFloat(v, 'f', -1, 64)
	if state == webhook.StateFiring {
		evt.Title = "Alert: " + r.Name
		evt.Message = r.Message
		if evt.Message == "" {
			evt.Message = fmt.Sprintf("%s is %s (%s)", num, describe(r.ElementAlertRule), what)
		}
	} else {
		evt.Title = "Resolved: " + r.Name
		evt.Message = fmt.Sprintf("%s is back to normal (%s)", subject, num)
	}
	payload, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	endpoints, err := store.ListWebhookEndpoints(ctx, tx)
	if err != nil {
		return err
	}
	// integration streams hear it too, on every gateway (exactly once with
	// the state change: same transaction)
	ev, err := events.New(events.TypeAlertChanged, events.SourceAPI, r.ElementID.String(), events.AlertChanged{
		RuleID: r.ID, RuleName: r.Name, State: state, Severity: r.Severity, ElementID: r.ElementID, ElementName: r.ElementName,
		DeviceID: r.DeviceID, DeviceName: r.DeviceName, Field: r.Field, Value: v, Message: evt.Message, Time: at.UTC(),
	})
	if err != nil {
		return err
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if err := store.EnqueueOutbox(ctx, tx, events.TopicControlEvents, ev.PartitionKey, b); err != nil {
		return err
	}
	for _, ep := range endpoints {
		if !ep.Enabled || !subscribed(ep.Severities, r.Severity) {
			continue
		}
		if _, err := store.EnqueueWebhookDelivery(ctx, tx, store.WebhookDelivery{EndpointID: ep.ID, EventID: evt.ID,
			EventType: evt.Type, Severity: r.Severity, Payload: payload}); err != nil {
			return err
		}
	}
	return nil
}

func subscribed(severities []string, s string) bool {
	for _, x := range severities {
		if x == s {
			return true
		}
	}
	return false
}

func describe(r store.ElementAlertRule) string {
	f := func(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }
	switch r.Condition {
	case "above":
		return "above " + f(r.Threshold)
	case "below":
		return "below " + f(r.Threshold)
	case "outside_range":
		hi := r.Threshold
		if r.ThresholdMax != nil {
			hi = *r.ThresholdMax
		}
		return "outside " + f(r.Threshold) + "–" + f(hi)
	case "equals":
		return "equal to " + f(r.Threshold)
	}
	return r.Condition
}
