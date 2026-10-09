package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/keys"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// --- Keys ---

func (s *Service) CreateKey(ctx context.Context, a Actor, name, pemText string, id *uuid.UUID) (store.Key, error) {
	if name = strings.TrimSpace(name); name == "" || len(name) > 100 {
		return store.Key{}, invalid("name", "must be 1-100 characters")
	}
	info, err := keys.Analyze(pemText)
	if err != nil {
		return store.Key{}, invalid("pem", err.Error())
	}
	k := store.Key{ID: newUUID(), Name: name, PEM: strings.TrimSpace(pemText) + "\n", Algorithm: info.Algorithm, KeySize: info.KeySize, IsActive: true}
	if id != nil {
		k.ID = *id
	}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if k, err = store.InsertKey(ctx, tx, k); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "jwt_key", k.ID.String(), map[string]any{"name": k.Name, "algorithm": k.Algorithm})
	})
	return k, err
}

type KeyPatch struct {
	Name     *string
	IsActive *bool
}

// UpdateKey: any change to a key forces devices using it to reconnect (rotation semantics).
func (s *Service) UpdateKey(ctx context.Context, a Actor, id uuid.UUID, p KeyPatch) (store.Key, error) {
	var k store.Key
	err := s.tx(ctx, func(tx pgx.Tx) error {
		devs, err := lockKeyDevices(ctx, tx, id)
		if err != nil {
			return err
		}
		if k, err = store.GetKey(ctx, tx, id); err != nil {
			return err
		}
		if p.Name != nil {
			if n := strings.TrimSpace(*p.Name); n == "" || len(n) > 100 {
				return invalid("name", "must be 1-100 characters")
			}
			k.Name = strings.TrimSpace(*p.Name)
		}
		if p.IsActive != nil {
			k.IsActive = *p.IsActive
		}
		if k, err = store.UpdateKey(ctx, tx, k); err != nil {
			return err
		}
		if err := publishDevices(ctx, tx, devs...); err != nil {
			return err
		}
		return record(ctx, tx, a, "update", "jwt_key", id.String(), map[string]any{"name": k.Name, "is_active": k.IsActive},
			events.ControlChanged{Kind: events.KindJWTKey, Op: events.OpUpdate, ID: id.String()})
	})
	return k, err
}

func (s *Service) DeleteKey(ctx context.Context, a Actor, id uuid.UUID) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		devs, err := lockKeyDevices(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.DeleteKey(ctx, tx, id); err != nil {
			return err
		}
		if err := publishDevices(ctx, tx, devs...); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "jwt_key", id.String(), nil,
			events.ControlChanged{Kind: events.KindJWTKey, Op: events.OpDelete, ID: id.String()})
	})
}

// lockKeyDevices locks the devices using a key, before the key changes.
func lockKeyDevices(ctx context.Context, tx pgx.Tx, keyID uuid.UUID) ([]uuid.UUID, error) {
	ds, err := store.DevicesUsingKey(ctx, tx, keyID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(ds))
	for i, d := range ds {
		ids[i] = d.ID
	}
	return store.LockDevices(ctx, tx, ids)
}

// --- Devices ---

type DeviceInput struct {
	ID          *uuid.UUID
	Name        string
	Description string
	PublicKeyID *uuid.UUID
}

func validDeviceName(n string) error {
	if n = strings.TrimSpace(n); n == "" || len(n) > 50 {
		return invalid("name", "must be 1-50 characters")
	}
	return nil
}

func (s *Service) CreateDevice(ctx context.Context, a Actor, in DeviceInput) (store.Device, error) {
	if err := validDeviceName(in.Name); err != nil {
		return store.Device{}, err
	}
	d := store.Device{ID: newUUID(), Name: strings.TrimSpace(in.Name), Description: in.Description, PublicKeyID: in.PublicKeyID}
	if in.ID != nil {
		d.ID = *in.ID
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if d, err = store.InsertDevice(ctx, tx, d); err != nil {
			return err
		}
		if err := publishDevices(ctx, tx, d.ID); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "device", d.ID.String(), d,
			events.ControlChanged{Kind: events.KindDevice, Op: events.OpCreate, ID: d.ID.String(), DeviceID: uid(d.ID)})
	})
	return d, err
}

type DevicePatch struct {
	Name        *string
	Description *string
	PublicKeyID **uuid.UUID // set to pointer-to-nil to clear the key
}

func (s *Service) UpdateDevice(ctx context.Context, a Actor, id uuid.UUID, p DevicePatch) (store.Device, error) {
	var d store.Device
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := store.LockDevices(ctx, tx, []uuid.UUID{id}); err != nil {
			return err
		}
		var err error
		if d, err = store.GetDevice(ctx, tx, id); err != nil {
			return err
		}
		if p.Name != nil {
			if err := validDeviceName(*p.Name); err != nil {
				return err
			}
			d.Name = strings.TrimSpace(*p.Name)
		}
		if p.Description != nil {
			d.Description = *p.Description
		}
		if p.PublicKeyID != nil {
			d.PublicKeyID = *p.PublicKeyID
		}
		if d, err = store.UpdateDevice(ctx, tx, d); err != nil {
			return err
		}
		if err := publishDevices(ctx, tx, id); err != nil {
			return err
		}
		return record(ctx, tx, a, "update", "device", id.String(), d,
			events.ControlChanged{Kind: events.KindDevice, Op: events.OpUpdate, ID: id.String(), DeviceID: uid(id)})
	})
	return d, err
}

