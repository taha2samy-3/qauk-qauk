//go:build integration

package timescale_test

import (
	"context"
	"os"
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
