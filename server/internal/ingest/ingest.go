// Package ingest writes every element event from Redpanda into the history
// store (internal/history: TimescaleDB, ClickHouse, …).
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/metrics"
)

const batchSize = 1000

// Publisher is the part of bus.Producer the ingester uses (nil disables the
// dead-letter topic and the latest-state topic).
type Publisher interface {
	PublishSync(ctx context.Context, recs ...*kgo.Record) error
}

type Ingester struct {
	Group     string // consumer group; one per environment/history store
	Store     history.Store
	Brokers   []string
	Publisher Publisher
	Log       *slog.Logger
}

// Run consumes with a shared group: run as many replicas as partitions allow.
// Offsets are committed only after the batch is stored (at-least-once);
// Store.Append is idempotent, so re-delivery is harmless.
func (i *Ingester) Run(ctx context.Context) error {
	gc, err := bus.NewGroupConsumer(i.Brokers, i.Group, events.TopicElementEvents)
	if err != nil {
		return err
	}
	return gc.Run(ctx, i.Log, batchSize, i.process)
}

type item struct {
	ev  history.Event
	rec *kgo.Record
}

func (i *Ingester) process(ctx context.Context, recs []*kgo.Record) error {
	items := make([]item, 0, len(recs))
	var dead []*kgo.Record
	var newest time.Time
	for _, r := range recs {
		ev, err := ToEvent(r.Value)
		if err == nil {
			err = history.ValidateMessage(ev.Payload)
		}
		if err != nil {
			dead = append(dead, deadLetter(r, err))
			continue
		}
		if ev.Time.After(newest) {
			newest = ev.Time
		}
		items = append(items, item{ev: ev, rec: r})
	}
	n, rejected, err := i.appendIsolating(ctx, items)
	if err != nil {
		return err // an outage: the whole batch is retried
	}
	dead = append(dead, rejected...)
	if len(dead) > 0 {
		if err := i.publish(ctx, dead); err != nil {
			return err
		}
		metrics.IngestDeadLetters.Add(float64(len(dead)))
		for _, d := range dead {
			i.Log.Warn("ingest: event dead-lettered", "topic", events.TopicElementEventsDLQ, "reason", header(d, "error"))
		}
	}
	if err := i.publish(ctx, latestState(items)); err != nil {
		// not fatal: the events are stored; the state topic catches up next batch
		i.Log.Warn("ingest: latest-state publish failed", "err", err)
	}
	i.Log.Debug("ingest: batch written", "events", len(items), "new", n, "dead", len(dead))
	metrics.IngestRows.Add(float64(len(items)))
	if !newest.IsZero() {
		metrics.IngestLag.Set(time.Since(newest).Seconds())
	}
	return nil
}

// appendIsolating appends the batch; if the store rejects the data itself
// (history.ErrBadData), it bisects to find the offending events and returns
// them as dead letters, so one bad message can't block ingestion forever.
func (i *Ingester) appendIsolating(ctx context.Context, items []item) (int, []*kgo.Record, error) {
	if len(items) == 0 {
		return 0, nil, nil
	}
	evs := make([]history.Event, len(items))
	for k, it := range items {
		evs[k] = it.ev
	}
	n, err := i.Store.Append(ctx, evs)
	if err == nil {
		return n, nil, nil
	}
	if !errors.Is(err, history.ErrBadData) {
		return 0, nil, err
	}
	if len(items) == 1 {
		return 0, []*kgo.Record{deadLetter(items[0].rec, err)}, nil
	}
	mid := len(items) / 2
	n1, d1, err := i.appendIsolating(ctx, items[:mid])
	if err != nil {
		return 0, nil, err
	}
	n2, d2, err := i.appendIsolating(ctx, items[mid:])
	if err != nil {
		return 0, nil, err
	}
	return n1 + n2, append(d1, d2...), nil
}

func (i *Ingester) publish(ctx context.Context, recs []*kgo.Record) error {
	if i.Publisher == nil || len(recs) == 0 {
		return nil
	}
	pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return i.Publisher.PublishSync(pctx, recs...)
}

func deadLetter(r *kgo.Record, reason error) *kgo.Record {
	return &kgo.Record{
		Topic: events.TopicElementEventsDLQ,
		Key:   r.Key,
		Value: r.Value,
		Headers: append(append([]kgo.RecordHeader(nil), r.Headers...),
			kgo.RecordHeader{Key: "error", Value: []byte(reason.Error())},
			kgo.RecordHeader{Key: "source", Value: []byte(r.Topic + "/" + strconv.Itoa(int(r.Partition)) + "/" + strconv.FormatInt(r.Offset, 10))}),
	}
}

func header(r *kgo.Record, k string) string {
	for _, h := range r.Headers {
		if h.Key == k {
			return string(h.Value)
		}
	}
	return ""
}

// latestState returns one record per element for the compacted
// element-state topic: the newest stored device event of the batch.
func latestState(items []item) []*kgo.Record {
	newest := map[uuid.UUID]item{}
	for _, it := range items {
		if it.ev.Source != history.SourceDevice {
			continue
		}
		if cur, ok := newest[it.ev.ElementID]; !ok || it.ev.Time.After(cur.ev.Time) ||
			(it.ev.Time.Equal(cur.ev.Time) && it.ev.ID.String() > cur.ev.ID.String()) {
			newest[it.ev.ElementID] = it
		}
	}
	out := make([]*kgo.Record, 0, len(newest))
	for id, it := range newest {
		out = append(out, bus.RawRecord(events.TopicElementState, id.String(), it.rec.Value))
	}
	return out
}

// ToEvent converts a CloudEvent record value into a history event.
func ToEvent(value []byte) (history.Event, error) {
	var ev events.Event
	if err := json.Unmarshal(value, &ev); err != nil {
		return history.Event{}, errors.New("not a CloudEvent")
	}
	if ev.Type != events.TypeElementMessage {
		return history.Event{}, errors.New("unexpected event type " + ev.Type)
	}
	var m events.ElementMessage
	if err := ev.DecodeData(&m); err != nil {
		return history.Event{}, errors.New("undecodable data: " + err.Error())
	}
	id, err := uuid.Parse(ev.ID)
	if err != nil {
		return history.Event{}, errors.New("event id is not a UUID")
	}
	out := history.Event{
		Time: ev.Time.Time.UTC(), ID: id, ElementID: m.ElementID, DeviceID: m.DeviceID, Source: m.Source,
		ActorID: m.Actor.ID, ActorName: m.Actor.Name, Payload: m.Message,
	}
	if len(out.Payload) == 0 {
		out.Payload = json.RawMessage("null")
	}
	out.Value = history.NumericValue(out.Payload)
	if m.ClientTS != nil {
		t := m.ClientTS.Time.UTC()
		out.ClientTS = &t
	}
	return out, nil
}