// DeleteDevice also emits element deletions for its (cascade-deleted) elements.
func (s *Service) DeleteDevice(ctx context.Context, a Actor, id uuid.UUID) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := store.LockDevices(ctx, tx, []uuid.UUID{id}); err != nil {
			return err
		}
		elems, err := store.ListElements(ctx, tx, &id)
		if err != nil {
			return err
		}
		if err := store.DeleteDevice(ctx, tx, id); err != nil {
			return err
		}
		if err := publishDevices(ctx, tx, id); err != nil {
			return err
		}
		ctrls := []events.ControlChanged{{Kind: events.KindDevice, Op: events.OpDelete, ID: id.String(), DeviceID: uid(id)}}
		for _, e := range elems {
			ctrls = append(ctrls, events.ControlChanged{Kind: events.KindElement, Op: events.OpDelete, ID: e.ID.String(),
				ElementID: uid(e.ID), DeviceID: uid(id)})
		}
		return record(ctx, tx, a, "delete", "device", id.String(), nil, ctrls...)
	})
}

// --- Elements ---

type ElementInput struct {
	ID          *uuid.UUID
	DeviceID    uuid.UUID
	Name        string
	Points      int
	Description string
	Details     json.RawMessage
	Limits      ElementLimits
}

// ElementLimits are an element's rate-limit settings. In a patch, nil leaves
// a field unchanged; a rate or burst of 0 resets it to the server default.
type ElementLimits struct {
	MsgRate   *float64
	MsgBurst  *int
	OverLimit *string
}

// apply merges the patch into e and validates the result.
func (l ElementLimits) apply(e *store.Element, maxRate float64) error {
	if l.MsgRate != nil {
		e.MsgRate = l.MsgRate
		if *l.MsgRate == 0 {
			e.MsgRate = nil
		}
	}
	if l.MsgBurst != nil {
		e.MsgBurst = l.MsgBurst
		if *l.MsgBurst == 0 {
			e.MsgBurst = nil
		}
	}
	if l.OverLimit != nil {
		e.OverLimit = *l.OverLimit
	}
	if e.OverLimit == "" {
		e.OverLimit = store.OverLimitDrop
	}
	switch {
	case e.MsgRate != nil && (*e.MsgRate < 0 || *e.MsgRate > maxRate):
		return invalid("msg_rate", fmt.Sprintf("must be between 0 and %g messages per second", maxRate))
	case e.MsgBurst != nil && (*e.MsgBurst < 0 || float64(*e.MsgBurst) > maxRate):
		return invalid("msg_burst", fmt.Sprintf("must be between 0 and %g", maxRate))
	case e.OverLimit != store.OverLimitDrop && e.OverLimit != store.OverLimitLatest:
		return invalid("over_limit", `must be "drop" or "latest"`)
	}
	return nil
}

func validateElement(name string, points int, details json.RawMessage) error {
	if n := strings.TrimSpace(name); n == "" || len(n) > 50 {
		return invalid("name", "must be 1-50 characters")
	}
	if points < 0 || points > 1000 {
		return invalid("points", "must be between 0 and 1000")
	}
	if len(details) > 0 && !json.Valid(details) {
		return invalid("details", "must be valid JSON")
	}
	return nil
}

func (s *Service) CreateElement(ctx context.Context, a Actor, in ElementInput) (store.Element, error) {
	if err := validateElement(in.Name, in.Points, in.Details); err != nil {
		return store.Element{}, err
	}
	e := store.Element{ID: newUUID(), DeviceID: in.DeviceID, Name: strings.TrimSpace(in.Name), Points: in.Points,
		Description: in.Description, Details: in.Details}
	if in.ID != nil {
		e.ID = *in.ID
	}
	if err := in.Limits.apply(&e, s.ElementRateMax); err != nil {
		return store.Element{}, err
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if _, err := store.LockDevices(ctx, tx, []uuid.UUID{in.DeviceID}); err != nil {
			return err
		}
		var err error
		if e, err = store.InsertElement(ctx, tx, e); err != nil {
			return err
		}
		if err := publishDevices(ctx, tx, e.DeviceID); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "element", e.ID.String(), e,
			events.ControlChanged{Kind: events.KindElement, Op: events.OpCreate, ID: e.ID.String(), ElementID: uid(e.ID), DeviceID: uid(e.DeviceID)})
	})
	return e, err
}

type ElementPatch struct {
	Name        *string
	Points      *int
	Description *string
	Details     *json.RawMessage
	Limits      ElementLimits
}

