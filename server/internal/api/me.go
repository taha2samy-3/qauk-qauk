package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/store"
)

type MyElementsOutput struct {
	Body []store.UserElement
}

type HistoryInput struct {
	ID    uuid.UUID `path:"id"`
	From  time.Time `query:"from" doc:"Start (RFC 3339). Default: to - 1h."`
	To    time.Time `query:"to" doc:"End (RFC 3339, exclusive). Default: now."`
	Step  string    `query:"step" enum:"raw,1m,5m,15m,1h,1d" default:"raw" doc:"raw events, or numeric aggregates per bucket"`
	Limit int       `query:"limit" minimum:"1" maximum:"10000" default:"1000" doc:"Max raw events"`
	Field string    `query:"field" maxLength:"200" doc:"Aggregate this message attribute (e.g. temperature, gps.lat, sensors[0].temp) instead of message.value. Raw events always return the full message."`
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
		ElementID uuid.UUID      `json:"element_id"`
		Step      string         `json:"step"`
		Events    []HistoryEvent `json:"events,omitempty"`
		Buckets   []store.Bucket `json:"buckets,omitempty"`
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
	out := &HistoryOutput{}
	out.Body.ElementID, out.Body.Step = in.ID, in.Step
	if in.Step == "raw" {
		rows, err := store.EventsInRange(ctx, a.pool, in.ID, from, to, in.Limit)
		if err != nil {
			return nil, a.fail(err)
		}
		out.Body.Events = make([]HistoryEvent, len(rows))
		for i, r := range rows {
			out.Body.Events[i] = HistoryEvent{Time: r.Time, Source: r.Source, ActorID: r.ActorID, ActorName: r.ActorName,
				Message: r.Payload, Value: r.Value}
		}
		return out, nil
	}
	var b []store.Bucket
	if in.Field == "" || in.Field == "value" {
		b, err = store.Buckets(ctx, a.pool, in.ID, from, to, steps[in.Step])
	} else {
		path, perr := store.ParseFieldPath(in.Field)
		if perr != nil {
			return nil, huma.Error422UnprocessableEntity("field must be a path like temperature, gps.lat or sensors[0].temp")
		}
		b, err = store.FieldBuckets(ctx, a.pool, in.ID, from, to, steps[in.Step], path)
	}
	if err != nil {
		return nil, a.fail(err)
	}
	out.Body.Buckets = b
	return out, nil
}
