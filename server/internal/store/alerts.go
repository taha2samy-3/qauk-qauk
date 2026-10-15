package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ElementAlertRule struct {
	ID           uuid.UUID `json:"id"`
	ElementID    uuid.UUID `json:"element_id"`
	Name         string    `json:"name"`
	Condition    string    `json:"condition"` // above, below, outside_range, equals
	Threshold    float64   `json:"threshold"`
	ThresholdMax *float64  `json:"threshold_max,omitempty"`
	Hysteresis   float64   `json:"hysteresis"`
	Severity     string    `json:"severity"` // info, warning, critical
	Message      string    `json:"message"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

const alertRuleCols = `id, element_id, name, condition, threshold, threshold_max, hysteresis, severity, message, enabled, created_at, updated_at`

func ListElementAlertRules(ctx context.Context, db DBTX, elementID uuid.UUID) ([]ElementAlertRule, error) {
	return many[ElementAlertRule](db.Query(ctx, `SELECT `+alertRuleCols+` FROM element_alert_rules WHERE element_id = $1 ORDER BY created_at ASC`, elementID))
}

func GetElementAlertRule(ctx context.Context, db DBTX, id uuid.UUID) (ElementAlertRule, error) {
	return one[ElementAlertRule](db.Query(ctx, `SELECT `+alertRuleCols+` FROM element_alert_rules WHERE id = $1`, id))
}

// SaveElementAlertRule creates or updates a rule of an element (an id that
// belongs to another element is not found).
func SaveElementAlertRule(ctx context.Context, db DBTX, r ElementAlertRule) (ElementAlertRule, error) {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	return one[ElementAlertRule](db.Query(ctx, `
		INSERT INTO element_alert_rules (id, element_id, name, condition, threshold, threshold_max, hysteresis, severity, message, enabled, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), now())
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			condition = EXCLUDED.condition,
			threshold = EXCLUDED.threshold,
			threshold_max = EXCLUDED.threshold_max,
			hysteresis = EXCLUDED.hysteresis,
			severity = EXCLUDED.severity,
			message = EXCLUDED.message,
			enabled = EXCLUDED.enabled,
			updated_at = now()
		WHERE element_alert_rules.element_id = EXCLUDED.element_id
		RETURNING `+alertRuleCols,
		r.ID, r.ElementID, r.Name, r.Condition, r.Threshold, r.ThresholdMax, r.Hysteresis, r.Severity, r.Message, r.Enabled,
	))
}

// DeleteElementAlertRule deletes one of an element's rules.
func DeleteElementAlertRule(ctx context.Context, db DBTX, elementID, id uuid.UUID) error {
	return execOne(db.Exec(ctx, `DELETE FROM element_alert_rules WHERE id = $1 AND element_id = $2`, id, elementID))
}

// ActiveAlertRule is an enabled rule with the names its notifications show.
type ActiveAlertRule struct {
	ElementAlertRule
	ElementName string `db:"element_name"`
	DeviceID    uuid.UUID
	DeviceName  string `db:"device_name"`
}

// ListActiveAlertRules lists enabled rules with their element and device names.
func ListActiveAlertRules(ctx context.Context, db DBTX) ([]ActiveAlertRule, error) {
	return many[ActiveAlertRule](db.Query(ctx, `
		SELECT r.id, r.element_id, r.name, r.condition, r.threshold, r.threshold_max, r.hysteresis, r.severity,
			r.message, r.enabled, r.created_at, r.updated_at,
			e.name AS element_name, d.id AS device_id, d.name AS device_name
		FROM element_alert_rules r
		JOIN elements e ON e.id = r.element_id
		JOIN devices d ON d.id = e.device_id
		WHERE r.enabled`))
}

// AlertFiring reports whether a rule is firing (false when never evaluated).
func AlertFiring(ctx context.Context, db DBTX, ruleID uuid.UUID) (bool, error) {
	var firing bool
	err := db.QueryRow(ctx, `SELECT firing FROM element_alert_state WHERE rule_id = $1`, ruleID).Scan(&firing)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return firing, mapErr(err)
}

// SetAlertFiring moves a rule from !firing to firing (or back) if it is
// still in the expected state, and reports whether it did. Run it in the
// transaction that enqueues the transition's deliveries.
func SetAlertFiring(ctx context.Context, db DBTX, ruleID uuid.UUID, firing bool, value float64) (bool, error) {
	tag, err := db.Exec(ctx, `
		INSERT INTO element_alert_state (rule_id, firing, last_value, changed_at) VALUES ($1, $2, $3, now())
		ON CONFLICT (rule_id) DO UPDATE SET firing = EXCLUDED.firing, last_value = EXCLUDED.last_value, changed_at = now()
		WHERE element_alert_state.firing <> EXCLUDED.firing`, ruleID, firing, value)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}
