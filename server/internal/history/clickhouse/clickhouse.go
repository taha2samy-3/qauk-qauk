// Package clickhouse is the ClickHouse history driver.
//
// URL: clickhouse://user:password@host:9000/database (native protocol). The
// database is created by Migrate.
//
// Tables (ORDER BY element first, so per-element reads touch few granules):
//   - element_event: the raw messages, ReplacingMergeTree (duplicates by event
//     id collapse on merge; reads use FINAL)
//   - element_point: one row per numeric attribute (history.Points)
//   - element_point_1m: 1-minute rollup per element and attribute, filled by a
//     materialized view on element_point
//
// The materialized view counts every inserted row, so Append inserts only
// events that aren't stored yet (it checks first); that keeps rollups exact
// under at-least-once delivery.
package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ch "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/history"
)

func init() {
	history.Register("clickhouse", Open)
}

type Store struct {
	conn driver.Conn
	opts *ch.Options
	db   string
	s    history.Settings
}

// Open connects using a clickhouse:// URL.
func Open(ctx context.Context, url string, s history.Settings) (history.Store, error) {
	opts, err := ch.ParseDSN(url)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: parse url: %w", err)
	}
	db := opts.Auth.Database
	if db == "" || db == "default" {
		return nil, errors.New("clickhouse: put a dedicated database in the URL, e.g. clickhouse://host:9000/quack")
	}
	conn, err := ch.Open(opts)
	if err != nil {
		return nil, err
	}
	return &Store{conn: conn, opts: opts, db: db, s: s.WithDefaults()}, nil
}

func (st *Store) Close() error { return st.conn.Close() }

func ident(s string) string { return "`" + strings.ReplaceAll(s, "`", "``") + "`" }

// Migrate creates the database, tables and rollup, and applies retention.
func (st *Store) Migrate(ctx context.Context) error {
	admin := *st.opts
	admin.Auth.Database = "default"
	ac, err := ch.Open(&admin)
	if err != nil {
		return err
	}
	defer func() { _ = ac.Close() }()
	if err := ac.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+ident(st.db)); err != nil {
		return fmt.Errorf("clickhouse: create database: %w", err)
	}

	keep := int64(st.s.Retention / time.Second)
	tables := []struct{ name, ttlExpr, ddl string }{
		{"element_event", "toDateTime(time)", `CREATE TABLE IF NOT EXISTS element_event (
			time       DateTime64(3, 'UTC') CODEC(Delta, ZSTD),
			event_id   UUID,
			element_id UUID,
			device_id  UUID,
			source     LowCardinality(String),
			actor_id   String,
			actor_name String,
			client_ts  Nullable(DateTime64(3, 'UTC')),
			payload    String CODEC(ZSTD(3)),
			value      Nullable(Float64)
		) ENGINE = ReplacingMergeTree
		PARTITION BY toYYYYMM(time)
		ORDER BY (element_id, time, event_id)`},
		{"element_point", "toDateTime(time)", `CREATE TABLE IF NOT EXISTS element_point (
			time       DateTime64(3, 'UTC') CODEC(Delta, ZSTD),
			element_id UUID,
			field      LowCardinality(String),
			value      Float64 CODEC(Gorilla, ZSTD),
			event_id   UUID
		) ENGINE = ReplacingMergeTree
		PARTITION BY toYYYYMM(time)
		ORDER BY (element_id, field, time, event_id)`},
		{"element_point_1m", "bucket", `CREATE TABLE IF NOT EXISTS element_point_1m (
			bucket     DateTime('UTC'),
			element_id UUID,
			field      LowCardinality(String),
			sum        SimpleAggregateFunction(sum, Float64),
			min        SimpleAggregateFunction(min, Float64),
			max        SimpleAggregateFunction(max, Float64),
			n          SimpleAggregateFunction(sum, UInt64)
		) ENGINE = AggregatingMergeTree
		PARTITION BY toYYYYMM(bucket)
		ORDER BY (element_id, field, bucket)`},
	}
	for _, t := range tables {
		if err := st.conn.Exec(ctx, fmt.Sprintf("%s TTL %s + toIntervalSecond(%d)", t.ddl, t.ttlExpr, keep)); err != nil {
			return fmt.Errorf("clickhouse: create %s: %w", t.name, err)
		}
		// Apply a changed QUACK_HISTORY_RETENTION; skipped when unchanged,
		// since MODIFY TTL rewrites old parts.
		var create string
		if err := st.conn.QueryRow(ctx, "SELECT create_table_query FROM system.tables WHERE database = ? AND name = ?", st.db, t.name).Scan(&create); err != nil {
			return fmt.Errorf("clickhouse: inspect %s: %w", t.name, err)
		}
		if !strings.Contains(create, fmt.Sprintf("toIntervalSecond(%d)", keep)) {
			if err := st.conn.Exec(ctx, fmt.Sprintf("ALTER TABLE %s MODIFY TTL %s + toIntervalSecond(%d)", t.name, t.ttlExpr, keep)); err != nil {
				return fmt.Errorf("clickhouse: retention on %s: %w", t.name, err)
			}
		}
	}
	if err := st.conn.Exec(ctx, `CREATE MATERIALIZED VIEW IF NOT EXISTS element_point_1m_mv TO element_point_1m AS
		SELECT toStartOfMinute(time) AS bucket, element_id, field,
			sum(value) AS sum, min(value) AS min, max(value) AS max, count() AS n
		FROM element_point
		GROUP BY bucket, element_id, field`); err != nil {
		return fmt.Errorf("clickhouse: create rollup view: %w", err)
	}
	return nil
}

