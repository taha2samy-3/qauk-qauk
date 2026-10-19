package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// userPerms is a CTE: the highest grant of user $1 per element.
const userPerms = `perms AS (
	SELECT ep.element_id, max(ep.permission) AS permission
	FROM element_permissions ep JOIN users u ON u.id = $1 AND u.is_active
	WHERE ep.user_id = $1 OR ep.group_id IN (SELECT group_id FROM user_groups WHERE user_id = $1)
	GROUP BY ep.element_id)`

// VisibleElement is an element a user (or API token) may read, with its device.
type VisibleElement struct {
	ID          uuid.UUID       `db:"id" json:"id"`
	Name        string          `db:"name" json:"name"`
	Description string          `db:"description" json:"description"`
	Details     json.RawMessage `db:"details" json:"details" doc:"Free-form widget settings (unit, range…)"`
	DeviceID    uuid.UUID       `db:"device_id" json:"device_id"`
	DeviceName  string          `db:"device_name" json:"device_name"`
	Permission  string          `db:"permission" json:"permission" enum:"R,RC" doc:"R: read; RC: read and send commands"`
	Points      int             `db:"points" json:"-"` // the element's history window, for streams
}

func ListVisibleElements(ctx context.Context, db DBTX, userID int64) ([]VisibleElement, error) {
	return many[VisibleElement](db.Query(ctx, `WITH `+userPerms+`
		SELECT e.id, e.name, e.description, e.details, d.id AS device_id, d.name AS device_name, p.permission, e.points
		FROM perms p JOIN elements e ON e.id = p.element_id JOIN devices d ON d.id = e.device_id
		ORDER BY d.name, e.name, e.id`, userID))
}

// VisibleAlert is an alert rule of an element a user may read, with its state.
type VisibleAlert struct {
	ElementAlertRule
	ElementName string     `db:"element_name" json:"element_name"`
	DeviceID    uuid.UUID  `db:"device_id" json:"device_id"`
	DeviceName  string     `db:"device_name" json:"device_name"`
	Firing      bool       `db:"firing" json:"firing"`
	LastValue   *float64   `db:"last_value" json:"last_value,omitempty" doc:"The value of the last change of state"`
	ChangedAt   *time.Time `db:"changed_at" json:"changed_at,omitempty" doc:"When it last started firing or resolved"`
}

// ListVisibleAlerts lists the alert rules of the elements a user may read;
// firingOnly keeps the enabled ones that are firing.
func ListVisibleAlerts(ctx context.Context, db DBTX, userID int64, firingOnly bool) ([]VisibleAlert, error) {
	return many[VisibleAlert](db.Query(ctx, `WITH `+userPerms+`
		SELECT r.id, r.element_id, r.name, r.field, r.condition, r.threshold, r.threshold_max, r.hysteresis, r.severity,
			r.message, r.enabled, r.created_at, r.updated_at,
			e.name AS element_name, d.id AS device_id, d.name AS device_name,
			COALESCE(s.firing, false) AS firing, s.last_value, s.changed_at
		FROM perms p
		JOIN element_alert_rules r ON r.element_id = p.element_id
		JOIN elements e ON e.id = r.element_id JOIN devices d ON d.id = e.device_id
		LEFT JOIN element_alert_state s ON s.rule_id = r.id
		WHERE NOT $2 OR (r.enabled AND COALESCE(s.firing, false))
		ORDER BY s.changed_at DESC NULLS LAST, r.name`, userID, firingOnly))
}
