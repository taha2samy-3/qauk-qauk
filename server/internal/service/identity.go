package service

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/authn"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/store"
)

const minPasswordLen = 8

type UserInput struct {
	Username string
	Email    string
	Password string
	IsActive bool
	IsAdmin  bool
}

func validateUsername(u string) error {
	if u = strings.TrimSpace(u); u == "" || len(u) > 150 {
		return invalid("username", "must be 1-150 characters")
	}
	return nil
}

func hashNew(password string) (string, error) {
	if len(password) < minPasswordLen {
		return "", invalid("password", "must be at least 8 characters")
	}
	return authn.HashPassword(password)
}

func (s *Service) CreateUser(ctx context.Context, a Actor, in UserInput) (store.User, error) {
	if err := validateUsername(in.Username); err != nil {
		return store.User{}, err
	}
	hash, err := hashNew(in.Password)
	if err != nil {
		return store.User{}, err
	}
	var u store.User
	err = s.tx(ctx, func(tx pgx.Tx) error {
		u, err = store.InsertUser(ctx, tx, store.User{Username: strings.TrimSpace(in.Username), Email: in.Email,
			PasswordHash: hash, IsActive: in.IsActive, IsAdmin: in.IsAdmin})
		if err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "user", idStr(u.ID), u,
			events.ControlChanged{Kind: events.KindUser, Op: events.OpCreate, ID: idStr(u.ID), UserID: i64(u.ID)})
	})
	return u, err
}

type UserPatch struct {
	Username *string
	Email    *string
	Password *string
	IsActive *bool
	IsAdmin  *bool
}

// UpdateUser applies a patch. Deactivation or a password change revokes all
// sessions, and the control event makes gateways close the user's sockets.
func (s *Service) UpdateUser(ctx context.Context, a Actor, id int64, p UserPatch) (store.User, error) {
	var u store.User
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if u, err = store.GetUser(ctx, tx, id); err != nil {
			return err
		}
		revoke := false
		if p.Username != nil {
			if err := validateUsername(*p.Username); err != nil {
				return err
			}
			u.Username = strings.TrimSpace(*p.Username)
		}
		if p.Email != nil {
			u.Email = *p.Email
		}
		if p.Password != nil {
			if u.PasswordHash, err = hashNew(*p.Password); err != nil {
				return err
			}
			revoke = true
		}
		if p.IsActive != nil {
			revoke = revoke || (u.IsActive && !*p.IsActive)
			u.IsActive = *p.IsActive
		}
		if p.IsAdmin != nil {
			u.IsAdmin = *p.IsAdmin
		}
		if u, err = store.UpdateUser(ctx, tx, u); err != nil {
			return err
		}
		if revoke {
			if err := store.DeleteUserSessions(ctx, tx, id); err != nil {
				return err
			}
		}
		return record(ctx, tx, a, "update", "user", idStr(id), u,
			events.ControlChanged{Kind: events.KindUser, Op: events.OpUpdate, ID: idStr(id), UserID: i64(id)})
	})
	return u, err
}

func (s *Service) DeleteUser(ctx context.Context, a Actor, id int64) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.DeleteUser(ctx, tx, id); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "user", idStr(id), nil,
			events.ControlChanged{Kind: events.KindUser, Op: events.OpDelete, ID: idStr(id), UserID: i64(id)})
	})
}

// ChangeOwnPassword verifies the old password first.
func (s *Service) ChangeOwnPassword(ctx context.Context, u store.User, oldPw, newPw string) error {
	ok, _, err := authn.VerifyPassword(u.PasswordHash, oldPw)
	if err != nil || !ok {
		return invalid("old_password", "incorrect password")
	}
	_, err = s.UpdateUser(ctx, UserActor(u), u.ID, UserPatch{Password: &newPw})
	return err
}

// --- Groups ---

func (s *Service) CreateGroup(ctx context.Context, a Actor, name string) (store.Group, error) {
	if name = strings.TrimSpace(name); name == "" || len(name) > 150 {
		return store.Group{}, invalid("name", "must be 1-150 characters")
	}
	var g store.Group
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if g, err = store.InsertGroup(ctx, tx, name); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "group", idStr(g.ID), g)
	})
	return g, err
}

func (s *Service) RenameGroup(ctx context.Context, a Actor, id int64, name string) (store.Group, error) {
	if name = strings.TrimSpace(name); name == "" || len(name) > 150 {
		return store.Group{}, invalid("name", "must be 1-150 characters")
	}
	var g store.Group
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if g, err = store.RenameGroup(ctx, tx, id, name); err != nil {
			return err
		}
		return record(ctx, tx, a, "update", "group", idStr(id), g)
	})
	return g, err
}

// DeleteGroup emits a membership change for every member so their live
// subscriptions are re-evaluated.
func (s *Service) DeleteGroup(ctx context.Context, a Actor, id int64) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		members, err := store.ListGroupMembers(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.DeleteGroup(ctx, tx, id); err != nil {
			return err
		}
		ctrls := make([]events.ControlChanged, 0, len(members))
		for _, m := range members {
			ctrls = append(ctrls, events.ControlChanged{Kind: events.KindGroupMembership, Op: events.OpDelete,
				ID: idStr(m.ID), UserID: i64(m.ID), GroupID: i64(id)})
		}
		return record(ctx, tx, a, "delete", "group", idStr(id), nil, ctrls...)
	})
}

func (s *Service) AddGroupMember(ctx context.Context, a Actor, groupID, userID int64) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		added, err := store.AddGroupMember(ctx, tx, groupID, userID)
		if err != nil || !added {
			return err
		}
		return record(ctx, tx, a, "add_member", "group", idStr(groupID), map[string]int64{"user_id": userID},
			events.ControlChanged{Kind: events.KindGroupMembership, Op: events.OpCreate, ID: idStr(userID), UserID: i64(userID), GroupID: i64(groupID)})
	})
}

// RemoveGroupMember fixes B8: the user's subscriptions are re-evaluated live.
func (s *Service) RemoveGroupMember(ctx context.Context, a Actor, groupID, userID int64) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.RemoveGroupMember(ctx, tx, groupID, userID); err != nil {
			return err
		}
		return record(ctx, tx, a, "remove_member", "group", idStr(groupID), map[string]int64{"user_id": userID},
			events.ControlChanged{Kind: events.KindGroupMembership, Op: events.OpDelete, ID: idStr(userID), UserID: i64(userID), GroupID: i64(groupID)})
	})
}
