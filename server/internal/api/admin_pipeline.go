package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

type PipelineGetOut struct {
	Body service.PipelineDetails
}

type PipelinePutIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Steps json.RawMessage `json:"steps" doc:"Array of pipeline step configs"`
	}
}

type PipelineRollbackIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Version int `json:"version" minimum:"1"`
	}
}

type PipelineSaveOut struct {
	Body store.ElementPipeline
}

type PipelinePreviewIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Steps json.RawMessage `json:"steps"`
	}
}

type PreviewRow struct {
	Time     string          `json:"time"`
	Before   json.RawMessage `json:"before"`
	After    json.RawMessage `json:"after,omitempty"`
	Filtered bool            `json:"filtered"`
	Reason   string          `json:"reason,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type PreviewSummary struct {
	Total    int `json:"total"`
	Passed   int `json:"passed"`
	Filtered int `json:"filtered"`
	Failed   int `json:"failed"`
}

type PipelinePreviewOut struct {
	Body struct {
		Summary PreviewSummary `json:"summary"`
		Rows    []PreviewRow   `json:"rows"`
	}
}

type PipelineTestIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Steps   json.RawMessage `json:"steps"`
		Message json.RawMessage `json:"message"`
		Last    json.RawMessage `json:"last,omitempty"`
	}
}

type PipelineTestOut struct {
	Body struct {
		After    json.RawMessage `json:"after,omitempty"`
		Filtered bool            `json:"filtered"`
		Reason   string          `json:"reason,omitempty"`
		Error    string          `json:"error,omitempty"`
		Inverse  json.RawMessage `json:"inverse,omitempty"`
	}
}

func (a *API) registerAdminPipeline(api huma.API) {
	// GET /api/v1/admin/elements/{id}/pipeline
	huma.Register(api, huma.Operation{
		OperationID: "get-element-pipeline",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/elements/{id}/pipeline",
		Tags:        adminTags("elements"),
	}, func(ctx context.Context, in *UUIDPath) (*PipelineGetOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		res, err := a.svc.GetElementPipeline(ctx, in.ID)
		if err != nil {
			return nil, a.fail(err)
		}
		return &PipelineGetOut{Body: res}, nil
	})

	// PUT /api/v1/admin/elements/{id}/pipeline
	huma.Register(api, huma.Operation{
		OperationID: "save-element-pipeline",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/elements/{id}/pipeline",
		Tags:        adminTags("elements"),
	}, func(ctx context.Context, in *PipelinePutIn) (*PipelineSaveOut, error) {
		admin, err := requireAdmin(ctx)
		if err != nil {
			return nil, err
		}
		p, err := a.svc.SaveElementPipeline(ctx, service.UserActor(admin), in.ID, in.Body.Steps)
		if err != nil {
			return nil, a.fail(err)
		}
		return &PipelineSaveOut{Body: p}, nil
	})

	// POST /api/v1/admin/elements/{id}/pipeline/rollback
	huma.Register(api, huma.Operation{
		OperationID: "rollback-element-pipeline",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/elements/{id}/pipeline/rollback",
		Tags:        adminTags("elements"),
	}, func(ctx context.Context, in *PipelineRollbackIn) (*PipelineSaveOut, error) {
		admin, err := requireAdmin(ctx)
		if err != nil {
			return nil, err
		}
		p, err := a.svc.RollbackElementPipeline(ctx, service.UserActor(admin), in.ID, in.Body.Version)
		if err != nil {
			return nil, a.fail(err)
		}
		return &PipelineSaveOut{Body: p}, nil
	})

	// DELETE /api/v1/admin/elements/{id}/pipeline
	huma.Register(api, huma.Operation{
		OperationID:   "delete-element-pipeline",
		Method:        http.MethodDelete,
		Path:          "/api/v1/admin/elements/{id}/pipeline",
		Tags:          adminTags("elements"),
		DefaultStatus: http.StatusNoContent,
	}, func(ctx context.Context, in *UUIDPath) (*struct{}, error) {
		admin, err := requireAdmin(ctx)
		if err != nil {
			return nil, err
		}
		return nil, a.wrap(a.svc.DeleteElementPipeline(ctx, service.UserActor(admin), in.ID))
	})

	// POST /api/v1/admin/elements/{id}/pipeline/preview
	huma.Register(api, huma.Operation{
		OperationID: "preview-element-pipeline",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/elements/{id}/pipeline/preview",
		Tags:        adminTags("elements"),
	}, func(ctx context.Context, in *PipelinePreviewIn) (*PipelinePreviewOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		pipe, err := elementpipe.CompileJSON(1, in.Body.Steps)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid steps: " + err.Error())
		}
		// Fetch last 500 stored device messages
		evMap, err := a.hist.Last(ctx, []uuid.UUID{in.ID}, 500, time.Time{})
		if err != nil {
			return nil, a.fail(err)
		}
		evs := evMap[in.ID]
		rows := make([]PreviewRow, 0, len(evs))
		summary := PreviewSummary{Total: len(evs)}
		var last elementpipe.Msg
		var lastAt time.Time
		maxSilence := pipe.MaxSilence()

		for _, ev := range evs {
			var parsed map[string]any
			if err := json.Unmarshal(ev.Payload, &parsed); err != nil {
				continue
			}
			activeLast := last
			if maxSilence > 0 && !lastAt.IsZero() && ev.Time.Sub(lastAt) >= maxSilence {
				activeLast = nil
			}
			out, runRes, fr, err := pipe.Apply(parsed, activeLast)
			row := PreviewRow{
				Time:   ev.Time.UTC().Format(time.RFC3339Nano),
				Before: ev.Payload,
			}
			if err != nil {
				summary.Failed++
				row.Error = err.Error()
			} else if runRes == elementpipe.RunFiltered {
				summary.Filtered++
				row.Filtered = true
				row.Reason = string(fr)
			} else {
				summary.Passed++
				b, _ := json.Marshal(out)
				row.After = b
				last = out
				lastAt = ev.Time
			}
			rows = append(rows, row)
		}

		out := &PipelinePreviewOut{}
		out.Body.Summary = summary
		out.Body.Rows = rows
		return out, nil
	})

	// POST /api/v1/admin/elements/{id}/pipeline/test
	huma.Register(api, huma.Operation{
		OperationID: "test-element-pipeline",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/elements/{id}/pipeline/test",
		Tags:        adminTags("elements"),
	}, func(ctx context.Context, in *PipelineTestIn) (*PipelineTestOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		pipe, err := elementpipe.CompileJSON(1, in.Body.Steps)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("invalid steps: " + err.Error())
		}
		var msg map[string]any
		if len(in.Body.Message) > 0 {
			if err := json.Unmarshal(in.Body.Message, &msg); err != nil {
				return nil, huma.Error422UnprocessableEntity("invalid message JSON: " + err.Error())
			}
		}
		var last map[string]any
		if len(in.Body.Last) > 0 && string(in.Body.Last) != "null" {
			if err := json.Unmarshal(in.Body.Last, &last); err != nil {
				return nil, huma.Error422UnprocessableEntity("invalid last JSON: " + err.Error())
			}
		}
		resOut := &PipelineTestOut{}
		// Test forward transform
		out, runRes, fr, runErr := pipe.Apply(msg, last)
		if runErr != nil {
			resOut.Body.Error = runErr.Error()
		} else if runRes == elementpipe.RunFiltered {
			resOut.Body.Filtered = true
			resOut.Body.Reason = string(fr)
		} else {
			b, _ := json.Marshal(out)
			resOut.Body.After = b
		}
		// Test inverse transform for commands on the input message
		if inv, err := pipe.Inverse(msg); err == nil && inv != nil {
			if b, err := json.Marshal(inv); err == nil {
				resOut.Body.Inverse = b
			}
		}
		return resOut, nil
	})
}
