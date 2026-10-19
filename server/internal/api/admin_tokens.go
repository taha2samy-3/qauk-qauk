package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// MachineUser is a machine user with its API tokens (never their secrets).
type MachineUser struct {
	store.User
	Tokens []store.APIToken `json:"tokens"`
}

type MachineUsersOut struct{ Body []MachineUser }

type MachineUserCreateIn struct {
	Body struct {
		Name  string `json:"name" minLength:"1" maxLength:"150" doc:"The account's username, e.g. greenhouse-dashboard"`
		Email string `json:"email,omitempty" maxLength:"254" doc:"Who to contact about it"`
	}
}

type APITokenCreateIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Name      string     `json:"name" minLength:"1" maxLength:"100" doc:"What the token is for, e.g. wallboard"`
		Scopes    []string   `json:"scopes" minItems:"1" doc:"read: elements, values, history; control: commands (elements granted RC); alerts: alert events" enum:"read,control,alerts"`
		ExpiresAt *time.Time `json:"expires_at,omitempty" doc:"Optional; the token stops working then"`
	}
}

type APITokenCreatedOut struct {
	Body struct {
		store.APIToken
		Token string `json:"token" doc:"The token, shown once: send it as Authorization: Bearer <token>"`
	}
}

type APITokenPath struct {
	ID      int64     `path:"id"`
	TokenID uuid.UUID `path:"tokenId"`
}

func (a *API) registerAdminTokens(api huma.API) {
	tags := adminTags("machine users")
	huma.Register(api, huma.Operation{OperationID: "list-machine-users", Method: http.MethodGet, Path: "/api/v1/admin/machine-users",
		Summary: "Machine users (accounts for integrations) with their API tokens", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*MachineUsersOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			us, err := store.ListMachineUsers(ctx, a.pool)
			if err != nil {
				return nil, a.fail(err)
			}
			out := make([]MachineUser, len(us))
			for i, u := range us {
				toks, err := store.ListAPITokens(ctx, a.pool, u.ID)
				if err != nil {
					return nil, a.fail(err)
				}
				out[i] = MachineUser{User: u, Tokens: toks}
			}
			return &MachineUsersOut{Body: out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-machine-user", Method: http.MethodPost, Path: "/api/v1/admin/machine-users",
		Summary: "Create a machine user: no password, never admin; grant it elements, then give it tokens", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *MachineUserCreateIn) (*UserOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			u, err := a.svc.CreateMachineUser(ctx, service.UserActor(admin), service.MachineUserInput{Name: in.Body.Name, Email: in.Body.Email})
			if err != nil {
				return nil, a.fail(err)
			}
			return &UserOut{Body: u}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-api-token", Method: http.MethodPost, Path: "/api/v1/admin/machine-users/{id}/tokens",
		Summary: "Create an API token for a machine user (the token is returned once)", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *APITokenCreateIn) (*APITokenCreatedOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			t, secret, err := a.svc.CreateAPIToken(ctx, service.UserActor(admin), in.ID,
				service.TokenInput{Name: in.Body.Name, Scopes: in.Body.Scopes, ExpiresAt: in.Body.ExpiresAt})
			if err != nil {
				return nil, a.fail(err)
			}
			out := &APITokenCreatedOut{}
			out.Body.APIToken, out.Body.Token = t, secret
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "revoke-api-token", Method: http.MethodDelete, Path: "/api/v1/admin/machine-users/{id}/tokens/{tokenId}",
		Summary: "Revoke an API token", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *APITokenPath) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if err := a.svc.RevokeAPIToken(ctx, service.UserActor(admin), in.ID, in.TokenID); err != nil {
				return nil, a.fail(err)
			}
			return nil, nil
		})
}
