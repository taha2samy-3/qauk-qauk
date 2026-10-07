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

// QUACK_IT_CLICKHOUSE_URL: a ClickHouse to run the contract against.
func TestContract(t *testing.T) {
	u := os.Getenv("QUACK_IT_CLICKHOUSE_URL")
	if u == "" {
		u = "clickhouse://default:@127.0.0.1:9000/quack_history_it"
	}
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
