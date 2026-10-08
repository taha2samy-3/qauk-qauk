// Package timescale is the TimescaleDB history driver. It can share the main
// Postgres (the default) or use its own database: QUACK_HISTORY_URL.
// Its schema is versioned separately from the core schema (goose table
// goose_history_version).
package timescale

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/taha2samy/quackquack/server/internal/history"
)

//go:embed migrations/*.sql
var migrations embed.FS

func init() {
	history.Register("timescale", Open)
}

// lateAfter: events older than this when appended may be below the
// continuous aggregate's watermark, so their range is refreshed explicitly.
const lateAfter = 2 * time.Minute

type Store struct {
	pool *pgxpool.Pool
	url  string
	s    history.Settings
}

// Open connects to a Postgres URL with TimescaleDB available.
func Open(ctx context.Context, url string, s history.Settings) (history.Store, error) {
	if url == "" {
		return nil, errors.New("timescale: empty URL")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("timescale: parse url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool, url: url, s: s.WithDefaults()}, nil
}

func (st *Store) Close() error {
	st.pool.Close()
	return nil
}

// Migrate applies the history schema and the retention/compression settings.
func (st *Store) Migrate(ctx context.Context) error {
	cfg, err := pgx.ParseConfig(st.url)
	if err != nil {
		return err
	}
	db := stdlib.OpenDB(*cfg)
	defer func() { _ = db.Close() }()
	if err := migrate(ctx, db); err != nil {
		return err
	}
	// Policies are (re)applied on every migrate so QUACK_HISTORY_RETENTION and
	// QUACK_HISTORY_COMPRESS_AFTER take effect without a schema migration.
	for _, q := range []string{
		`SELECT remove_retention_policy('element_event', if_exists => true)`,
		`SELECT remove_retention_policy('element_point', if_exists => true)`,
		`SELECT remove_retention_policy('element_point_1m', if_exists => true)`,
		`SELECT remove_compression_policy('element_event', if_exists => true)`,
		`SELECT remove_compression_policy('element_point', if_exists => true)`,
	} {
		if _, err := st.pool.Exec(ctx, q); err != nil {
			return fmt.Errorf("timescale: %s: %w", q, err)
		}
	}
	keep, compress := pgInterval(st.s.Retention), pgInterval(st.s.CompressAfter)
	for _, q := range []struct{ sql, arg string }{
		{`SELECT add_retention_policy('element_event', $1::interval)`, keep},
		{`SELECT add_retention_policy('element_point', $1::interval)`, keep},
		{`SELECT add_retention_policy('element_point_1m', $1::interval)`, keep},
		{`SELECT add_compression_policy('element_event', $1::interval)`, compress},
		{`SELECT add_compression_policy('element_point', $1::interval)`, compress},
	} {
		if _, err := st.pool.Exec(ctx, q.sql, q.arg); err != nil {
			return fmt.Errorf("timescale: %s: %w", q.sql, err)
		}
	}
	return nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	p, err := goose.NewProvider(goose.DialectPostgres, db, mustSub(migrations, "migrations"),
		goose.WithTableName("goose_history_version"))
	if err != nil {
		return fmt.Errorf("timescale: goose: %w", err)
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("timescale: migrate: %w", err)
	}
	return nil
}

func pgInterval(d time.Duration) string { return fmt.Sprintf("%d seconds", int64(d/time.Second)) }

const eventCols = `time, event_id, element_id, device_id, source, actor_id, actor_name, client_ts, payload, value`

// Append inserts events and, for the ones that are new, their points, in one
// transaction. Duplicates (same time and event id) are skipped, so rollups
// never count an event twice.
func (st *Store) Append(ctx context.Context, evs []history.Event) (int, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	n := len(evs)
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
	byID := make(map[uuid.UUID]history.Event, n)
	for i, e := range evs {
		times[i], ids[i], elems[i], devs[i] = e.Time, e.ID, e.ElementID, e.DeviceID
		sources[i], actorIDs[i], actorNames[i] = e.Source, e.ActorID, e.ActorName
		clientTS[i], payloads[i], values[i] = e.ClientTS, string(e.Payload), e.Value
		byID[e.ID] = e
	}

	var inserted int
	var oldest, newest time.Time
	err := pgx.BeginFunc(ctx, st.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `INSERT INTO element_event (`+eventCols+`)
			SELECT * FROM unnest($1::timestamptz[], $2::uuid[], $3::uuid[], $4::uuid[], $5::text[], $6::text[], $7::text[],
				$8::timestamptz[], $9::jsonb[], $10::float8[])
			ON CONFLICT DO NOTHING
			RETURNING event_id`, times, ids, elems, devs, sources, actorIDs, actorNames, clientTS, payloads, values)
		if err != nil {
			return err
		}
		newIDs, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
		if err != nil {
			return err
		}
		inserted = len(newIDs)
		var pt []time.Time
		var pe, pid []uuid.UUID
		var pf []string
		var pv []float64
		for _, id := range newIDs {
			for _, p := range history.Points(byID[id]) {
				pt, pe, pf, pv, pid = append(pt, p.Time), append(pe, p.ElementID), append(pf, p.Field), append(pv, p.Value), append(pid, p.EventID)
				if oldest.IsZero() || p.Time.Before(oldest) {
					oldest = p.Time
				}
				if p.Time.After(newest) {
					newest = p.Time
				}
			}
		}
		if len(pt) == 0 {
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO element_point (time, element_id, field, value, event_id)
			SELECT * FROM unnest($1::timestamptz[], $2::uuid[], $3::text[], $4::float8[], $5::uuid[])
			ON CONFLICT DO NOTHING`, pt, pe, pf, pv, pid)
		return err
	})
	if err != nil {
		return 0, classify(err)
	}
	if !oldest.IsZero() && time.Since(oldest) > lateAfter {
		// Late data (catch-up after downtime, backfill) may sit below the
		// aggregate's watermark, where real-time aggregation doesn't look.
		st.refreshRollup(ctx, oldest.Truncate(time.Minute), newest.Truncate(time.Minute).Add(time.Minute))
	}
	return inserted, nil
}

// refreshRollup materializes [from, to) of the rollup, retrying while
// TimescaleDB's own policy job (or another ingester) refreshes concurrently
// (SQLSTATE 55P03). It never fails the Append: the events are committed, and
// a retried batch would find nothing new to refresh. If every attempt
// collides, the invalidation log still has the range, and the policy job
// materializes it within its window (3 days).
func (st *Store) refreshRollup(ctx context.Context, from, to time.Time) {
	for attempt := range 8 {
		_, err := st.pool.Exec(ctx, `CALL refresh_continuous_aggregate('element_point_1m', $1::timestamptz, $2::timestamptz)`, from, to)
		var pgErr *pgconn.PgError
		if err == nil || !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(50*(attempt+1)) * time.Millisecond):
		}
	}
}

// classify marks data errors (bad input, not an outage) so the ingester can
// isolate the offending event instead of retrying the batch forever.
func classify(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && len(pgErr.Code) == 5 && (pgErr.Code[:2] == "22" || pgErr.Code[:2] == "23") {
		return fmt.Errorf("%w: %v", history.ErrBadData, err)
	}
	return err
}

func scanEvents(rows pgx.Rows) ([]history.Event, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (history.Event, error) {
		var e history.Event
		var payload []byte
		err := r.Scan(&e.Time, &e.ID, &e.ElementID, &e.DeviceID, &e.Source, &e.ActorID, &e.ActorName, &e.ClientTS, &payload, &e.Value)
		e.Payload = payload
		e.Time = e.Time.UTC()
		if e.ClientTS != nil {
			t := e.ClientTS.UTC()
			e.ClientTS = &t
		}
		return e, err
	})
}

func (st *Store) Last(ctx context.Context, elementIDs []uuid.UUID, n int, since time.Time) (map[uuid.UUID][]history.Event, error) {
	out := map[uuid.UUID][]history.Event{}
	if len(elementIDs) == 0 || n <= 0 {
		return out, nil
	}
	rows, err := st.pool.Query(ctx, `SELECT e.* FROM unnest($1::uuid[]) AS el(id)
		CROSS JOIN LATERAL (
			SELECT `+eventCols+` FROM element_event
			WHERE element_id = el.id AND source = 'device' AND time >= $3
			ORDER BY time DESC, event_id DESC LIMIT $2) e`, elementIDs, n, since)
	if err != nil {
		return nil, err
	}
	evs, err := scanEvents(rows)
	if err != nil {
		return nil, err
	}
	for _, e := range evs {
		out[e.ElementID] = append(out[e.ElementID], e)
	}
	for id := range out {
		history.SortEvents(out[id])
	}
	return out, nil
}

func (st *Store) Events(ctx context.Context, q history.EventQuery) ([]history.Event, error) {
	order := "ASC"
	if q.Newest {
		order = "DESC"
	}
	rows, err := st.pool.Query(ctx, `SELECT `+eventCols+` FROM element_event
		WHERE element_id = $1 AND time >= $2 AND time < $3
		ORDER BY time `+order+`, event_id `+order+` LIMIT $4`, q.ElementID, q.From, q.To, q.Limit)
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
	var rows pgx.Rows
	var err error
	if q.Step >= time.Minute && q.Step%time.Minute == 0 {
		rows, err = st.pool.Query(ctx, `SELECT time_bucket($5::interval, bucket) AS t,
				sum(sum) / NULLIF(sum(n), 0), min(min), max(max), sum(n)::bigint
			FROM element_point_1m
			WHERE element_id = $1 AND field = $2 AND bucket >= $3 AND bucket < $4
			GROUP BY 1 ORDER BY 1`, q.ElementID, q.Field, q.From, q.To, pgInterval(q.Step))
	} else {
		rows, err = st.pool.Query(ctx, `SELECT time_bucket($5::interval, time) AS t,
				avg(value), min(value), max(value), count(*)::bigint
			FROM element_point
			WHERE element_id = $1 AND field = $2 AND time >= $3 AND time < $4
			GROUP BY 1 ORDER BY 1`, q.ElementID, q.Field, q.From, q.To, pgInterval(q.Step))
	}
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (history.Bucket, error) {
		var b history.Bucket
		err := r.Scan(&b.Time, &b.Avg, &b.Min, &b.Max, &b.N)
		b.Time = b.Time.UTC()
		return b, err
	})
}

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

func (st *Store) Reset(ctx context.Context) error {
	_, err := st.pool.Exec(ctx, `TRUNCATE element_event, element_point`)
	if err == nil {
		// drop the rollup's materialized rows too
		_, err = st.pool.Exec(ctx, `CALL refresh_continuous_aggregate('element_point_1m', NULL, NULL)`)
	}
	return err
}
