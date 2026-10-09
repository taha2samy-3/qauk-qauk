// Package store contains all SQL access. Functions take a DBTX so they can run
// on the pool or inside a transaction.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// mapErr converts driver errors into store errors.
func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505": // unique_violation
			return errors.Join(ErrConflict, err)
		case "23503": // foreign_key_violation
			return errors.Join(ErrNotFound, err)
		}
	}
	return err
}

func one[T any](rows pgx.Rows, err error) (T, error) {
	if err != nil {
		var zero T
		return zero, mapErr(err)
	}
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[T])
	return v, mapErr(err)
}

func many[T any](rows pgx.Rows, err error) ([]T, error) {
	if err != nil {
		return nil, mapErr(err)
	}
	v, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if v == nil {
		v = []T{}
	}
	return v, mapErr(err)
}

func execOne(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Models ------------------------------------------------------------------

type User struct {
	ID           int64      `db:"id" json:"id"`
	Username     string     `db:"username" json:"username"`
	Email        string     `db:"email" json:"email"`
	PasswordHash string     `db:"password_hash" json:"-"`
	IsActive     bool       `db:"is_active" json:"is_active"`
	IsAdmin      bool       `db:"is_admin" json:"is_admin"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
	LastLoginAt  *time.Time `db:"last_login_at" json:"last_login_at"`
}

type Group struct {
	ID   int64  `db:"id" json:"id"`
	Name string `db:"name" json:"name"`
}

type Key struct {
	ID        uuid.UUID `db:"id" json:"id"`
	Name      string    `db:"name" json:"name"`
	PEM       string    `db:"pem" json:"pem"`
	Algorithm string    `db:"algorithm" json:"algorithm"`
	KeySize   int       `db:"key_size" json:"key_size"`
	IsActive  bool      `db:"is_active" json:"is_active"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

type Device struct {
	ID          uuid.UUID  `db:"id" json:"id"`
	Name        string     `db:"name" json:"name"`
	Description string     `db:"description" json:"description"`
	PublicKeyID *uuid.UUID `db:"public_key_id" json:"public_key_id"`
}

type Element struct {
	ID          uuid.UUID       `db:"id" json:"id"`
	DeviceID    uuid.UUID       `db:"device_id" json:"device_id"`
	Name        string          `db:"name" json:"name"`
	Points      int             `db:"points" json:"points"`
	Description string          `db:"description" json:"description"`
	Details     json.RawMessage `db:"details" json:"details"`
	CreatedAt   time.Time       `db:"created_at" json:"created_at"`
	// Rate limit: nil means the server default (QUACK_ELEMENT_MSG_RATE).
	MsgRate   *float64 `db:"msg_rate" json:"msg_rate" doc:"Messages per second; null = server default"`
	MsgBurst  *int     `db:"msg_burst" json:"msg_burst" doc:"Bucket size; null = same as the rate"`
	OverLimit string   `db:"over_limit" json:"over_limit" enum:"drop,latest" doc:"drop: discard extra messages; latest: keep the newest and send it when the bucket refills"`
}

type Style struct {
	ID        int64           `db:"id" json:"id"`
	ElementID uuid.UUID       `db:"element_id" json:"element_id"`
	Name      string          `db:"name" json:"name"`
	Details   json.RawMessage `db:"details" json:"details"`
}

type Permission struct {
	ID         int64     `db:"id" json:"id"`
	ElementID  uuid.UUID `db:"element_id" json:"element_id"`
	UserID     *int64    `db:"user_id" json:"user_id"`
	GroupID    *int64    `db:"group_id" json:"group_id"`
	Permission string    `db:"permission" json:"permission"`
}

type Connection struct {
	ID             uuid.UUID       `db:"id" json:"id"`
	DeviceID       uuid.UUID       `db:"device_id" json:"device_id"`
	GatewayID      string          `db:"gateway_id" json:"gateway_id"`
	Details        json.RawMessage `db:"details" json:"details"`
	ConnectedAt    time.Time       `db:"connected_at" json:"connected_at"`
	DisconnectedAt *time.Time      `db:"disconnected_at" json:"disconnected_at"`
}

type AuditEntry struct {
	ID          int64           `db:"id" json:"id"`
	ActorUserID *int64          `db:"actor_user_id" json:"actor_user_id"`
	ActorName   string          `db:"actor_name" json:"actor_name"`
	Action      string          `db:"action" json:"action"`
	Entity      string          `db:"entity" json:"entity"`
	EntityID    string          `db:"entity_id" json:"entity_id"`
	Data        json.RawMessage `db:"data" json:"data"`
	At          time.Time       `db:"at" json:"at"`
}

const (
	PermRead      = "R"
	PermReadWrite = "RC"
)
