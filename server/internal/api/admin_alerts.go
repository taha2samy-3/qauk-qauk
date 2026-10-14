package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type ElementAlertsListIn struct {
	ID uuid.UUID `path:"id"`
}

type ElementAlertsListOut struct {
	Body []service.AlertRuleView
}

type ElementAlertSaveIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		RuleID       *uuid.UUID `json:"id,omitempty" doc:"A rule of this element to change; omit to create one"`
		Name         string     `json:"name" doc:"Alert rule name" minLength:"1" maxLength:"100"`
		Condition    string     `json:"condition" doc:"Condition type" enum:"above,below,outside_range,equals"`
		Threshold    float64    `json:"threshold" doc:"Threshold (the low end for outside_range)"`
		ThresholdMax *float64   `json:"threshold_max,omitempty" doc:"High end for outside_range (required there, greater than threshold)"`
		Hysteresis   float64    `json:"hysteresis" minimum:"0" doc:"How far back past the threshold the value must go before the alert resolves"`
		Severity     string     `json:"severity,omitempty" doc:"Severity (default warning)" enum:"info,warning,critical"`
		Message      string     `json:"message,omitempty" maxLength:"500" doc:"Custom notification message"`
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
	tags := []string{"Admin Elements"}
	huma.Register(api, huma.Operation{OperationID: "list-element-alerts", Method: http.MethodGet, Path: "/api/v1/admin/elements/{id}/alerts",
		Summary: "List an element's alert rules, with whether each is firing", Tags: tags},
		func(ctx context.Context, in *ElementAlertsListIn) (*ElementAlertsListOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			rules, err := a.svc.ListAlertRules(ctx, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			return &ElementAlertsListOut{Body: rules}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "save-element-alert", Method: http.MethodPost, Path: "/api/v1/admin/elements/{id}/alerts",
		Summary: "Create or update an alert rule of an element", Tags: tags},
		func(ctx context.Context, in *ElementAlertSaveIn) (*ElementAlertOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			b := in.Body
			r, err := a.svc.SaveAlertRule(ctx, service.UserActor(admin), in.ID, service.AlertRuleInput{ID: b.RuleID, Name: b.Name,
				Condition: b.Condition, Threshold: b.Threshold, ThresholdMax: b.ThresholdMax, Hysteresis: b.Hysteresis,
				Severity: b.Severity, Message: b.Message, Enabled: b.Enabled})
			if err != nil {
				return nil, a.fail(err)
			}
			return &ElementAlertOut{Body: r}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-element-alert", Method: http.MethodDelete, Path: "/api/v1/admin/elements/{id}/alerts/{ruleId}",
		Summary: "Delete an alert rule of an element", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *ElementAlertDeleteIn) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteAlertRule(ctx, service.UserActor(admin), in.ID, in.RuleID))
		})
}
