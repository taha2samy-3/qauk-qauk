-- +goose Up

CREATE TABLE element_alert_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    element_id uuid NOT NULL REFERENCES elements(id) ON DELETE CASCADE,
    name text NOT NULL,
    condition text NOT NULL CHECK (condition IN ('above', 'below', 'outside_range', 'equals')),
    threshold double precision NOT NULL DEFAULT 0,
    threshold_max double precision,
    hysteresis double precision NOT NULL DEFAULT 0,
    severity text NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    message text NOT NULL DEFAULT '',
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX element_alert_rules_element_id_idx ON element_alert_rules (element_id);

-- +goose Down
DROP TABLE IF EXISTS element_alert_rules;
