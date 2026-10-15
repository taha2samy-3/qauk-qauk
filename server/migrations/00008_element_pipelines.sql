-- +goose Up

CREATE TABLE element_pipelines (
    element_id uuid PRIMARY KEY REFERENCES elements(id) ON DELETE CASCADE,
    version    integer NOT NULL DEFAULT 1,
    steps      jsonb NOT NULL DEFAULT '[]'::jsonb, -- [] = no pipeline
    updated_by text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE element_pipeline_versions (
    element_id uuid    NOT NULL REFERENCES elements(id) ON DELETE CASCADE,
    version    integer NOT NULL,
    steps      jsonb   NOT NULL,
    updated_by text    NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (element_id, version)
);

-- Index for pipeline version history lookups
CREATE INDEX element_pipeline_versions_element_id_idx ON element_pipeline_versions (element_id, version DESC);

-- +goose Down
DROP TABLE IF EXISTS element_pipeline_versions;
DROP TABLE IF EXISTS element_pipelines;