// lockElementDevice locks the device owning an element (elements never move
// between devices), then reads the element.
func lockElementDevice(ctx context.Context, tx pgx.Tx, id uuid.UUID) (store.Element, error) {
	e, err := store.GetElement(ctx, tx, id)
	if err != nil {
		return e, err
	}
	if _, err := store.LockDevices(ctx, tx, []uuid.UUID{e.DeviceID}); err != nil {
		return e, err
	}
	return store.GetElement(ctx, tx, id)
}

func (s *Service) UpdateElement(ctx context.Context, a Actor, id uuid.UUID, p ElementPatch) (store.Element, error) {
	var e store.Element
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if e, err = lockElementDevice(ctx, tx, id); err != nil {
			return err
		}
		if p.Name != nil {
			e.Name = strings.TrimSpace(*p.Name)
		}
		if p.Points != nil {
			e.Points = *p.Points
		}
		if p.Description != nil {
			e.Description = *p.Description
		}
		if p.Details != nil {
			e.Details = *p.Details
		}
		if err := validateElement(e.Name, e.Points, e.Details); err != nil {
			return err
		}
		if err := p.Limits.apply(&e, s.ElementRateMax); err != nil {
			return err
		}
		if e, err = store.UpdateElement(ctx, tx, e); err != nil {
			return err
		}
		if err := publishDevices(ctx, tx, e.DeviceID); err != nil {
			return err
		}
		return record(ctx, tx, a, "update", "element", id.String(), e,
			events.ControlChanged{Kind: events.KindElement, Op: events.OpUpdate, ID: id.String(), ElementID: uid(id), DeviceID: uid(e.DeviceID)})
	})
	return e, err
}

func (s *Service) DeleteElement(ctx context.Context, a Actor, id uuid.UUID) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		e, err := lockElementDevice(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.DeleteElement(ctx, tx, id); err != nil {
			return err
		}
		if err := publishDevices(ctx, tx, e.DeviceID); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "element", id.String(), nil,
			events.ControlChanged{Kind: events.KindElement, Op: events.OpDelete, ID: id.String(), ElementID: uid(id), DeviceID: uid(e.DeviceID)})
	})
}

// --- Styles (presentation only: audited, no realtime event) ---

func (s *Service) CreateStyle(ctx context.Context, a Actor, st store.Style) (store.Style, error) {
	if n := strings.TrimSpace(st.Name); n == "" || len(n) > 100 {
		return store.Style{}, invalid("name", "must be 1-100 characters")
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if st, err = store.InsertStyle(ctx, tx, st); err != nil {
			return err
		}
		return record(ctx, tx, a, "create", "element_style", idStr(st.ID), st)
	})
	return st, err
}

func (s *Service) UpdateStyle(ctx context.Context, a Actor, id int64, name *string, details *json.RawMessage) (store.Style, error) {
	var st store.Style
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var err error
		if st, err = store.GetStyle(ctx, tx, id); err != nil {
			return err
		}
		if name != nil {
			st.Name = strings.TrimSpace(*name)
		}
		if details != nil {
			st.Details = *details
		}
		if st, err = store.UpdateStyle(ctx, tx, st); err != nil {
			return err
		}
		return record(ctx, tx, a, "update", "element_style", idStr(id), st)
	})
	return st, err
}

func (s *Service) DeleteStyle(ctx context.Context, a Actor, id int64) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if err := store.DeleteStyle(ctx, tx, id); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "element_style", idStr(id), nil)
	})
}

// --- Permissions ---

type PermissionInput struct {
	ElementID  uuid.UUID
	UserID     *int64
	GroupID    *int64
	Permission string
}

func (s *Service) SetPermission(ctx context.Context, a Actor, in PermissionInput) (store.Permission, error) {
	if (in.UserID == nil) == (in.GroupID == nil) {
		return store.Permission{}, invalid("user_id", "exactly one of user_id or group_id is required")
	}
	if !validPerm(in.Permission) {
		return store.Permission{}, invalid("permission", `must be "R" or "RC"`)
	}
	var p store.Permission
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var inserted bool
		var err error
		p, inserted, err = store.UpsertPermission(ctx, tx, store.Permission{ElementID: in.ElementID, UserID: in.UserID,
			GroupID: in.GroupID, Permission: in.Permission})
		if err != nil {
			return err
		}
		op := events.OpUpdate
		if inserted {
			op = events.OpCreate
		}
		return record(ctx, tx, a, op, "permission", idStr(p.ID), p, permEvent(p, op))
	})
	return p, err
}

func (s *Service) DeletePermission(ctx context.Context, a Actor, id int64) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		p, err := store.GetPermission(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := store.DeletePermission(ctx, tx, id); err != nil {
			return err
		}
		return record(ctx, tx, a, "delete", "permission", idStr(id), p, permEvent(p, events.OpDelete))
	})
}

func permEvent(p store.Permission, op string) events.ControlChanged {
	return events.ControlChanged{Kind: events.KindPermission, Op: op, ID: idStr(p.ID),
		ElementID: uid(p.ElementID), UserID: p.UserID, GroupID: p.GroupID}
}
