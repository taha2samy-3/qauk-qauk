package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

var adminTags = func(t string) []string { return []string{"admin: " + t} }

type IDPath struct {
	ID int64 `path:"id"`
}

type UserOut struct{ Body store.User }
type UsersOut struct{ Body []store.User }

type UserCreateIn struct {
	Body struct {
		Username string `json:"username" minLength:"1" maxLength:"150"`
		Email    string `json:"email,omitempty" maxLength:"254"`
		Password string `json:"password" minLength:"8" maxLength:"1024"`
		IsActive *bool  `json:"is_active,omitempty"`
		IsAdmin  bool   `json:"is_admin,omitempty"`
	}
}

type UserPatchIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Username *string `json:"username,omitempty" minLength:"1" maxLength:"150"`
		Email    *string `json:"email,omitempty" maxLength:"254"`
		Password *string `json:"password,omitempty" minLength:"8" maxLength:"1024"`
		IsActive *bool   `json:"is_active,omitempty"`
		IsAdmin  *bool   `json:"is_admin,omitempty"`
	}
}

type GroupOut struct{ Body store.Group }
type GroupsOut struct{ Body []store.Group }

type GroupIn struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"150"`
	}
}

type GroupPatchIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"150"`
	}
}

type MemberPath struct {
	ID     int64 `path:"id"`
	UserID int64 `path:"user_id"`
}

func (a *API) registerAdminIdentity(api huma.API) {
	// --- users ---
	huma.Register(api, huma.Operation{OperationID: "list-users", Method: http.MethodGet, Path: "/api/v1/admin/users", Tags: adminTags("users")},
		func(ctx context.Context, _ *struct{}) (*UsersOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			us, err := store.ListUsers(ctx, a.pool)
			if err != nil {
				return nil, a.fail(err)
			}
			return &UsersOut{Body: us}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "create-user", Method: http.MethodPost, Path: "/api/v1/admin/users", Tags: adminTags("users"), DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *UserCreateIn) (*UserOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			active := in.Body.IsActive == nil || *in.Body.IsActive
			u, err := a.svc.CreateUser(ctx, service.UserActor(admin), service.UserInput{Username: in.Body.Username, Email: in.Body.Email,
				Password: in.Body.Password, IsActive: active, IsAdmin: in.Body.IsAdmin})
			if err != nil {
				return nil, a.fail(err)
			}
			return &UserOut{Body: u}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "get-user", Method: http.MethodGet, Path: "/api/v1/admin/users/{id}", Tags: adminTags("users")},
		func(ctx context.Context, in *IDPath) (*UserOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			u, err := store.GetUser(ctx, a.pool, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			return &UserOut{Body: u}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "update-user", Method: http.MethodPatch, Path: "/api/v1/admin/users/{id}", Tags: adminTags("users"),
		Description: "Deactivating a user or changing the password ends all of their sessions and closes their sockets."},
		func(ctx context.Context, in *UserPatchIn) (*UserOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			b := in.Body
			u, err := a.svc.UpdateUser(ctx, service.UserActor(admin), in.ID, service.UserPatch{Username: b.Username, Email: b.Email,
				Password: b.Password, IsActive: b.IsActive, IsAdmin: b.IsAdmin})
			if err != nil {
				return nil, a.fail(err)
			}
			return &UserOut{Body: u}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-user", Method: http.MethodDelete, Path: "/api/v1/admin/users/{id}", Tags: adminTags("users"), DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			if admin.ID == in.ID {
				return nil, huma.Error422UnprocessableEntity("you cannot delete yourself")
			}
			return nil, a.wrap(a.svc.DeleteUser(ctx, service.UserActor(admin), in.ID))
		})

	// --- groups ---
	huma.Register(api, huma.Operation{OperationID: "list-groups", Method: http.MethodGet, Path: "/api/v1/admin/groups", Tags: adminTags("groups")},
		func(ctx context.Context, _ *struct{}) (*GroupsOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			gs, err := store.ListGroups(ctx, a.pool)
			if err != nil {
				return nil, a.fail(err)
			}
			return &GroupsOut{Body: gs}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "create-group", Method: http.MethodPost, Path: "/api/v1/admin/groups", Tags: adminTags("groups"), DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *GroupIn) (*GroupOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			g, err := a.svc.CreateGroup(ctx, service.UserActor(admin), in.Body.Name)
			if err != nil {
				return nil, a.fail(err)
			}
			return &GroupOut{Body: g}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "rename-group", Method: http.MethodPatch, Path: "/api/v1/admin/groups/{id}", Tags: adminTags("groups")},
		func(ctx context.Context, in *GroupPatchIn) (*GroupOut, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			g, err := a.svc.RenameGroup(ctx, service.UserActor(admin), in.ID, in.Body.Name)
			if err != nil {
				return nil, a.fail(err)
			}
			return &GroupOut{Body: g}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "delete-group", Method: http.MethodDelete, Path: "/api/v1/admin/groups/{id}", Tags: adminTags("groups"), DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.DeleteGroup(ctx, service.UserActor(admin), in.ID))
		})
	huma.Register(api, huma.Operation{OperationID: "list-group-members", Method: http.MethodGet, Path: "/api/v1/admin/groups/{id}/members", Tags: adminTags("groups")},
		func(ctx context.Context, in *IDPath) (*UsersOut, error) {
			if _, err := requireAdmin(ctx); err != nil {
				return nil, err
			}
			us, err := store.ListGroupMembers(ctx, a.pool, in.ID)
			if err != nil {
				return nil, a.fail(err)
			}
			return &UsersOut{Body: us}, nil
		})
	huma.Register(api, huma.Operation{OperationID: "add-group-member", Method: http.MethodPut, Path: "/api/v1/admin/groups/{id}/members/{user_id}", Tags: adminTags("groups"), DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *MemberPath) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.AddGroupMember(ctx, service.UserActor(admin), in.ID, in.UserID))
		})
	huma.Register(api, huma.Operation{OperationID: "remove-group-member", Method: http.MethodDelete, Path: "/api/v1/admin/groups/{id}/members/{user_id}", Tags: adminTags("groups"), DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *MemberPath) (*struct{}, error) {
			admin, err := requireAdmin(ctx)
			if err != nil {
				return nil, err
			}
			return nil, a.wrap(a.svc.RemoveGroupMember(ctx, service.UserActor(admin), in.ID, in.UserID))
		})
}

func (a *API) wrap(err error) error {
	if err != nil {
		return a.fail(err)
	}
	return nil
}
