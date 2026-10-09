// Package outbox relays committed outbox rows to Redpanda.
package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/taha2samy/quackquack/server/internal/bus"
	"github.com/taha2samy/quackquack/server/internal/db"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type Relay struct {
	Pool     *pgxpool.Pool
	Producer *bus.Producer
	Log      *slog.Logger
	// Published is called with the number of rows relayed (metrics hook).
	Published func(n int)
}

const batchSize = 200

// drainLockKey makes one relay drain at a time, so rows are published in id
// order even with several API replicas. device-config.v1 relies on this: the
// newest snapshot of a device must be the last record for its key.
const drainLockKey = 0x71756163_6b6f7574 // "quackout"

// Run drains the outbox whenever a NOTIFY arrives, and at least every second.
// Multiple relays may run concurrently; SKIP LOCKED keeps them from colliding.
func (r *Relay) Run(ctx context.Context) error {
	wake := make(chan struct{}, 1)
	go r.listen(ctx, wake)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	purge := time.NewTicker(time.Hour)
	defer purge.Stop()
	for {
		if err := r.drain(ctx); err != nil && ctx.Err() == nil {
			r.Log.Warn("outbox: drain failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		case <-tick.C:
		case <-purge.C:
			if n, err := store.PurgePublishedOutbox(ctx, r.Pool, 7*24*time.Hour); err == nil && n > 0 {
				r.Log.Info("outbox: purged published rows", "rows", n)
			}
		}
	}
}

func (r *Relay) drain(ctx context.Context) error {
	for {
		var n int
		err := db.InTx(ctx, r.Pool, func(tx pgx.Tx) error {
			var locked bool
			if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, int64(drainLockKey)).Scan(&locked); err != nil || !locked {
				return err // another relay is draining; it will publish these rows
			}
			rows, err := store.LockUnpublished(ctx, tx, batchSize)
			if err != nil || len(rows) == 0 {
				return err
			}
			recs := make([]*kgo.Record, len(rows))
			ids := make([]int64, len(rows))
			for i, row := range rows {
				recs[i] = bus.RawRecord(row.Topic, row.Key, row.Payload)
				ids[i] = row.ID
			}
			if err := r.Producer.PublishSync(ctx, recs...); err != nil {
				return err
			}
			n = len(rows)
			return store.MarkPublished(ctx, tx, ids)
		})
		if err != nil {
			return err
		}
		if r.Published != nil && n > 0 {
			r.Published(n)
		}
		if n < batchSize {
			return nil
		}
	}
}

func (r *Relay) listen(ctx context.Context, wake chan<- struct{}) {
	for ctx.Err() == nil {
		conn, err := r.Pool.Acquire(ctx)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}
		if _, err := conn.Exec(ctx, "LISTEN "+store.OutboxChannel); err != nil {
			conn.Release()
			time.Sleep(time.Second)
			continue
		}
		for {
			if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
				break
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		}
		// The connection is still LISTENing; take it out of the pool and close it.
		_ = conn.Hijack().Close(context.Background())
	}
}
