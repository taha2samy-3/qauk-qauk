package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ElementPipeline struct {
	ElementID uuid.UUID       `json:"element_id"`
	Version   int             `json:"version"`
	Steps     json.RawMessage `json:"steps"`
	UpdatedBy string          `json:"updated_by"`
	UpdatedAt time.Time       `json:"updated_at"`
}

type ElementPipelineVersion struct {
	ElementID uuid.UUID       `json:"element_id"`
	Version   int             `json:"version"`
	Steps     json.RawMessage `json:"steps"`
	UpdatedBy string          `json:"updated_by"`
	UpdatedAt time.Time       `json:"updated_at"`
}

const pipelineCols = `element_id, version, steps, updated_by, updated_at`

func GetElementPipeline(ctx context.Context, db DBTX, elementID uuid.UUID) (ElementPipeline, error) {
	return one[ElementPipeline](db.Query(ctx, `SELECT `+pipelineCols+` FROM element_pipelines WHERE element_id = $1`, elementID))
}

func ListElementPipelineVersions(ctx context.Context, db DBTX, elementID uuid.UUID) ([]ElementPipelineVersion, error) {
	return many[ElementPipelineVersion](db.Query(ctx, `SELECT `+pipelineCols+` FROM element_pipeline_versions WHERE element_id = $1 ORDER BY version DESC`, elementID))
}

func GetElementPipelineVersion(ctx context.Context, db DBTX, elementID uuid.UUID, version int) (ElementPipelineVersion, error) {
	return one[ElementPipelineVersion](db.Query(ctx, `SELECT `+pipelineCols+` FROM element_pipeline_versions WHERE element_id = $1 AND version = $2`, elementID, version))
}

// SaveElementPipeline stores steps as the element's next pipeline version and
// records it in the version history. The version continues from the history,
// so it never collides with an older version (even after a pipeline was
// cleared). Callers hold the device row lock.
func SaveElementPipeline(ctx context.Context, db DBTX, elementID uuid.UUID, steps json.RawMessage, updatedBy string) (ElementPipeline, error) {
	if len(steps) == 0 {
		steps = json.RawMessage("[]")
	}
	p, err := one[ElementPipeline](db.Query(ctx, `
		WITH next AS (
			SELECT COALESCE(MAX(version), 0) + 1 AS v FROM element_pipeline_versions WHERE element_id = $1
		)
		INSERT INTO element_pipelines (element_id, version, steps, updated_by, updated_at)
		SELECT $1, next.v, $2, $3, now() FROM next
		ON CONFLICT (element_id) DO UPDATE SET
			version = GREATEST(element_pipelines.version + 1, EXCLUDED.version),
			steps = EXCLUDED.steps,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
		RETURNING `+pipelineCols, elementID, steps, updatedBy))
	if err != nil {
		return ElementPipeline{}, mapErr(err)
	}
	_, err = db.Exec(ctx, `
		INSERT INTO element_pipeline_versions (element_id, version, steps, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5)`, p.ElementID, p.Version, p.Steps, p.UpdatedBy, p.UpdatedAt)
	if err != nil {
		return ElementPipeline{}, mapErr(err)
	}
	return p, nil
}
