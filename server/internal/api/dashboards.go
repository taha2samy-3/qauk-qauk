package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/store"
)

const maxLayoutBytes = 256 << 10

type DashboardOut struct{ Body store.Dashboard }
type DashboardsOut struct{ Body []store.Dashboard }

type DashboardBody struct {
	Name   string          `json:"name" minLength:"1" maxLength:"100"`
	Shared bool            `json:"shared,omitempty" doc:"Visible (read-only) to every user; element permissions still apply per viewer"`
	Layout json.RawMessage `json:"layout,omitempty" doc:"Frontend-owned layout document, e.g. {\"widgets\":[...]}"`
}

type DashboardCreateIn struct{ Body DashboardBody }

type DashboardPutIn struct {
	ID   uuid.UUID `path:"id"`
	Body DashboardBody
}

func checkLayout(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`{"widgets":[]}`), nil
	}
	if len(raw) > maxLayoutBytes {
		return nil, huma.Error422UnprocessableEntity("layout too large (max 256 KiB)")
	}
	if t := bytes.TrimSpace(raw); len(t) == 0 || t[0] != '{' {
		return nil, huma.Error422UnprocessableEntity("layout must be a JSON object")
	}
	return raw, nil
}

// ownerOnly turns a failed owner-scoped write into 403 when the user can see
// the dashboard (shared by someone else), and 404 when they cannot.
func (a *API) ownerOnly(ctx context.Context, id uuid.UUID, userID int64, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		if _, gerr := store.GetDashboard(ctx, a.pool, id, userID); gerr == nil {
			return huma.Error403Forbidden("only the owner can change this dashboard")
		}
	}
	return a.fail(err)
}

func (a *API) registerDashboards(api huma.API) {
	tags := []string{"dashboards"}
	huma.Register(api, huma.Operation{OperationID: "list-dashboards", Method: http.MethodGet, Path: "/api/v1/dashboards", Tags: tags,
		Summary: "Your dashboards and dashboards shared by others"},
		func(ctx context.Context, _ *struct{}) (*DashboardsOut, error) {
			u, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			ds, err := store.ListDashboards(ctx, a.pool, u.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			return &DashboardsOut{Body: ds}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "create-dashboard", Method: http.MethodPost, Path: "/api/v1/dashboards", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *DashboardCreateIn) (*DashboardOut, error) {
			u, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			layout, err := checkLayout(in.Body.Layout)
			if err != nil {
				return nil, err
			}
			d, err := store.InsertDashboard(ctx, a.pool, store.Dashboard{ID: uuid.Must(uuid.NewV7()), OwnerID: u.ID,
				Name: strings.TrimSpace(in.Body.Name), Shared: in.Body.Shared, Layout: layout})
			if err != nil {
				return nil, a.fail(err)
			}
			return &DashboardOut{Body: d}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-dashboard", Method: http.MethodGet, Path: "/api/v1/dashboards/{id}", Tags: tags},
		func(ctx context.Context, in *UUIDPath) (*DashboardOut, error) {
			u, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			d, err := store.GetDashboard(ctx, a.pool, in.ID, u.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			return &DashboardOut{Body: d}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-dashboard", Method: http.MethodPut, Path: "/api/v1/dashboards/{id}", Tags: tags,
		Summary: "Replace name, sharing and layout (owner only)"},
		func(ctx context.Context, in *DashboardPutIn) (*DashboardOut, error) {
			u, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			layout, err := checkLayout(in.Body.Layout)
			if err != nil {
				return nil, err
			}
			d, err := store.UpdateDashboard(ctx, a.pool, store.Dashboard{ID: in.ID, OwnerID: u.ID,
				Name: strings.TrimSpace(in.Body.Name), Shared: in.Body.Shared, Layout: layout})
			if err != nil {
				return nil, a.ownerOnly(ctx, in.ID, u.ID, err)
			}
			return &DashboardOut{Body: d}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-dashboard", Method: http.MethodDelete, Path: "/api/v1/dashboards/{id}", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *UUIDPath) (*struct{}, error) {
			u, err := currentUser(ctx)
			if err != nil {
				return nil, err
			}
			if err := store.DeleteDashboard(ctx, a.pool, in.ID, u.ID); err != nil {
				return nil, a.ownerOnly(ctx, in.ID, u.ID, err)
			}
			return nil, nil
		})
}
