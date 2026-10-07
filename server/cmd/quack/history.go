package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/config"
	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/store"
)

const historyUsage = `quack history copy --to-driver <name> --to-url <url> [flags]

Copies stored history (events, and so points and rollups) from the configured
store (QUACK_HISTORY_DRIVER / QUACK_HISTORY_URL) into another one, element by
element. It is idempotent: interrupt and run it again at will.

To move to another backend without a gap:
  1. QUACK_HISTORY_DRIVER=timescale,clickhouse (+ QUACK_HISTORY_URL=,clickhouse://…)
     so new events go to both; restart quack serve / quack ingest
  2. quack history copy --to-driver clickhouse --to-url clickhouse://…
  3. QUACK_HISTORY_DRIVER=clickhouse; restart

Flags:
`

func historyCmd(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string) error {
	if len(args) == 0 || args[0] != "copy" {
		fmt.Print(historyUsage)
		return errors.New("usage: quack history copy …")
	}
	fs := newFlagSet("history copy")
	fs.Usage = func() { fmt.Print(historyUsage); fs.PrintDefaults() }
	toDriver := fs.String("to-driver", "", "target driver (e.g. clickhouse)")
	toURL := fs.String("to-url", "", "target URL")
	since := fs.String("since", "", "copy events from this time (RFC 3339; default: now - QUACK_HISTORY_RETENTION)")
	batch := fs.Int("batch", 5000, "events per read/append")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *toDriver == "" {
		return errors.New("--to-driver is required")
	}
	from := time.Now().Add(-cfg.HistoryRetention)
	if *since != "" {
		t, err := time.Parse(time.RFC3339, *since)
		if err != nil {
			return fmt.Errorf("--since: %w", err)
		}
		from = t
	}
	until := time.Now()

	pool, err := openPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	src, err := openHistory(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	settings := history.Settings{Retention: cfg.HistoryRetention, CompressAfter: cfg.HistoryCompressAfter}
	dst, err := history.Open(ctx, []history.Target{{Driver: *toDriver, URL: *toURL}}, settings)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()
	if err := dst.Migrate(ctx); err != nil {
		return fmt.Errorf("target migrate: %w", err)
	}

	els, err := store.ListElements(ctx, pool, nil)
	if err != nil {
		return err
	}
	start := time.Now()
	var total, added int
	for i, el := range els {
		n, a, err := copyElement(ctx, src, dst, el.ID, from, until, *batch)
		if err != nil {
			return fmt.Errorf("element %s (%s): %w", el.ID, el.Name, err)
		}
		total, added = total+n, added+a
		log.Info("history copy", "element", el.Name, "events", n, "new", a, "progress", fmt.Sprintf("%d/%d", i+1, len(els)))
	}
	log.Info("history copy done", "elements", len(els), "events", total, "new", added, "took", time.Since(start).Round(time.Millisecond))
	return nil
}

// copyElement walks one element's events in time order, batch by batch. The
// next batch starts at the last batch's newest millisecond (re-reading it is
// harmless: Append is idempotent); a batch stuck in one millisecond reads that
// whole millisecond, then moves past it.
func copyElement(ctx context.Context, src, dst history.Store, el uuid.UUID, from, until time.Time, batch int) (n, added int, err error) {
	cursor := from
	for cursor.Before(until) {
		evs, err := src.Events(ctx, history.EventQuery{ElementID: el, From: cursor, To: until, Limit: batch})
		if err != nil {
			return n, added, err
		}
		if len(evs) == 0 {
			break
		}
		next := evs[len(evs)-1].Time
		if len(evs) == batch && !next.After(cursor) {
			ms := cursor.Add(time.Millisecond)
			if evs, err = src.Events(ctx, history.EventQuery{ElementID: el, From: cursor, To: ms, Limit: math.MaxInt32}); err != nil {
				return n, added, err
			}
			next = ms
		}
		a, err := dst.Append(ctx, evs)
		if err != nil {
			return n, added, err
		}
		n, added = n+len(evs), added+a
		if len(evs) < batch && next.Equal(evs[len(evs)-1].Time) {
			break // the last batch
		}
		cursor = next
	}
	return n, added, nil
}
