package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/store"
)

type ElementAlertsListIn struct {
	ID uuid.UUID `path:"id"`
}

type ElementAlertsListOut struct {
	Body []store.ElementAlertRule
}

type ElementAlertSaveIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		RuleID       *uuid.UUID `json:"id,omitempty"`
		Name         string     `json:"name" doc:"Alert rule name" minLength:"1" maxLength:"100"`
		Condition    string     `json:"condition" doc:"Condition type" enum:"above,below,outside_range,equals"`
		Threshold    float64    `json:"threshold" doc:"Primary threshold"`
		ThresholdMax *float64   `json:"threshold_max,omitempty" doc:"Max threshold for outside_range"`
		Hysteresis   float64    `json:"hysteresis" doc:"Hysteresis band before clearing"`
		Severity     string     `json:"severity" doc:"Severity: info, warning, critical" enum:"info,warning,critical"`
		Message      string     `json:"message" doc:"Custom notification message"`
		Enabled      *bool      `json:"enabled,omitempty"`
	}
}

type ElementAlertOut struct {
	Body store.ElementAlertRule
}

type ElementAlertDeleteIn struct {
	ID     uuid.UUID `path:"id"`
	RuleID uuid.UUID `path:"ruleId"`
}

func (a *API) registerAdminAlerts(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-element-alerts",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/elements/{id}/alerts",
		Summary:     "List alert rules for an element",
		Tags:        []string{"Admin Elements"},
	}, func(ctx context.Context, input *ElementAlertsListIn) (*ElementAlertsListOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		rules, err := store.ListElementAlertRules(ctx, a.pool, input.ID)
		if err != nil {
			return nil, a.fail(err)
		}
		return &ElementAlertsListOut{Body: rules}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "save-element-alert",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/elements/{id}/alerts",
		Summary:     "Create or update an alert rule for an element",
		Tags:        []string{"Admin Elements"},
	}, func(ctx context.Context, input *ElementAlertSaveIn) (*ElementAlertOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}

		ruleID := uuid.New()
		if input.Body.RuleID != nil && *input.Body.RuleID != uuid.Nil {
			ruleID = *input.Body.RuleID
		}

		enabled := true
		if input.Body.Enabled != nil {
			enabled = *input.Body.Enabled
		}

		severity := input.Body.Severity
		if severity == "" {
			severity = "warning"
		}

		cond := input.Body.Condition
		if cond == "" {
			cond = "above"
		}

		rule, err := store.SaveElementAlertRule(ctx, a.pool, store.ElementAlertRule{
			ID:           ruleID,
			ElementID:    input.ID,
			Name:         input.Body.Name,
			Condition:    cond,
			Threshold:    input.Body.Threshold,
			ThresholdMax: input.Body.ThresholdMax,
			Hysteresis:   input.Body.Hysteresis,
			Severity:     severity,
			Message:      input.Body.Message,
			Enabled:      enabled,
		})
		if err != nil {
			return nil, a.fail(err)
		}
		return &ElementAlertOut{Body: rule}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-element-alert",
		Method:      http.MethodDelete,
		Path:        "/api/v1/admin/elements/{id}/alerts/{ruleId}",
		Summary:     "Delete an alert rule from an element",
		Tags:        []string{"Admin Elements"},
	}, func(ctx context.Context, input *ElementAlertDeleteIn) (*struct{}, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		if err := store.DeleteElementAlertRule(ctx, a.pool, input.RuleID); err != nil {
			return nil, a.fail(err)
		}
		return &struct{}{}, nil
	})
}
