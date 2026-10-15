package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/taha2samy/quackquack/server/internal/elementpipe"
	"github.com/taha2samy/quackquack/server/internal/service"
	"github.com/taha2samy/quackquack/server/internal/store"
)

// PreviewSize is how many stored device messages a preview runs over.
const PreviewSize = 500

type PipelineGetOut struct {
	Body service.PipelineDetails
}

type PipelineStepsBody struct {
	Steps json.RawMessage `json:"steps" doc:"Array of pipeline step configs ([] for none)"`
}

type PipelinePutIn struct {
	ID   uuid.UUID `path:"id"`
	Body PipelineStepsBody
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
	Body PipelineStepsBody
}

type PreviewRow struct {
	Time     string          `json:"time"`
	Basis    string          `json:"basis" enum:"raw,reconstructed,stored" doc:"How before was obtained (see the response's basis)"`
	Stored   json.RawMessage `json:"stored" doc:"The message as stored (the output of the pipeline version active then)"`
	Before   json.RawMessage `json:"before,omitempty" doc:"The input the steps ran on (see basis)"`
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
		Basis   string         `json:"basis" enum:"raw,reconstructed,stored,mixed" doc:"raw: no pipeline was active when these values were stored, they are what the device sent; reconstructed: the inverse of the pipeline version active then turned them back into device values; stored: that version can't be inverted, the steps ran on its output; mixed: rows differ (see each row's basis)"`
		Note    string         `json:"note"`
		Summary PreviewSummary `json:"summary"`
		Rows    []PreviewRow   `json:"rows"`
	}
}

type PipelineTestIn struct {
	ID   uuid.UUID `path:"id"`
	Body struct {
		Steps   json.RawMessage `json:"steps"`
		Message json.RawMessage `json:"message" doc:"A device message"`
		Last    json.RawMessage `json:"last,omitempty" doc:"The last published message, for deadband and scripts"`
	}
}

type PipelineTestOut struct {
	Body struct {
		After           json.RawMessage `json:"after,omitempty"`
		Filtered        bool            `json:"filtered"`
		Reason          string          `json:"reason,omitempty"`
		Error           string          `json:"error,omitempty"`
		Inverse         json.RawMessage `json:"inverse,omitempty" doc:"after sent back as a command, in device units"`
		InverseError    string          `json:"inverse_error,omitempty"`
		Invertible      bool            `json:"invertible" doc:"Commands are transformed by at least one step"`
		StepsInvertible []bool          `json:"steps_invertible"`
	}
}

type PipelineValidateIn struct {
	Body PipelineStepsBody
}

type PipelineValidateOut struct {
	Body struct {
		Valid           bool   `json:"valid"`
		Error           string `json:"error,omitempty"`
		Invertible      bool   `json:"invertible" doc:"Commands are transformed by at least one step"`
		RoundTrips      bool   `json:"round_trips" doc:"Every step that changes values also applies to commands"`
		StepsInvertible []bool `json:"steps_invertible"`
	}
}

type PipelineUnitsOut struct {
	Body []elementpipe.UnitInfo
}

