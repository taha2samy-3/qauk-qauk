package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type UUIDPath struct {
	ID uuid.UUID `path:"id"`
}

type KeyOut struct{ Body store.Key }
type KeysOut struct{ Body []store.Key }

type KeyCreateIn struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"100"`
		PEM  string `json:"pem" minLength:"1" maxLength:"16384" doc:"PEM public key. RSA >= 2048 bits (RS256) or ECDSA P-256 (ES256)."`
	}
}

type KeyPatchIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Name     *string `json:"name,omitempty" minLength:"1" maxLength:"100"`
		IsActive *bool   `json:"is_active,omitempty"`
	}
}

type DeviceOut struct{ Body store.Device }
type DevicesOut struct{ Body []store.Device }

type DeviceCreateIn struct {
	Body struct {
		Name        string     `json:"name" minLength:"1" maxLength:"50"`
		Description string     `json:"description,omitempty"`
		PublicKeyID *uuid.UUID `json:"public_key_id,omitempty"`
	}
}

type DevicePatchIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Name        *string `json:"name,omitempty" minLength:"1" maxLength:"50"`
		Description *string `json:"description,omitempty"`
		PublicKeyID *string `json:"public_key_id,omitempty" doc:"Key UUID, or empty string to unassign."`
	}
}

type ElementOut struct{ Body store.Element }
type ElementsOut struct{ Body []store.Element }

type ElementListIn struct {
	DeviceID string `query:"device_id" doc:"Filter by device UUID"`
}

type ElementCreateIn struct {
	Body struct {
		DeviceID    uuid.UUID       `json:"device_id"`
		Name        string          `json:"name" minLength:"1" maxLength:"50"`
		Points      int             `json:"points" minimum:"0" maximum:"1000" doc:"History window replayed to new subscribers"`
		Description string          `json:"description,omitempty"`
		Details     json.RawMessage `json:"details,omitempty" doc:"Free-form widget configuration"`
	}
}

type ElementPatchIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Name        *string          `json:"name,omitempty" minLength:"1" maxLength:"50"`
		Points      *int             `json:"points,omitempty" minimum:"0" maximum:"1000"`
		Description *string          `json:"description,omitempty"`
		Details     *json.RawMessage `json:"details,omitempty"`
	}
}

type StyleOut struct{ Body store.Style }
type StylesOut struct{ Body []store.Style }

type StyleCreateIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Name    string          `json:"name" minLength:"1" maxLength:"100"`
		Details json.RawMessage `json:"details,omitempty"`
	}
}

type StylePatchIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Name    *string          `json:"name,omitempty" minLength:"1" maxLength:"100"`
		Details *json.RawMessage `json:"details,omitempty"`
	}
}

func (a *API) registerAdminDevices(api huma.API) {
	// --- keys ---
	huma.Register(api, huma.Operation{OperationID: "list-keys", Method: http.MethodGet, Path: "/api/v1/admin/keys", Tags: adminTags("keys")},
		func(ctx context.Context, _ *struct{}) (*KeysOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			ks, err := store.ListKeys(ctx, a.pool)
			if err != nil {
				return nil, a.fail(err)
			}
			return &KeysOut{Body: ks}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "create-key", Method: http.MethodPost, Path: "/api/v1/admin/keys", Tags: adminTags("keys"), DefaultStatus: http.StatusCreated,
		Summary: "Upload a device public key; algorithm and size are detected"},
		func(ctx context.Context, in *KeyCreateIn) (*KeyOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			k, err := a.svc.CreateKey(ctx, service.UserActor(admin), in.Body.Name, in.Body.PEM, nil)
			if err != nil {
				return nil, a.fail(err)
			}
			return &KeyOut{Body: k}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-key", Method: http.MethodPatch, Path: "/api/v1/admin/keys/{id}", Tags: adminTags("keys"),
		Description: "Any change forces devices using this key to reconnect."},
		func(ctx context.Context, in *KeyPatchIn) (*KeyOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			k, err := a.svc.UpdateKey(ctx, service.UserActor(admin), in.ID, service.KeyPatch{Name: in.Body.Name, IsActive: in.Body.IsActive})
			if err != nil {
				return nil, a.fail(err)
			}
			return &KeyOut{Body: k}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-key", Method: http.MethodDelete, Path: "/api/v1/admin/keys/{id}", Tags: adminTags("keys"), DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *UUIDPath) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteKey(ctx, service.UserActor(admin), in.ID))
		})

	// --- devices ---
	huma.Register(api, huma.Operation{OperationID: "list-devices", Method: http.MethodGet, Path: "/api/v1/admin/devices", Tags: adminTags("devices")},
		func(ctx context.Context, _ *struct{}) (*DevicesOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			ds, err := store.ListDevices(ctx, a.pool)
			if err != nil {
				return nil, a.fail(err)
			}
			return &DevicesOut{Body: ds}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "create-device", Method: http.MethodPost, Path: "/api/v1/admin/devices", Tags: adminTags("devices"), DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *DeviceCreateIn) (*DeviceOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			d, err := a.svc.CreateDevice(ctx, service.UserActor(admin), service.DeviceInput{Name: in.Body.Name,
				Description: in.Body.Description, PublicKeyID: in.Body.PublicKeyID})
			if err != nil {
				return nil, a.fail(err)
			}
			return &DeviceOut{Body: d}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-device", Method: http.MethodGet, Path: "/api/v1/admin/devices/{id}", Tags: adminTags("devices")},
		func(ctx context.Context, in *UUIDPath) (*DeviceOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			d, err := store.GetDevice(ctx, a.pool, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			return &DeviceOut{Body: d}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-device", Method: http.MethodPatch, Path: "/api/v1/admin/devices/{id}", Tags: adminTags("devices")},
		func(ctx context.Context, in *DevicePatchIn) (*DeviceOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			p := service.DevicePatch{Name: in.Body.Name, Description: in.Body.Description}
			if in.Body.PublicKeyID != nil {
				var key *uuid.UUID
				if *in.Body.PublicKeyID != "" {
					id, err := uuid.Parse(*in.Body.PublicKeyID)
					if err != nil {
						return nil, huma.Error422UnprocessableEntity("public_key_id must be a UUID or empty")
					}
					key = &id
				}
				p.PublicKeyID = &key
			}
			d, err := a.svc.UpdateDevice(ctx, service.UserActor(admin), in.ID, p)
			if err != nil {
				return nil, a.fail(err)
			}
			return &DeviceOut{Body: d}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-device", Method: http.MethodDelete, Path: "/api/v1/admin/devices/{id}", Tags: adminTags("devices"), DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *UUIDPath) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteDevice(ctx, service.UserActor(admin), in.ID))
		})

	// --- elements ---
	huma.Register(api, huma.Operation{OperationID: "list-elements", Method: http.MethodGet, Path: "/api/v1/admin/elements", Tags: adminTags("elements")},
		func(ctx context.Context, in *ElementListIn) (*ElementsOut, error) {
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
			es, err := store.ListElements(ctx, a.pool, dev)
			if err != nil {
				return nil, a.fail(err)
			}
			return &ElementsOut{Body: es}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "create-element", Method: http.MethodPost, Path: "/api/v1/admin/elements", Tags: adminTags("elements"), DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *ElementCreateIn) (*ElementOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			b := in.Body
			e, err := a.svc.CreateElement(ctx, service.UserActor(admin), service.ElementInput{DeviceID: b.DeviceID, Name: b.Name,
				Points: b.Points, Description: b.Description, Details: b.Details})
			if err != nil {
				return nil, a.fail(err)
			}
			return &ElementOut{Body: e}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-element", Method: http.MethodGet, Path: "/api/v1/admin/elements/{id}", Tags: adminTags("elements")},
		func(ctx context.Context, in *UUIDPath) (*ElementOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			e, err := store.GetElement(ctx, a.pool, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			return &ElementOut{Body: e}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-element", Method: http.MethodPatch, Path: "/api/v1/admin/elements/{id}", Tags: adminTags("elements")},
		func(ctx context.Context, in *ElementPatchIn) (*ElementOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			b := in.Body
			e, err := a.svc.UpdateElement(ctx, service.UserActor(admin), in.ID, service.ElementPatch{Name: b.Name, Points: b.Points,
				Description: b.Description, Details: b.Details})
			if err != nil {
				return nil, a.fail(err)
			}
			return &ElementOut{Body: e}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-element", Method: http.MethodDelete, Path: "/api/v1/admin/elements/{id}", Tags: adminTags("elements"), DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *UUIDPath) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteElement(ctx, service.UserActor(admin), in.ID))
		})

	// --- element styles ---
	huma.Register(api, huma.Operation{OperationID: "list-element-styles", Method: http.MethodGet, Path: "/api/v1/admin/elements/{id}/styles", Tags: adminTags("elements")},
		func(ctx context.Context, in *UUIDPath) (*StylesOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			ss, err := store.ListStyles(ctx, a.pool, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			return &StylesOut{Body: ss}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "create-element-style", Method: http.MethodPost, Path: "/api/v1/admin/elements/{id}/styles", Tags: adminTags("elements"), DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *StyleCreateIn) (*StyleOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			s, err := a.svc.CreateStyle(ctx, service.UserActor(admin), store.Style{ElementID: in.ID, Name: in.Body.Name, Details: in.Body.Details})
			if err != nil {
				return nil, a.fail(err)
			}
			return &StyleOut{Body: s}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-element-style", Method: http.MethodPatch, Path: "/api/v1/admin/styles/{id}", Tags: adminTags("elements")},
		func(ctx context.Context, in *StylePatchIn) (*StyleOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			s, err := a.svc.UpdateStyle(ctx, service.UserActor(admin), in.ID, in.Body.Name, in.Body.Details)
			if err != nil {
				return nil, a.fail(err)
			}
			return &StyleOut{Body: s}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-element-style", Method: http.MethodDelete, Path: "/api/v1/admin/styles/{id}", Tags: adminTags("elements"), DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteStyle(ctx, service.UserActor(admin), in.ID))
		})
}