func ms(t time.Time) int64 { return t.UnixMilli() }

// in renders "(?, ?, …)" and appends the values as strings (ClickHouse casts
// string literals to UUID).
func in(args []any, ids []uuid.UUID) (string, []any) {
	ph := make([]string, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args = append(args, id.String())
	}
	return "(" + strings.Join(ph, ", ") + ")", args
}

// existing returns which of the candidate events already have rows in table.
func (st *Store) existing(ctx context.Context, table string, evs []history.Event) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(evs) == 0 {
		return out, nil
	}
	elems := map[uuid.UUID]struct{}{}
	ids := make([]uuid.UUID, 0, len(evs))
	lo, hi := evs[0].Time, evs[0].Time
	for _, e := range evs {
		elems[e.ElementID] = struct{}{}
		ids = append(ids, e.ID)
		if e.Time.Before(lo) {
			lo = e.Time
		}
		if e.Time.After(hi) {
			hi = e.Time
		}
	}
	elemIDs := make([]uuid.UUID, 0, len(elems))
	for id := range elems {
		elemIDs = append(elemIDs, id)
	}
	args := []any{}
	elIn, args := in(args, elemIDs)
	args = append(args, ms(lo), ms(hi))
	idIn, args := in(args, ids)
	rows, err := st.conn.Query(ctx, `SELECT DISTINCT event_id FROM `+table+`
		WHERE element_id IN `+elIn+`
		  AND time >= fromUnixTimestamp64Milli(?, 'UTC') AND time <= fromUnixTimestamp64Milli(?, 'UTC')
		  AND event_id IN `+idIn, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Append stores the events that are new: points first, then the events, so a
// crash in between is repaired by the redelivery (the events are still
// "new", and points already written are not written twice).
func (st *Store) Append(ctx context.Context, evs []history.Event) (int, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	// a batch may itself repeat an event
	seen := make(map[uuid.UUID]bool, len(evs))
	uniq := evs[:0:0]
	for _, e := range evs {
		if !seen[e.ID] {
			seen[e.ID] = true
			uniq = append(uniq, e)
		}
	}
	stored, err := st.existing(ctx, "element_event", uniq)
	if err != nil {
		return 0, err
	}
	fresh := uniq[:0:0]
	for _, e := range uniq {
		if !stored[e.ID] {
			fresh = append(fresh, e)
		}
	}
	if len(fresh) == 0 {
		return 0, nil
	}
	pointed, err := st.existing(ctx, "element_point", fresh)
	if err != nil {
		return 0, err
	}

	pb, err := st.conn.PrepareBatch(ctx, "INSERT INTO element_point (time, element_id, field, value, event_id)")
	if err != nil {
		return 0, err
	}
	npoints := 0
	for _, e := range fresh {
		if pointed[e.ID] {
			continue
		}
		for _, p := range history.Points(e) {
			if err := pb.Append(p.Time, p.ElementID, p.Field, p.Value, p.EventID); err != nil {
				_ = pb.Abort()
				return 0, classify(err)
			}
			npoints++
		}
	}
	if npoints > 0 {
		if err := pb.Send(); err != nil {
			return 0, classify(err)
		}
	} else {
		_ = pb.Abort()
	}

	eb, err := st.conn.PrepareBatch(ctx, `INSERT INTO element_event
		(time, event_id, element_id, device_id, source, actor_id, actor_name, client_ts, payload, value)`)
	if err != nil {
		return 0, err
	}
	for _, e := range fresh {
		if err := eb.Append(e.Time, e.ID, e.ElementID, e.DeviceID, e.Source, e.ActorID, e.ActorName,
			e.ClientTS, string(e.Payload), e.Value); err != nil {
			_ = eb.Abort()
			return 0, classify(err)
		}
	}
	if err := eb.Send(); err != nil {
		return 0, classify(err)
	}
	return len(fresh), nil
}

// classify marks errors caused by the data itself.
func classify(err error) error {
	var ex *ch.Exception
	if errors.As(err, &ex) {
		switch ex.Code {
		case 6, 27, 38, 41, 53, 69, 70, 72, 117: // CANNOT_PARSE_*, TYPE_MISMATCH, CANNOT_CONVERT_TYPE, INCORRECT_DATA, …
			return fmt.Errorf("%w: %v", history.ErrBadData, err)
		}
	}
	return err
}

const eventCols = `time, event_id, element_id, device_id, source, actor_id, actor_name, client_ts, payload, value`

func scanEvents(rows driver.Rows) ([]history.Event, error) {
	defer func() { _ = rows.Close() }()
	var out []history.Event
	for rows.Next() {
		var e history.Event
		var payload string
		if err := rows.Scan(&e.Time, &e.ID, &e.ElementID, &e.DeviceID, &e.Source, &e.ActorID, &e.ActorName,
			&e.ClientTS, &payload, &e.Value); err != nil {
			return nil, err
		}
		e.Time = e.Time.UTC()
		if e.ClientTS != nil {
			t := e.ClientTS.UTC()
			e.ClientTS = &t
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Last reads the newest device events of many elements. It looks back in
// widening windows (1 h, 1 day, then since) and only widens for elements that
// still have fewer than n, so busy elements cost a few granules instead of a
// scan of the whole replay window. ORDER BY is the reversed sorting key, so
// ClickHouse reads in order and stops early; without FINAL, duplicates (rare:
// Append inserts only new events) are dropped here.
func (st *Store) Last(ctx context.Context, elementIDs []uuid.UUID, n int, since time.Time) (map[uuid.UUID][]history.Event, error) {
	out := map[uuid.UUID][]history.Event{}
	if len(elementIDs) == 0 || n <= 0 {
		return out, nil
	}
	now := time.Now()
	pending := elementIDs
	for _, back := range []time.Duration{time.Hour, 24 * time.Hour, 0} {
		lo := since
		if back > 0 {
			if lo = now.Add(-back); lo.Before(since) {
				lo = since
			}
		}
		elIn, args := in(nil, pending)
		args = append(args, ms(lo), n)
		rows, err := st.conn.Query(ctx, `SELECT `+eventCols+` FROM element_event
			WHERE element_id IN `+elIn+` AND source = 'device' AND time >= fromUnixTimestamp64Milli(?, 'UTC')
			ORDER BY element_id DESC, time DESC, event_id DESC
			LIMIT ? BY element_id`, args...)
		if err != nil {
			return nil, err
		}
		evs, err := scanEvents(rows)
		if err != nil {
			return nil, err
		}
		got := map[uuid.UUID][]history.Event{}
		for _, e := range evs {
			got[e.ElementID] = append(got[e.ElementID], e)
		}
		var next []uuid.UUID
		for _, id := range pending {
			es := dedupe(got[id])
			if len(es) >= n || lo.Equal(since) {
				if len(es) > 0 {
					history.SortEvents(es)
					if over := len(es) - n; over > 0 {
						es = es[over:]
					}
					out[id] = es
				}
				continue
			}
			next = append(next, id)
		}
		if pending = next; len(pending) == 0 || lo.Equal(since) {
			break
		}
	}
	return out, nil
}

func dedupe(evs []history.Event) []history.Event {
	seen := make(map[uuid.UUID]bool, len(evs))
	out := evs[:0]
	for _, e := range evs {
		if !seen[e.ID] {
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	return out
}

func (st *Store) Events(ctx context.Context, q history.EventQuery) ([]history.Event, error) {
	order := "ASC"
	if q.Newest {
		order = "DESC"
	}
	rows, err := st.conn.Query(ctx, `SELECT `+eventCols+` FROM element_event FINAL
		WHERE element_id = ? AND time >= fromUnixTimestamp64Milli(?, 'UTC') AND time < fromUnixTimestamp64Milli(?, 'UTC')
		ORDER BY time `+order+`, toString(event_id) `+order+`
		LIMIT ?`, q.ElementID.String(), ms(q.From), ms(q.To), q.Limit)
	if err != nil {
		return nil, err
	}
	evs, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	history.SortEvents(evs)
	return evs, nil
}

func (st *Store) Buckets(ctx context.Context, q history.BucketQuery) ([]history.Bucket, error) {
	step := int64(q.Step / time.Second)
	if step <= 0 {
		return nil, errors.New("clickhouse: step must be at least one second")
	}
	var query string
	if q.Step >= time.Minute && q.Step%time.Minute == 0 {
		query = `SELECT toDateTime64(toStartOfInterval(bucket, toIntervalSecond(?)), 3, 'UTC') AS t,
				sum(sum) / sum(n), min(min), max(max), sum(n)
			FROM element_point_1m
			WHERE element_id = ? AND field = ?
			  AND bucket >= fromUnixTimestamp64Milli(?, 'UTC') AND bucket < fromUnixTimestamp64Milli(?, 'UTC')
			GROUP BY t ORDER BY t`
	} else {
		query = `SELECT toDateTime64(toStartOfInterval(time, toIntervalSecond(?)), 3, 'UTC') AS t,
				avg(value), min(value), max(value), toUInt64(count())
			FROM element_point FINAL
			WHERE element_id = ? AND field = ?
			  AND time >= fromUnixTimestamp64Milli(?, 'UTC') AND time < fromUnixTimestamp64Milli(?, 'UTC')
			GROUP BY t ORDER BY t`
	}
	rows, err := st.conn.Query(ctx, query, step, q.ElementID.String(), q.Field, ms(q.From), ms(q.To))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []history.Bucket
	for rows.Next() {
		var t time.Time
		var avg, lo, hi float64
		var n uint64
		if err := rows.Scan(&t, &avg, &lo, &hi, &n); err != nil {
			return nil, err
		}
		out = append(out, history.Bucket{Time: t.UTC(), Avg: &avg, Min: &lo, Max: &hi, N: int64(n)})
	}
	return out, rows.Err()
}

func (st *Store) Reset(ctx context.Context) error {
	for _, t := range []string{"element_event", "element_point", "element_point_1m"} {
		if err := st.conn.Exec(ctx, "TRUNCATE TABLE IF EXISTS "+t); err != nil {
			return err
		}
	}
	return nil
}
