package store

import (
	"context"
	"time"

	"github.com/google/uuid"
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

func SaveElementAlertRule(ctx context.Context, db DBTX, r ElementAlertRule) (ElementAlertRule, error) {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if r.Severity == "" {
		r.Severity = "warning"
	}
	if r.Condition == "" {
		r.Condition = "above"
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
		RETURNING `+alertRuleCols,
		r.ID, r.ElementID, r.Name, r.Condition, r.Threshold, r.ThresholdMax, r.Hysteresis, r.Severity, r.Message, r.Enabled,
	))
}

func DeleteElementAlertRule(ctx context.Context, db DBTX, id uuid.UUID) error {
	tag, err := db.Exec(ctx, `DELETE FROM element_alert_rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func ListActiveAlertRules(ctx context.Context, db DBTX) ([]ElementAlertRule, error) {
	return many[ElementAlertRule](db.Query(ctx, `SELECT `+alertRuleCols+` FROM element_alert_rules WHERE enabled = true`))
}
