package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type EventRow struct {
	Time      time.Time       `db:"time"`
	EventID   uuid.UUID       `db:"event_id"`
	ElementID uuid.UUID       `db:"element_id"`
	DeviceID  uuid.UUID       `db:"device_id"`
	Source    string          `db:"source"`
	ActorID   string          `db:"actor_id"`
	ActorName string          `db:"actor_name"`
	ClientTS  *time.Time      `db:"client_ts"`
	Payload   json.RawMessage `db:"payload"`
	Value     *float64        `db:"value"`
}

const eventCols = `time, event_id, element_id, device_id, source, actor_id, actor_name, client_ts, payload, value`

// LastDeviceEvents returns the newest n device-originated events, oldest first.
// Timestamps have millisecond precision, so ties are broken by the UUIDv7 event id.
func LastDeviceEvents(ctx context.Context, db DBTX, elementID uuid.UUID, n int) ([]EventRow, error) {
	return many[EventRow](db.Query(ctx, `SELECT * FROM (
			SELECT `+eventCols+` FROM element_event WHERE element_id = $1 AND source = 'device'
			ORDER BY time DESC, event_id DESC LIMIT $2) t ORDER BY time, event_id`, elementID, n))
}

func EventsInRange(ctx context.Context, db DBTX, elementID uuid.UUID, from, to time.Time, limit int) ([]EventRow, error) {
	return many[EventRow](db.Query(ctx, `SELECT `+eventCols+` FROM element_event
		WHERE element_id = $1 AND time >= $2 AND time < $3 ORDER BY time, event_id LIMIT $4`, elementID, from, to, limit))
}

type Bucket struct {
	Bucket time.Time `db:"bucket" json:"t"`
	Avg    *float64  `db:"avg" json:"avg"`
	Min    *float64  `db:"min" json:"min"`
	Max    *float64  `db:"max" json:"max"`
	N      int64     `db:"n" json:"n"`
}

// Buckets aggregates numeric values. step is a Postgres interval ("1 minute", "1 hour").
// It reads the 1-minute continuous aggregate (real-time aggregation covers the newest data).
func Buckets(ctx context.Context, db DBTX, elementID uuid.UUID, from, to time.Time, step time.Duration) ([]Bucket, error) {
	return many[Bucket](db.Query(ctx, `SELECT time_bucket($4::interval, bucket) AS bucket,
			sum(avg * n) / NULLIF(sum(n), 0) AS avg, min(min) AS min, max(max) AS max, sum(n)::bigint AS n
		FROM element_value_1m WHERE element_id = $1 AND bucket >= $2 AND bucket < $3
		GROUP BY 1 ORDER BY 1`, elementID, from, to, step))
}

// InsertEvents writes a batch idempotently (duplicates by (time, event_id) are skipped).
func InsertEvents(ctx context.Context, db DBTX, rows []EventRow) (int64, error) {
	n := len(rows)
	times := make([]time.Time, n)
	ids := make([]uuid.UUID, n)
	elems := make([]uuid.UUID, n)
	devs := make([]uuid.UUID, n)
	sources := make([]string, n)
	actorIDs := make([]string, n)
	actorNames := make([]string, n)
	clientTS := make([]*time.Time, n)
	payloads := make([]string, n)
	values := make([]*float64, n)
	for i, r := range rows {
		times[i], ids[i], elems[i], devs[i] = r.Time, r.EventID, r.ElementID, r.DeviceID
		sources[i], actorIDs[i], actorNames[i] = r.Source, r.ActorID, r.ActorName
		clientTS[i], payloads[i], values[i] = r.ClientTS, string(r.Payload), r.Value
	}
	tag, err := db.Exec(ctx, `INSERT INTO element_event (`+eventCols+`)
		SELECT * FROM unnest($1::timestamptz[], $2::uuid[], $3::uuid[], $4::uuid[], $5::text[], $6::text[], $7::text[],
			$8::timestamptz[], $9::jsonb[], $10::float8[])
		ON CONFLICT DO NOTHING`, times, ids, elems, devs, sources, actorIDs, actorNames, clientTS, payloads, values)
	return tag.RowsAffected(), mapErr(err)
}

// FieldBuckets aggregates an arbitrary numeric attribute of the stored
// messages (payload #> path) per time bucket. Numbers, booleans (1/0) and
// numeric strings count; anything else is skipped. It reads the raw
// hypertable, so it costs more than Buckets on long ranges.
func FieldBuckets(ctx context.Context, db DBTX, elementID uuid.UUID, from, to time.Time, step time.Duration, path []string) ([]Bucket, error) {
	return many[Bucket](db.Query(ctx, `SELECT time_bucket($4::interval, time) AS bucket,
			avg(v) AS avg, min(v) AS min, max(v) AS max, count(*)::bigint AS n
		FROM (
			SELECT time, CASE jsonb_typeof(payload #> $5::text[])
				WHEN 'number'  THEN (payload #>> $5::text[])::float8
				WHEN 'boolean' THEN CASE WHEN (payload #>> $5::text[])::boolean THEN 1 ELSE 0 END
				WHEN 'string'  THEN CASE WHEN (payload #>> $5::text[]) ~ '^\s*-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?\s*$'
					THEN (payload #>> $5::text[])::float8 END
				END AS v
			FROM element_event WHERE element_id = $1 AND time >= $2 AND time < $3
		) s
		WHERE v IS NOT NULL
		GROUP BY 1 ORDER BY 1`, elementID, from, to, step, path))
}
