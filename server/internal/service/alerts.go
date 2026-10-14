package service

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/store"
)

// AlertRuleInput creates (ID nil) or changes an element's alert rule.
type AlertRuleInput struct {
	ID           *uuid.UUID
	Name         string
	Condition    string
	Threshold    float64
	ThresholdMax *float64
	Hysteresis   float64
	Severity     string
	Message      string
	Enabled      *bool
}

var alertConditions = []string{"above", "below", "outside_range", "equals"}

func validateAlertRule(in AlertRuleInput) error {
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
	switch {
	case strings.TrimSpace(in.Name) == "" || len(in.Name) > 100:
		return invalid("name", "1-100 characters")
	case !slices.Contains(alertConditions, in.Condition):
		return invalid("condition", "one of above, below, outside_range, equals")
	case !slices.Contains(severities, in.Severity):
		return invalid("severity", "one of info, warning, critical")
	case !finite(in.Threshold):
		return invalid("threshold", "must be a finite number")
	case !finite(in.Hysteresis) || in.Hysteresis < 0:
		return invalid("hysteresis", "must be a number ≥ 0")
	case len(in.Message) > 500:
		return invalid("message", "at most 500 characters")
	}
	if in.Condition == "outside_range" {
		switch {
		case in.ThresholdMax == nil || !finite(*in.ThresholdMax):
			return invalid("threshold_max", "outside_range needs threshold_max")
		case *in.ThresholdMax <= in.Threshold:
			return invalid("threshold_max", "must be greater than threshold")
		case in.Hysteresis >= (*in.ThresholdMax-in.Threshold)/2:
			return invalid("hysteresis", "must be less than half the range, or the alert could never clear")
		}
	} else if in.ThresholdMax != nil {
		return invalid("threshold_max", "only for outside_range")
	}
	return nil
}

// AlertRuleView is a rule with whether it is firing now.
type AlertRuleView struct {
	store.ElementAlertRule
	Firing bool `json:"firing"`
}

func (s *Service) ListAlertRules(ctx context.Context, elementID uuid.UUID) ([]AlertRuleView, error) {
	if _, err := store.GetElement(ctx, s.Pool, elementID); err != nil {
		return nil, err
	}
	rules, err := store.ListElementAlertRules(ctx, s.Pool, elementID)
	if err != nil {
		return nil, err
	}
	out := make([]AlertRuleView, len(rules))
	for i, r := range rules {
		firing, err := store.AlertFiring(ctx, s.Pool, r.ID)
		if err != nil {
			return nil, err
		}
		out[i] = AlertRuleView{ElementAlertRule: r, Firing: firing}
	}
	return out, nil
}

func (s *Service) SaveAlertRule(ctx context.Context, a Actor, elementID uuid.UUID, in AlertRuleInput) (store.ElementAlertRule, error) {
	if in.Severity == "" {
		in.Severity = "warning"
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := validateAlertRule(in); err != nil {
		return store.ElementAlertRule{}, err
	}
	r := store.ElementAlertRule{ElementID: elementID, Name: in.Name, Condition: in.Condition, Threshold: in.Threshold,
		ThresholdMax: in.ThresholdMax, Hysteresis: in.Hysteresis, Severity: in.Severity, Message: in.Message, Enabled: true}
	action := "create"
	if in.ID != nil && *in.ID != uuid.Nil {
		r.ID, action = *in.ID, "update"
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	var out store.ElementAlertRule
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := store.GetElement(ctx, tx, elementID); err != nil {
			return err
		}
		var err error
		if out, err = store.SaveElementAlertRule(ctx, tx, r); err != nil {
			return err // a rule id of another element: not found
		}
		return record(ctx, tx, a, action, "alert_rule", out.ID.String(), out)
	})
	return out, err
}

func (s *Service) DeleteAlertRule(ctx context.Context, a Actor, elementID, ruleID uuid.UUID) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.DeleteElementAlertRule(ctx, tx, elementID, ruleID); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "alert_rule", ruleID.String(), map[string]string{"element_id": elementID.String()})
	})
}
