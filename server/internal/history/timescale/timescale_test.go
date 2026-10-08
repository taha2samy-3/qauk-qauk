//go:build integration

package timescale_test

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/history/historytest"
	"github.com/taha2samy/quackquack/server/internal/history/timescale"
)

// QUACK_IT_TIMESCALE_URL points at a database to (re)use for the contract;
// it is created if missing.
func testURL(t *testing.T) string {
	u := os.Getenv("QUACK_IT_TIMESCALE_URL")
	if u == "" {
		u = "postgres://quack:quack@127.0.0.1:5433/quack_history_it?sslmode=disable"
	}
	cfg, err := pgx.ParseConfig(u)
	if err != nil {
		t.Fatal(err)
	}
	admin := strings.Replace(u, "/"+cfg.Database+"?", "/postgres?", 1)
	c, err := pgx.Connect(context.Background(), admin)
	if err != nil {
		t.Skipf("no Postgres at %s: %v", admin, err)
	}
	defer func() { _ = c.Close(context.Background()) }()
	_, _ = c.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{cfg.Database}.Sanitize())
	return u
}

func TestContract(t *testing.T) {
	u := testURL(t)
	st, err := timescale.Open(context.Background(), u, history.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	// twice: migrate must be idempotent
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	historytest.Run(t, func(t *testing.T) history.Store { return st })
}

// QUACK_IT_BENCH=1 compares drivers: go test -tags integration -run Bench -v ./internal/history/...
func TestBench(t *testing.T) {
	if os.Getenv("QUACK_IT_BENCH") == "" {
		t.Skip("set QUACK_IT_BENCH=1")
	}
	st := openForBench(t)
	historytest.Bench(t, st, 20, 7200) // 20 elements × 2 h at 1 Hz = 144k events
}

func openForBench(t *testing.T) history.Store {
	st, err := timescale.Open(context.Background(), testURL(t), history.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

// Late batches appended concurrently refresh the rollup concurrently; that
// must not fail an Append (TimescaleDB answers 55P03 to the loser).
func TestConcurrentLateAppends(t *testing.T) {
	st := openForBench(t)
	ctx := context.Background()
	el := uuid.New()
	at := time.Now().UTC().Add(-6 * time.Hour).Truncate(time.Minute)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for w := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var evs []history.Event
			for i := range 50 {
				id, _ := uuid.NewV7()
				evs = append(evs, history.Event{Time: at.Add(time.Duration(w*50+i) * time.Second), ID: id, ElementID: el,
					DeviceID: el, Source: history.SourceDevice, ActorID: "d", ActorName: "d", Payload: []byte(`{"value": 1}`)})
			}
			if _, err := st.Append(ctx, evs); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent late append failed: %v", err)
	}
	bs, err := st.Buckets(ctx, history.BucketQuery{ElementID: el, Field: "value", From: at, To: at.Add(time.Hour), Step: time.Hour})
	if err != nil || len(bs) != 1 || bs[0].N != 800 {
		t.Fatalf("rollup after concurrent refreshes: %+v %v", bs, err)
	}
}
