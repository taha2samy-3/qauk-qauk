package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Dashboard struct {
	ID        uuid.UUID       `db:"id" json:"id"`
	OwnerID   int64           `db:"owner_id" json:"owner_id"`
	OwnerName string          `db:"owner_name" json:"owner_name"`
	Name      string          `db:"name" json:"name"`
	Shared    bool            `db:"shared" json:"shared"`
	Layout    json.RawMessage `db:"layout" json:"layout"`
	CreatedAt time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt time.Time       `db:"updated_at" json:"updated_at"`
}

const dashSelect = `SELECT d.id, d.owner_id, u.username AS owner_name, d.name, d.shared, d.layout, d.created_at, d.updated_at
	FROM dashboards d JOIN users u ON u.id = d.owner_id`

// ListDashboards returns the user's own dashboards plus dashboards shared by others.
func ListDashboards(ctx context.Context, db DBTX, userID int64) ([]Dashboard, error) {
	return many[Dashboard](db.Query(ctx, dashSelect+` WHERE d.owner_id = $1 OR d.shared ORDER BY d.owner_id <> $1, d.name, d.id`, userID))
}

// GetDashboard returns a dashboard visible to the user (owned or shared).
func GetDashboard(ctx context.Context, db DBTX, id uuid.UUID, userID int64) (Dashboard, error) {
	return one[Dashboard](db.Query(ctx, dashSelect+` WHERE d.id = $1 AND (d.owner_id = $2 OR d.shared)`, id, userID))
}

func InsertDashboard(ctx context.Context, db DBTX, d Dashboard) (Dashboard, error) {
	return one[Dashboard](db.Query(ctx, `WITH ins AS (
			INSERT INTO dashboards (id, owner_id, name, shared, layout) VALUES ($1, $2, $3, $4, $5) RETURNING *)
		SELECT ins.id, ins.owner_id, u.username AS owner_name, ins.name, ins.shared, ins.layout, ins.created_at, ins.updated_at
		FROM ins JOIN users u ON u.id = ins.owner_id`, d.ID, d.OwnerID, d.Name, d.Shared, d.Layout))
}

// UpdateDashboard only succeeds for the owner.
func UpdateDashboard(ctx context.Context, db DBTX, d Dashboard) (Dashboard, error) {
	return one[Dashboard](db.Query(ctx, `WITH up AS (
			UPDATE dashboards SET name = $3, shared = $4, layout = $5, updated_at = now()
			WHERE id = $1 AND owner_id = $2 RETURNING *)
		SELECT up.id, up.owner_id, u.username AS owner_name, up.name, up.shared, up.layout, up.created_at, up.updated_at
		FROM up JOIN users u ON u.id = up.owner_id`, d.ID, d.OwnerID, d.Name, d.Shared, d.Layout))
}

func DeleteDashboard(ctx context.Context, db DBTX, id uuid.UUID, ownerID int64) error {
	return execOne(db.Exec(ctx, `DELETE FROM dashboards WHERE id = $1 AND owner_id = $2`, id, ownerID))
}
