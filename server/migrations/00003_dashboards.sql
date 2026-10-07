-- +goose Up
-- User-built dashboards. `layout` is owned by the frontend (widget grid + options);
-- data access is still enforced per element at subscribe time, so sharing a
-- dashboard never leaks data the viewer cannot read.
CREATE TABLE dashboards (
    id         uuid PRIMARY KEY,
    owner_id   bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    shared     boolean NOT NULL DEFAULT false,
    layout     jsonb NOT NULL DEFAULT '{"widgets": []}',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX dashboards_owner_idx ON dashboards (owner_id);
CREATE INDEX dashboards_shared_idx ON dashboards (shared) WHERE shared;

-- +goose Down
DROP TABLE dashboards;
