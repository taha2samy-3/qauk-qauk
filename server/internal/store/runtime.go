package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// --- Presence ---

func InsertPresence(ctx context.Context, db DBTX, deviceID uuid.UUID, gatewayID, connID string) error {
	_, err := db.Exec(ctx, `INSERT INTO device_presence (device_id, gateway_id, conn_id) VALUES ($1, $2, $3)
		ON CONFLICT (device_id, gateway_id, conn_id) DO UPDATE SET last_seen_at = now()`, deviceID, gatewayID, connID)
	return mapErr(err)
}

func DeletePresence(ctx context.Context, db DBTX, deviceID uuid.UUID, gatewayID, connID string) error {
	_, err := db.Exec(ctx, `DELETE FROM device_presence WHERE device_id = $1 AND gateway_id = $2 AND conn_id = $3`,
		deviceID, gatewayID, connID)
	return mapErr(err)
}

func HeartbeatPresence(ctx context.Context, db DBTX, gatewayID string) error {
	_, err := db.Exec(ctx, `UPDATE device_presence SET last_seen_at = now() WHERE gateway_id = $1`, gatewayID)
	return mapErr(err)
}

// DeviceConnected reports whether any live lease exists for the device.
func DeviceConnected(ctx context.Context, db DBTX, deviceID uuid.UUID, ttl time.Duration) (bool, error) {
	var ok bool
	err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM device_presence WHERE device_id = $1 AND last_seen_at > now() - $2::interval)`,
		deviceID, ttl).Scan(&ok)
	return ok, mapErr(err)
}

// SweepPresence deletes expired leases and returns devices that no longer have any live lease.
func SweepPresence(ctx context.Context, db DBTX, ttl time.Duration) ([]uuid.UUID, error) {
	rows, err := db.Query(ctx, `WITH gone AS (
			DELETE FROM device_presence WHERE last_seen_at <= now() - $1::interval RETURNING device_id)
		SELECT DISTINCT g.device_id FROM gone g
		WHERE NOT EXISTS (SELECT 1 FROM device_presence p WHERE p.device_id = g.device_id AND p.last_seen_at > now() - $1::interval)`, ttl)
	if err != nil {
		return nil, mapErr(err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// --- Connection audit log ---

func InsertConnection(ctx context.Context, db DBTX, c Connection) error {
	_, err := db.Exec(ctx, `INSERT INTO device_connections (id, device_id, gateway_id, details) VALUES ($1, $2, $3, $4)`,
		c.ID, c.DeviceID, c.GatewayID, c.Details)
	return mapErr(err)
}

func CloseConnection(ctx context.Context, db DBTX, id uuid.UUID) error {
	_, err := db.Exec(ctx, `UPDATE device_connections SET disconnected_at = now() WHERE id = $1 AND disconnected_at IS NULL`, id)
	return mapErr(err)
}

func ListConnections(ctx context.Context, db DBTX, deviceID *uuid.UUID, limit int) ([]Connection, error) {
	return many[Connection](db.Query(ctx, `SELECT id, device_id, gateway_id, details, connected_at, disconnected_at
		FROM device_connections WHERE ($1::uuid IS NULL OR device_id = $1) ORDER BY connected_at DESC LIMIT $2`, deviceID, limit))
}

type PresenceRow struct {
	DeviceID    uuid.UUID `db:"device_id" json:"device_id"`
	GatewayID   string    `db:"gateway_id" json:"gateway_id"`
	ConnID      string    `db:"conn_id" json:"conn_id"`
	ConnectedAt time.Time `db:"connected_at" json:"connected_at"`
	LastSeenAt  time.Time `db:"last_seen_at" json:"last_seen_at"`
}

func ListPresence(ctx context.Context, db DBTX) ([]PresenceRow, error) {
	return many[PresenceRow](db.Query(ctx, `SELECT device_id, gateway_id, conn_id, connected_at, last_seen_at FROM device_presence ORDER BY connected_at`))
}

// --- Outbox ---

const OutboxChannel = "quack_outbox"

type OutboxRow struct {
	ID      int64           `db:"id"`
	Topic   string          `db:"topic"`
	Key     string          `db:"key"`
	Payload json.RawMessage `db:"payload"`
}

// EnqueueOutbox must be called inside the transaction that made the change.
// The NOTIFY is delivered only if that transaction commits.
func EnqueueOutbox(ctx context.Context, tx DBTX, topic, key string, payload []byte) error {
	if _, err := tx.Exec(ctx, `INSERT INTO outbox (topic, key, payload) VALUES ($1, $2, $3)`, topic, key, payload); err != nil {
		return mapErr(err)
	}
	_, err := tx.Exec(ctx, `SELECT pg_notify($1, '')`, OutboxChannel)
	return mapErr(err)
}

func LockUnpublished(ctx context.Context, tx DBTX, limit int) ([]OutboxRow, error) {
	return many[OutboxRow](tx.Query(ctx, `SELECT id, topic, key, payload FROM outbox WHERE published_at IS NULL
		ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit))
}

func MarkPublished(ctx context.Context, tx DBTX, ids []int64) error {
	_, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = ANY($1)`, ids)
	return mapErr(err)
}

func PurgePublishedOutbox(ctx context.Context, db DBTX, olderThan time.Duration) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM outbox WHERE published_at < now() - $1::interval`, olderThan)
	return tag.RowsAffected(), mapErr(err)
}

func OutboxBacklog(ctx context.Context, db DBTX) (int64, error) {
	var n int64
	err := db.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&n)
	return n, mapErr(err)
}

// --- Audit ---

func InsertAudit(ctx context.Context, db DBTX, a AuditEntry) error {
	_, err := db.Exec(ctx, `INSERT INTO audit_log (actor_user_id, actor_name, action, entity, entity_id, data) VALUES ($1, $2, $3, $4, $5, $6)`,
		a.ActorUserID, a.ActorName, a.Action, a.Entity, a.EntityID, nullJSON(a.Data))
	return mapErr(err)
}

func ListAudit(ctx context.Context, db DBTX, limit int) ([]AuditEntry, error) {
	return many[AuditEntry](db.Query(ctx, `SELECT id, actor_user_id, actor_name, action, entity, entity_id, data, at
		FROM audit_log ORDER BY at DESC, id DESC LIMIT $1`, limit))
}
