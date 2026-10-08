//go:build integration

package clickhouse_test

import (
	"context"
	"os"
	"testing"

	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/history/clickhouse"
	"github.com/taha2samy/quackquack/server/internal/history/historytest"
)

func testURL() string {
	if u := os.Getenv("QUACK_IT_CLICKHOUSE_URL"); u != "" {
		return u
	}
	return "clickhouse://default:@127.0.0.1:9000/quack_history_it"
}

// QUACK_IT_CLICKHOUSE_URL: a ClickHouse to run the contract against.
func TestContract(t *testing.T) {
	u := testURL()
	ctx := context.Background()
	st, err := clickhouse.Open(ctx, u, history.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Skipf("no ClickHouse at %s: %v", u, err)
	}
	if err := st.Migrate(ctx); err != nil { // idempotent
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
	st, err := clickhouse.Open(context.Background(), testURL(), history.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Skipf("no ClickHouse: %v", err)
	}
	return st
}
