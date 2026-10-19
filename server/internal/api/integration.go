package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/ratelimit"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// The integration API: what a dashboard does, for API tokens (and sessions).
// See docs/04_api_reference/integration_api.md.

// LatestValue is an element's newest stored device message.
type LatestValue struct {
	Time    time.Time       `json:"time"`
	Message json.RawMessage `json:"message"`
	Value   *float64        `json:"value,omitempty" doc:"The element's numeric value (message.value, a chart's y, or a bare number), if it has one"`
}

// IntegrationElement is an element the caller may read.
type IntegrationElement struct {
	store.VisibleElement
	Latest *LatestValue `json:"latest,omitempty" doc:"With latest=true: the newest stored device message, if any"`
}

type IntegrationElementsIn struct {
	Latest bool `query:"latest" doc:"Include each element's newest stored device message"`
}

type IntegrationElementsOut struct{ Body []IntegrationElement }

type ElementIDPath struct {
	ID uuid.UUID `path:"id"`
}

type LatestOut struct {
	Body struct {
		ElementID uuid.UUID    `json:"element_id"`
		Latest    *LatestValue `json:"latest" doc:"null when the element has no stored device message yet"`
	}
}

type AlertsIn struct {
	State string `query:"state" enum:"firing,all" default:"firing" doc:"firing: the enabled rules firing now; all: every rule"`
}

type AlertsOut struct{ Body []store.VisibleAlert }

type CommandIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Message json.RawMessage `json:"message" doc:"The command, in the element's (engineering) units, e.g. {\"value\": 1}"`
	}
}

type CommandIDPath struct {
	ID uuid.UUID `path:"id"`
}

type CommandStatusOut struct{ Body store.Command }

type CommandOut struct {
	Body service.CommandAccepted
}

func (a *API) latest(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*LatestValue, error) {
	evs, err := a.hist.Last(ctx, ids, 1, time.Time{})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]*LatestValue, len(evs))
	for id, list := range evs {
		if len(list) > 0 {
			ev := list[len(list)-1]
			out[id] = &LatestValue{Time: ev.Time, Message: ev.Payload, Value: ev.Value}
		}
	}
	return out, nil
}

func (a *API) registerIntegration(api huma.API) {
	tags := []string{"integration"}

	huma.Register(api, huma.Operation{OperationID: "list-visible-elements", Method: http.MethodGet, Path: "/api/v1/elements", Tags: tags,
		Summary: "Elements the caller may read, with their device and, optionally, their newest value (API tokens: read scope)"},
		func(ctx context.Context, in *IntegrationElementsIn) (*IntegrationElementsOut, error) {
			u, err := caller(ctx, store.ScopeRead)
			if err != nil {
				return nil, err
			}
			els, err := store.ListVisibleElements(ctx, a.pool, u.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			out := make([]IntegrationElement, len(els))
			for i, e := range els {
				out[i] = IntegrationElement{VisibleElement: e}
			}
			if in.Latest && len(els) > 0 {
				ids := make([]uuid.UUID, len(els))
				for i, e := range els {
					ids[i] = e.ID
				}
				latest, err := a.latest(ctx, ids)
				if err != nil {
					return nil, a.fail(err)
				}
				for i := range out {
					out[i].Latest = latest[out[i].ID]
				}
			}
			return &IntegrationElementsOut{Body: out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "element-latest", Method: http.MethodGet, Path: "/api/v1/elements/{id}/latest", Tags: tags,
		Summary: "An element's newest stored device message (API tokens: read scope)"},
		func(ctx context.Context, in *ElementIDPath) (*LatestOut, error) {
			u, err := caller(ctx, store.ScopeRead)
			if err != nil {
				return nil, err
			}
			if perm, err := store.MaxPermission(ctx, a.pool, u.ID, in.ID); err != nil {
				return nil, a.fail(err)
			} else if perm == "" {
				return nil, huma.Error404NotFound("not found")
			}
			latest, err := a.latest(ctx, []uuid.UUID{in.ID})
			if err != nil {
				return nil, a.fail(err)
			}
			out := &LatestOut{}
			out.Body.ElementID, out.Body.Latest = in.ID, latest[in.ID]
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-alerts", Method: http.MethodGet, Path: "/api/v1/alerts", Tags: tags,
		Summary: "Alert rules of the elements the caller may read, with whether each is firing (API tokens: alerts scope)"},
		func(ctx context.Context, in *AlertsIn) (*AlertsOut, error) {
			u, err := caller(ctx, store.ScopeAlerts)
			if err != nil {
				return nil, err
			}
			alerts, err := store.ListVisibleAlerts(ctx, a.pool, u.ID, in.State != "all")
			if err != nil {
				return nil, a.fail(err)
			}
			return &AlertsOut{Body: alerts}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-command", Method: http.MethodGet, Path: "/api/v1/commands/{id}", Tags: tags,
		Summary: "What became of a command the caller sent: accepted, delivered, failed or superseded (API tokens: control scope)"},
		func(ctx context.Context, in *CommandIDPath) (*CommandStatusOut, error) {
			u, err := caller(ctx, store.ScopeControl)
			if err != nil {
				return nil, err
			}
			c, err := store.GetCommand(ctx, a.pool, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			if c.UserID == nil || *c.UserID != u.ID {
				return nil, huma.Error404NotFound("not found") // only its sender sees it
			}
			return &CommandStatusOut{Body: c}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "send-command", Method: http.MethodPost, Path: "/api/v1/elements/{id}/commands", Tags: tags,
		DefaultStatus: http.StatusAccepted,
		Summary:       "Send a command to an element's device, like a dashboard (needs RC; API tokens: control scope)"},
		func(ctx context.Context, in *CommandIn) (*CommandOut, error) {
			u, err := caller(ctx, store.ScopeControl)
			if err != nil {
				return nil, err
			}
			perm, err := store.MaxPermission(ctx, a.pool, u.ID, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			switch perm {
			case "":
				return nil, huma.Error404NotFound("not found")
			case store.PermReadWrite:
			default:
				return nil, huma.Error403Forbidden("sending commands needs the RC permission on the element")
			}
			rate := a.cfg.APICommandRate
			if ok, retry := a.commands.Allow(fmt.Sprint("cmd:", u.ID), ratelimit.Limit{Rate: rate, Burst: int(2 * rate)}, 1); !ok {
				return nil, huma.Error429TooManyRequests(fmt.Sprintf("too many commands, retry in %s", retry.Round(time.Millisecond)))
			}
			res, err := a.svc.SendCommand(ctx, callerActor(ctx, u), in.ID, in.Body.Message)
			if err != nil {
				return nil, a.fail(err)
			}
			return &CommandOut{Body: res}, nil
		})
}
