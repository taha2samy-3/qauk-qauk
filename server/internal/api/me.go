package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/history"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type MyElementsOutput struct {
	Body []store.UserElement
}

type HistoryInput struct {
	ID     uuid.UUID `path:"id"`
	From   time.Time `query:"from" doc:"Start (RFC 3339). Default: to - 1h."`
	To     time.Time `query:"to" doc:"End (RFC 3339, exclusive). Default: now."`
	Step   string    `query:"step" enum:"raw,1m,5m,15m,1h,1d" default:"raw" doc:"raw events, or numeric aggregates per bucket"`
	Limit  int       `query:"limit" minimum:"1" maximum:"10000" default:"1000" doc:"Max raw events"`
	Newest bool      `query:"newest" doc:"Raw events: return the newest 'limit' events of the range instead of the oldest (still in ascending order)."`
	Field  string    `query:"field" maxLength:"200" doc:"Aggregate this message attribute (e.g. temperature, gps.lat, sensors[0].temp) instead of the element's value. Raw events always return the full message."`
}

type HistoryEvent struct {
	Time      time.Time `json:"t"`
	Source    string    `json:"source"`
	ActorID   string    `json:"actor_id"`
	ActorName string    `json:"actor_name"`
	Message   any       `json:"message"`
	Value     *float64  `json:"value"`
}

type HistoryOutput struct {
	Body struct {
		ElementID uuid.UUID        `json:"element_id"`
		Step      string           `json:"step"`
		Events    []HistoryEvent   `json:"events,omitempty"`
		Buckets   []history.Bucket `json:"buckets,omitempty"`
	}
}

var steps = map[string]time.Duration{"1m": time.Minute, "5m": 5 * time.Minute, "15m": 15 * time.Minute, "1h": time.Hour, "1d": 24 * time.Hour}

func (a *API) registerMe(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "my-elements", Method: http.MethodGet, Path: "/api/v1/me/elements", Tags: []string{"dashboard"},
		Summary: "Elements the current user can read, with effective permission, details and styles",
	}, func(ctx context.Context, _ *struct{}) (*MyElementsOutput, error) {
		u, err := currentUser(ctx)
		if err != nil {
			return nil, err
		}
		els, err := store.ListUserElements(ctx, a.pool, u.ID)
		if err != nil {
			return nil, a.fail(err)
		}
		return &MyElementsOutput{Body: els}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "element-history", Method: http.MethodGet, Path: "/api/v1/elements/{id}/history", Tags: []string{"dashboard"},
		Summary: "Historical values of an element (requires read permission)",
	}, a.history)
}

func (a *API) history(ctx context.Context, in *HistoryInput) (*HistoryOutput, error) {
	u, err := currentUser(ctx)
	if err != nil {
		return nil, err
	}
	perm, err := store.MaxPermission(ctx, a.pool, u.ID, in.ID)
	if err != nil {
		return nil, a.fail(err)
	}
	if perm == "" {
		return nil, huma.Error404NotFound("not found")
	}
	to := in.To
	if to.IsZero() {
		to = time.Now()
	}
	from := in.From
	if from.IsZero() {
		from = to.Add(-time.Hour)
	}
	if !from.Before(to) {
		return nil, huma.Error422UnprocessableEntity("from must be before to")
	}
	field := in.Field
	if field == "" {
		field = history.ValueField
	}
	if !history.ValidField(field) {
		return nil, huma.Error422UnprocessableEntity("field must be a path like temperature, gps.lat or sensors[0].temp")
	}
	if step, ok := steps[in.Step]; ok {
		if n := to.Sub(from) / step; int(n) > a.cfg.HistoryMaxBuckets {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf(
				"%s buckets over this range would be %d; the limit is %d. Use a larger step or a shorter range", in.Step, n, a.cfg.HistoryMaxBuckets))
		}
	}

	// Bound the cost: a limited number of history queries per instance, each
	// with a timeout, so heavy requests can't starve the database pool.
	release, err := a.acquireHistory(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	qctx, cancel := context.WithTimeout(ctx, a.cfg.HistoryQueryTimeout)
	defer cancel()

	out := &HistoryOutput{}
	out.Body.ElementID, out.Body.Step = in.ID, in.Step
	if in.Step == "raw" {
		evs, err := a.hist.Events(qctx, history.EventQuery{ElementID: in.ID, From: from, To: to, Limit: in.Limit, Newest: in.Newest})
		if err != nil {
			return nil, a.historyFail(qctx, err)
		}
		out.Body.Events = make([]HistoryEvent, len(evs))
		for i, e := range evs {
			out.Body.Events[i] = HistoryEvent{Time: e.Time, Source: e.Source, ActorID: e.ActorID, ActorName: e.ActorName,
				Message: e.Payload, Value: e.Value}
		}
		return out, nil
	}
	b, err := a.hist.Buckets(qctx, history.BucketQuery{ElementID: in.ID, Field: field, From: from, To: to, Step: steps[in.Step]})
	if err != nil {
		return nil, a.historyFail(qctx, err)
	}
	out.Body.Buckets = b
	return out, nil
}

func (a *API) acquireHistory(ctx context.Context) (func(), error) {
	wait, cancel := context.WithTimeout(ctx, a.cfg.HistoryQueryTimeout)
	defer cancel()
	select {
	case a.histSem <- struct{}{}:
		return func() { <-a.histSem }, nil
	case <-wait.Done():
		return nil, huma.Error503ServiceUnavailable("too many history queries; try again")
	}
}

func (a *API) historyFail(qctx context.Context, err error) error {
	if errors.Is(qctx.Err(), context.DeadlineExceeded) {
		return huma.Error503ServiceUnavailable("the history query took too long; use a shorter range or a larger step")
	}
	a.log.Error("api: history query", "err", err)
	return huma.Error500InternalServerError("internal error")
}