func (a *API) registerAdminPipeline(api huma.API) {
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

	huma.Register(api, huma.Operation{
		OperationID: "save-element-pipeline",
		Method:      http.MethodPut,
		Path:        "/api/v1/admin/elements/{id}/pipeline",
		Tags:        adminTags("elements"),
		Description: "Saves the steps as the element's next pipeline version and applies it on every gateway.",
	}, func(ctx context.Context, in *PipelinePutIn) (*PipelineSaveOut, error) {
		admin, err := requireAdmin(ctx)
		if err != nil {
			return nil, err
		}
		if s := bytes.TrimSpace(in.Body.Steps); len(s) == 0 || s[0] != '[' {
			return nil, huma.Error422UnprocessableEntity("steps must be an array ([] clears the pipeline)")
		}
		p, err := a.svc.SaveElementPipeline(ctx, service.UserActor(admin), in.ID, in.Body.Steps)
		if err != nil {
			return nil, a.fail(err)
		}
		return &PipelineSaveOut{Body: p}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "rollback-element-pipeline",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/elements/{id}/pipeline/rollback",
		Tags:        adminTags("elements"),
		Description: "Saves an earlier version's steps as a new version.",
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

	huma.Register(api, huma.Operation{
		OperationID:   "delete-element-pipeline",
		Method:        http.MethodDelete,
		Path:          "/api/v1/admin/elements/{id}/pipeline",
		Tags:          adminTags("elements"),
		DefaultStatus: http.StatusNoContent,
		Description:   "Clears the pipeline by saving an empty version; the history (and rollback) stays.",
	}, func(ctx context.Context, in *UUIDPath) (*struct{}, error) {
		admin, err := requireAdmin(ctx)
		if err != nil {
			return nil, err
		}
		return nil, a.wrap(a.svc.DeleteElementPipeline(ctx, service.UserActor(admin), in.ID))
	})

	huma.Register(api, huma.Operation{
		OperationID: "preview-element-pipeline",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/elements/{id}/pipeline/preview",
		Tags:        adminTags("elements"),
		Description: "Runs steps over the element's last stored device messages, in time order, without saving.",
	}, func(ctx context.Context, in *PipelinePreviewIn) (*PipelinePreviewOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		cur, err := a.svc.GetElementPipeline(ctx, in.ID)
		if err != nil {
			return nil, a.fail(err)
		}
		pipe, err := service.ValidatePipeline(in.Body.Steps)
		if err != nil {
			return nil, a.fail(err)
		}
		evMap, err := a.hist.Last(ctx, []uuid.UUID{in.ID}, PreviewSize, time.Time{})
		if err != nil {
			return nil, a.fail(err)
		}
		evs := evMap[in.ID]

		// History stores each message as the pipeline version active then
		// made it, not as the device sent it: reconstruct the device value
		// with that version's inverse where possible.
		versionAt := pipelineVersionAt(cur.Versions)
		out := &PipelinePreviewOut{}
		rows := make([]PreviewRow, 0, len(evs))
		summary := PreviewSummary{Total: len(evs)}
		bases := map[string]int{}
		var last elementpipe.Msg
		var lastAt time.Time
		for _, ev := range evs {
			row := PreviewRow{Time: ev.Time.UTC().Format(time.RFC3339Nano), Stored: ev.Payload, Basis: "raw"}
			input, err := decodeAny(ev.Payload)
			if active, version := versionAt(ev.Time); err == nil && active.HasSteps() {
				if active.IsInvertible() {
					row.Basis = "reconstructed"
					m, _ := elementpipe.WrapScalar(input)
					var inv elementpipe.Msg
					if inv, err = active.InverseWithContext(m, elementpipe.Context{Element: cur.ElementName, Time: ev.Time}); err == nil {
						input = inv
					} else {
						err = fmt.Errorf("version %d's inverse: %w", version, err)
					}
				} else {
					row.Basis = "stored"
				}
			}
			bases[row.Basis]++
			if err != nil {
				summary.Failed++
				row.Error = "can't read the stored message: " + err.Error()
				rows = append(rows, row)
				continue
			}
			msg, _ := elementpipe.WrapScalar(input)
			row.Before, _ = json.Marshal(msg)
			res, runRes, fr, err := pipe.ApplyWithContext(msg, elementpipe.Context{Element: cur.ElementName, Time: ev.Time, Last: last, LastAt: lastAt})
			switch {
			case err != nil:
				summary.Failed++
				row.Error = err.Error()
			case runRes == elementpipe.RunFiltered:
				summary.Filtered++
				row.Filtered, row.Reason = true, string(fr)
			default:
				summary.Passed++
				row.After, _ = json.Marshal(res)
				last, lastAt = res, ev.Time
			}
			rows = append(rows, row)
		}
		switch {
		case len(bases) > 1:
			out.Body.Basis = "mixed"
		case bases["reconstructed"] > 0:
			out.Body.Basis = "reconstructed"
		case bases["stored"] > 0:
			out.Body.Basis = "stored"
		default:
			out.Body.Basis = "raw"
		}
		out.Body.Note = map[string]string{
			"raw":           "No pipeline was active when these messages were stored: they are what the device sent.",
			"reconstructed": "The stored messages are pipeline output; the inverse of the version active then turned them back into device values. Rounding and clamping can't be undone exactly, and messages a pipeline filtered aren't stored.",
			"stored":        "The pipeline active when these messages were stored can't be inverted (pick, a map that isn't one-to-one, or a script without untransform): the steps ran on its stored output, not on what the device sent.",
			"mixed":         "Different pipeline versions were active over these messages: each row says whether it ran on what the device sent (raw), a reconstruction (reconstructed) or the stored output (stored).",
		}[out.Body.Basis]
		out.Body.Summary, out.Body.Rows = summary, rows
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "test-element-pipeline",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/elements/{id}/pipeline/test",
		Tags:        adminTags("elements"),
		Description: "Runs steps on one message, and sends the result back through the command path.",
	}, func(ctx context.Context, in *PipelineTestIn) (*PipelineTestOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		cur, err := a.svc.GetElementPipeline(ctx, in.ID)
		if err != nil {
			return nil, a.fail(err)
		}
		pipe, err := service.ValidatePipeline(in.Body.Steps)
		if err != nil {
			return nil, a.fail(err)
		}
		input, err := decodeAny(in.Body.Message)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity("message must be JSON: " + err.Error())
		}
		msg, _ := elementpipe.WrapScalar(input)
		var last elementpipe.Msg
		if s := bytes.TrimSpace(in.Body.Last); len(s) > 0 && string(s) != "null" {
			v, err := decodeAny(s)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("last must be JSON: " + err.Error())
			}
			last, _ = elementpipe.WrapScalar(v)
		}
		pctx := elementpipe.Context{Element: cur.ElementName, Time: time.Now().UTC(), Last: last}
		out := &PipelineTestOut{}
		out.Body.Invertible, out.Body.StepsInvertible = pipe.Invertible(), nonNil(pipe.StepsInvertible())
		res, runRes, fr, runErr := pipe.ApplyWithContext(msg, pctx)
		switch {
		case runErr != nil:
			out.Body.Error = runErr.Error()
		case runRes == elementpipe.RunFiltered:
			out.Body.Filtered, out.Body.Reason = true, string(fr)
		default:
			out.Body.After, _ = json.Marshal(res)
			if inv, err := pipe.InverseWithContext(res, pctx); err != nil {
				out.Body.InverseError = err.Error()
			} else {
				out.Body.Inverse, _ = json.Marshal(inv)
			}
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "validate-pipeline",
		Method:      http.MethodPost,
		Path:        "/api/v1/admin/pipelines/validate",
		Tags:        adminTags("elements"),
		Description: "Checks steps as a save would, and tells which apply to commands.",
	}, func(ctx context.Context, in *PipelineValidateIn) (*PipelineValidateOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		out := &PipelineValidateOut{}
		out.Body.StepsInvertible = []bool{}
		pipe, err := service.ValidatePipeline(in.Body.Steps)
		if err != nil {
			if v, ok := service.IsValidation(err); ok {
				out.Body.Error = v.Msg
				return out, nil
			}
			return nil, a.fail(err)
		}
		out.Body.Valid = true
		out.Body.Invertible, out.Body.RoundTrips = pipe.Invertible(), pipe.IsInvertible()
		out.Body.StepsInvertible = nonNil(pipe.StepsInvertible())
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-pipeline-units",
		Method:      http.MethodGet,
		Path:        "/api/v1/admin/pipelines/units",
		Tags:        adminTags("elements"),
		Description: "The units the unit step converts between (within one dimension).",
	}, func(ctx context.Context, _ *struct{}) (*PipelineUnitsOut, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		return &PipelineUnitsOut{Body: elementpipe.Units()}, nil
	})
}

// pipelineVersionAt returns a lookup of the pipeline version active at a
// time (by the versions' save times; compiled on first use).
func pipelineVersionAt(versions []store.ElementPipelineVersion) func(time.Time) (*elementpipe.Pipeline, int) {
	compiled := map[int]*elementpipe.Pipeline{}
	return func(at time.Time) (*elementpipe.Pipeline, int) {
		for _, v := range versions { // newest first
			if v.UpdatedAt.After(at) {
				continue
			}
			p, ok := compiled[v.Version]
			if !ok {
				p, _ = elementpipe.CompileJSON(v.Version, v.Steps)
				compiled[v.Version] = p
			}
			return p, v.Version
		}
		return nil, 0
	}
}

// decodeAny decodes a JSON message keeping numbers exact.
func decodeAny(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

func nonNil(b []bool) []bool {
	if b == nil {
		return []bool{}
	}
	return b
}
