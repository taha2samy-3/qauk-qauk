package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type PermissionOut struct{ Body store.Permission }
type PermissionsOut struct{ Body []store.Permission }

type PermissionListIn struct {
	ElementID string `query:"element_id"`
	UserID    int64  `query:"user_id"`
	GroupID   int64  `query:"group_id"`
}

type PermissionSetIn struct {
	Body struct {
		ElementID  uuid.UUID `json:"element_id"`
		UserID     *int64    `json:"user_id,omitempty" doc:"Exactly one of user_id / group_id"`
		GroupID    *int64    `json:"group_id,omitempty"`
		Permission string    `json:"permission" enum:"R,RC" doc:"R = read, RC = read and control"`
	}
}

type ConnectionsIn struct {
	DeviceID string `query:"device_id"`
	Limit    int    `query:"limit" minimum:"1" maximum:"1000" default:"100"`
}

type ConnectionsOut struct{ Body []store.Connection }
type PresenceOut struct{ Body []store.PresenceRow }

type AuditIn struct {
	Limit int `query:"limit" minimum:"1" maximum:"1000" default:"100"`
}

type AuditOut struct{ Body []store.AuditEntry }

func (a *API) registerAdminOps(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "list-permissions", Method: http.MethodGet, Path: "/api/v1/admin/permissions", Tags: adminTags("permissions")},
		func(ctx context.Context, in *PermissionListIn) (*PermissionsOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			var f store.PermissionFilter
			if in.ElementID != "" {
				id, err := uuid.Parse(in.ElementID)
				if err != nil {
					return nil, huma.Error422UnprocessableEntity("element_id must be a UUID")
				}
				f.ElementID = &id
			}
			if in.UserID != 0 {
				f.UserID = &in.UserID
			}
			if in.GroupID != 0 {
				f.GroupID = &in.GroupID
			}
			ps, err := store.ListPermissions(ctx, a.pool, f)
			if err != nil {
				return nil, a.fail(err)
			}
			return &PermissionsOut{Body: ps}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "set-permission", Method: http.MethodPut, Path: "/api/v1/admin/permissions", Tags: adminTags("permissions"),
		Summary: "Create or update a grant (idempotent)"},
		func(ctx context.Context, in *PermissionSetIn) (*PermissionOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			b := in.Body
			p, err := a.svc.SetPermission(ctx, service.UserActor(admin), service.PermissionInput{ElementID: b.ElementID,
				UserID: b.UserID, GroupID: b.GroupID, Permission: b.Permission})
			if err != nil {
				return nil, a.fail(err)
			}
			return &PermissionOut{Body: p}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-permission", Method: http.MethodDelete, Path: "/api/v1/admin/permissions/{id}", Tags: adminTags("permissions"), DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeletePermission(ctx, service.UserActor(admin), in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "list-connections", Method: http.MethodGet, Path: "/api/v1/admin/connections", Tags: adminTags("monitoring"),
		Summary: "Device connection audit log"},
		func(ctx context.Context, in *ConnectionsIn) (*ConnectionsOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			var dev *uuid.UUID
			if in.DeviceID != "" {
				id, err := uuid.Parse(in.DeviceID)
				if err != nil {
					return nil, huma.Error422UnprocessableEntity("device_id must be a UUID")
				}
				dev = &id
			}
			cs, err := store.ListConnections(ctx, a.pool, dev, in.Limit)
			if err != nil {
				return nil, a.fail(err)
			}
			return &ConnectionsOut{Body: cs}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "list-presence", Method: http.MethodGet, Path: "/api/v1/admin/presence", Tags: adminTags("monitoring"),
		Summary: "Live device connections (leases)"},
		func(ctx context.Context, _ *struct{}) (*PresenceOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			ps, err := store.ListPresence(ctx, a.pool)
			if err != nil {
				return nil, a.fail(err)
			}
			return &PresenceOut{Body: ps}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "list-audit", Method: http.MethodGet, Path: "/api/v1/admin/audit", Tags: adminTags("monitoring"),
		Summary: "Admin action history"},
		func(ctx context.Context, in *AuditIn) (*AuditOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			es, err := store.ListAudit(ctx, a.pool, in.Limit)
			if err != nil {
				return nil, a.fail(err)
			}
			return &AuditOut{Body: es}, nil
		})
}
