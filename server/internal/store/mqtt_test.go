package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/taha2samy/quackquack/server/internal/store"
)

type mockRow struct {
	val any
	err error
}

func (r *mockRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) > 0 && r.val != nil {
		if ptr, ok := dest[0].(*string); ok {
			*ptr = r.val.(string)
		}
	}
	return nil
}

type mockDB struct {
	executedQueries []string
	queryRowErr     error
	execErr         error
}

func (m *mockDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	m.executedQueries = append(m.executedQueries, strings.TrimSpace(sql))
	if m.execErr != nil {
		return pgconn.CommandTag{}, m.execErr
	}
	return pgconn.NewCommandTag("DELETE 1"), nil
}

func (m *mockDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, nil
}

func (m *mockDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	m.executedQueries = append(m.executedQueries, strings.TrimSpace(sql))
	return &mockRow{val: "conn-1", err: m.queryRowErr}
}

func TestUpsertMQTTGrantSequence(t *testing.T) {
	ctx := context.Background()
	mock := &mockDB{}
	devID := uuid.New()

	err := store.UpsertMQTTGrant(ctx, mock, "conn-1", devID, "sensor-ext-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.executedQueries) != 3 {
		t.Fatalf("expected 3 executed queries (FOR UPDATE, DELETE, INSERT), got %d: %v", len(mock.executedQueries), mock.executedQueries)
	}

	// 1. Must lock connection row FOR UPDATE
	if !strings.Contains(mock.executedQueries[0], "FOR UPDATE") {
		t.Errorf("query 0 must lock connection FOR UPDATE, got: %s", mock.executedQueries[0])
	}

	// 2. Must delete colliding device_id or external_id
	if !strings.Contains(mock.executedQueries[1], "DELETE FROM mqtt_connection_devices") ||
		!strings.Contains(mock.executedQueries[1], "device_id = $2 OR external_id = $3") {
		t.Errorf("query 1 must delete colliding device_id or external_id, got: %s", mock.executedQueries[1])
	}

	// 3. Must insert with ON CONFLICT
	if !strings.Contains(mock.executedQueries[2], "INSERT INTO mqtt_connection_devices") ||
		!strings.Contains(mock.executedQueries[2], "ON CONFLICT") {
		t.Errorf("query 2 must insert with ON CONFLICT, got: %s", mock.executedQueries[2])
	}
}

func TestUpsertMQTTGrantConnectionNotFound(t *testing.T) {
	ctx := context.Background()
	mock := &mockDB{queryRowErr: pgx.ErrNoRows}
	devID := uuid.New()

	err := store.UpsertMQTTGrant(ctx, mock, "conn-nonexistent", devID, "sensor-ext-1")
	if err == nil || err != store.ErrNotFound {
		t.Fatalf("expected store.ErrNotFound, got: %v", err)
	}
}
