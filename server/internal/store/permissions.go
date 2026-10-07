package store

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

const permCols = `id, element_id, user_id, group_id, permission`

type PermissionFilter struct {
	ElementID *uuid.UUID
	UserID    *int64
	GroupID   *int64
}

func ListPermissions(ctx context.Context, db DBTX, f PermissionFilter) ([]Permission, error) {
	return many[Permission](db.Query(ctx, `SELECT `+permCols+` FROM element_permissions
		WHERE ($1::uuid IS NULL OR element_id = $1) AND ($2::bigint IS NULL OR user_id = $2) AND ($3::bigint IS NULL OR group_id = $3)
		ORDER BY id`, f.ElementID, f.UserID, f.GroupID))
}

func GetPermission(ctx context.Context, db DBTX, id int64) (Permission, error) {
	return one[Permission](db.Query(ctx, `SELECT `+permCols+` FROM element_permissions WHERE id = $1`, id))
}

// UpsertPermission creates or updates a grant for exactly one of userID/groupID.
func UpsertPermission(ctx context.Context, db DBTX, p Permission) (Permission, bool, error) {
	conflict := `(element_id, user_id) WHERE user_id IS NOT NULL`
	if p.GroupID != nil {
		conflict = `(element_id, group_id) WHERE group_id IS NOT NULL`
	}
	type row struct {
		Permission
		Inserted bool `db:"inserted"`
	}
	r, err := one[row](db.Query(ctx, `INSERT INTO element_permissions (element_id, user_id, group_id, permission)
		VALUES ($1, $2, $3, $4) ON CONFLICT `+conflict+` DO UPDATE SET permission = EXCLUDED.permission
		RETURNING `+permCols+`, (xmax = 0) AS inserted`, p.ElementID, p.UserID, p.GroupID, p.Permission))
	return r.Permission, r.Inserted, err
}

func DeletePermission(ctx context.Context, db DBTX, id int64) error {
	return execOne(db.Exec(ctx, `DELETE FROM element_permissions WHERE id = $1`, id))
}

// MaxPermission ports PermissionManager.get_max_permission: the highest of the
// direct user grant and all group grants ("RC" > "R"). Returns "" if none, or
// if the user is inactive.
func MaxPermission(ctx context.Context, db DBTX, userID int64, elementID uuid.UUID) (string, error) {
	var perm *string
	err := db.QueryRow(ctx, `SELECT max(ep.permission) FROM element_permissions ep
		JOIN users u ON u.id = $1 AND u.is_active
		WHERE ep.element_id = $2
		  AND (ep.user_id = $1 OR ep.group_id IN (SELECT group_id FROM user_groups WHERE user_id = $1))`,
		userID, elementID).Scan(&perm)
	if err != nil {
		return "", mapErr(err)
	}
	if perm == nil {
		return "", nil
	}
	return *perm, nil
}

// UserElement is an element visible to a user, with its effective permission.
type UserElement struct {
	Element
	Permission string          `db:"permission" json:"permission"`
	Styles     json.RawMessage `db:"styles" json:"styles"`
}

func ListUserElements(ctx context.Context, db DBTX, userID int64) ([]UserElement, error) {
	return many[UserElement](db.Query(ctx, `WITH perms AS (
			SELECT ep.element_id, max(ep.permission) AS permission
			FROM element_permissions ep JOIN users u ON u.id = $1 AND u.is_active
			WHERE ep.user_id = $1 OR ep.group_id IN (SELECT group_id FROM user_groups WHERE user_id = $1)
			GROUP BY ep.element_id)
		SELECT e.id, e.device_id, e.name, e.points, e.description, e.details, e.created_at, p.permission,
			COALESCE((SELECT jsonb_agg(jsonb_build_object('id', s.id, 'name', s.name, 'details', s.details) ORDER BY s.id)
				FROM element_styles s WHERE s.element_id = e.id), '[]'::jsonb) AS styles
		FROM perms p JOIN elements e ON e.id = p.element_id
		ORDER BY e.created_at, e.id`, userID))
}
