// Package ingest writes every element event from Redpanda into TimescaleDB.
package ingest

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/metrics"
	"github.com/taha2samy/quackquack/server/internal/store"
)

const batchSize = 1000

type Ingester struct {
	Group   string // consumer group; one per environment/database
	Pool    *pgxpool.Pool
	Brokers []string
	Log     *slog.Logger
}

// Run consumes with a shared group: run as many replicas as partitions allow.
// Offsets are committed only after the batch is written (at-least-once), and
// the (time, event_id) unique key makes re-delivery harmless.
func (i *Ingester) Run(ctx context.Context) error {
	gc, err := bus.NewGroupConsumer(i.Brokers, i.Group, events.TopicElementEvents)
	if err != nil {
		return err
	}
	return gc.Run(ctx, i.Log, batchSize, i.process)
}

func (i *Ingester) process(ctx context.Context, recs []*kgo.Record) error {
	rows := make([]store.EventRow, 0, len(recs))
	var newest time.Time
	for _, r := range recs {
		row, ok := ToRow(r.Value)
		if !ok {
			i.Log.Warn("ingest: skipping undecodable record", "partition", r.Partition, "offset", r.Offset)
			continue
		}
		if row.Time.After(newest) {
			newest = row.Time
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil
	}
	n, err := store.InsertEvents(ctx, i.Pool, rows)
	if err != nil {
		return err
	}
	i.Log.Debug("ingest: batch written", "rows", len(rows), "inserted", n)
	metrics.IngestRows.Add(float64(len(rows)))
	metrics.IngestLag.Set(time.Since(newest).Seconds())
	return nil
}

// ToRow converts a CloudEvent record value into a TSDB row.
func ToRow(value []byte) (store.EventRow, bool) {
	var ev events.Event
	if err := json.Unmarshal(value, &ev); err != nil || ev.Type != events.TypeElementMessage {
		return store.EventRow{}, false
	}
	var m events.ElementMessage
	if err := ev.DecodeData(&m); err != nil {
		return store.EventRow{}, false
	}
	id, err := uuid.Parse(ev.ID)
	if err != nil {
		return store.EventRow{}, false
	}
	row := store.EventRow{
		Time: ev.Time.Time, EventID: id, ElementID: m.ElementID, DeviceID: m.DeviceID, Source: m.Source,
		ActorID: m.Actor.ID, ActorName: m.Actor.Name, Payload: m.Message, Value: NumericValue(m.Message),
	}
	if len(row.Payload) == 0 {
		row.Payload = json.RawMessage("null")
	}
	if m.ClientTS != nil {
		t := m.ClientTS.Time
		row.ClientTS = &t
	}
	return row, true
}

// NumericValue extracts message.value, chart-style message.y, or a bare
// number/bool for aggregates.
func NumericValue(msg json.RawMessage) *float64 {
	var v any
	if err := json.Unmarshal(msg, &v); err != nil {
		return nil
	}
	if obj, ok := v.(map[string]any); ok {
		if val, ok := obj["value"]; ok {
			v = val
		} else {
			v = obj["y"]
		}
	}
	switch x := v.(type) {
	case float64:
		return &x
	case bool:
		f := 0.0
		if x {
			f = 1
		}
		return &f
	}
	return nil
}
