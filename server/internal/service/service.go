// Package service implements every control-plane mutation. Each mutation runs
// in one transaction that writes the change, an audit row, and the outbox
// control events, so realtime consumers never miss a committed change.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/taha2samy/quackquack/server/internal/db"
	"github.com/taha2samy/quackquack/server/internal/events"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type Service struct {
	Pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service { return &Service{Pool: pool} }

// Actor identifies who performs a mutation (for the audit log).
type Actor struct {
	UserID *int64
	Name   string
}

func UserActor(u store.User) Actor { id := u.ID; return Actor{UserID: &id, Name: u.Username} }

var SystemActor = Actor{Name: "system"}

// ValidationError is a client error on a specific field.
type ValidationError struct {
	Field string
	Msg   string
}

func (e *ValidationError) Error() string { return fmt.Sprintf("%s: %s", e.Field, e.Msg) }

func invalid(field, msg string) error { return &ValidationError{Field: field, Msg: msg} }

func IsValidation(err error) (*ValidationError, bool) {
	var v *ValidationError
	ok := errors.As(err, &v)
	return v, ok
}

func (s *Service) tx(ctx context.Context, fn func(pgx.Tx) error) error {
	return db.InTx(ctx, s.Pool, fn)
}

// record writes the audit entry and enqueues control events in the same tx.
func record(ctx context.Context, tx pgx.Tx, a Actor, action, entity, entityID string, data any, ctrls ...events.ControlChanged) error {
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		raw = b
	}
	if err := store.InsertAudit(ctx, tx, store.AuditEntry{
		ActorUserID: a.UserID, ActorName: a.Name, Action: action, Entity: entity, EntityID: entityID, Data: raw,
	}); err != nil {
		return err
	}
	for _, c := range ctrls {
		ev, err := events.Control(c)
		if err != nil {
			return err
		}
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		if err := store.EnqueueOutbox(ctx, tx, events.TopicControlEvents, ev.PartitionKey, b); err != nil {
			return err
		}
	}
	return nil
}

func i64(v int64) *int64         { return &v }
func uid(v uuid.UUID) *uuid.UUID { return &v }
func idStr(v int64) string       { return fmt.Sprint(v) }
func newUUID() uuid.UUID         { return uuid.Must(uuid.NewV7()) }
func validPerm(p string) bool    { return p == store.PermRead || p == store.PermReadWrite }
