-- +goose Up

-- Per-format settings (Telegram's chat_id).
ALTER TABLE webhook_endpoints ADD COLUMN options jsonb NOT NULL DEFAULT '{}'::jsonb;

-- A claimed delivery is leased: a crashed or stopped worker's claims expire
-- and are taken again.
ALTER TABLE webhook_deliveries ADD COLUMN locked_until timestamptz;
ALTER TABLE webhook_deliveries ALTER COLUMN max_attempts SET DEFAULT 8;
UPDATE webhook_deliveries SET status = 'retrying', locked_until = NULL WHERE status = 'delivering';
-- one delivery per endpoint and event (an alert transition is enqueued once)
CREATE UNIQUE INDEX webhook_deliveries_endpoint_event_key ON webhook_deliveries (endpoint_id, event_id);
-- the claim takes the oldest open delivery of each endpoint
CREATE INDEX webhook_deliveries_open_idx ON webhook_deliveries (endpoint_id, created_at, id)
    WHERE status IN ('pending', 'retrying', 'delivering');
CREATE INDEX webhook_deliveries_purge_idx ON webhook_deliveries (created_at) WHERE status IN ('delivered', 'failed');

-- Whether each alert rule is firing, so a restart neither repeats an alert
-- nor misses its recovery. Changed only together with the deliveries it
-- causes (one transaction).
CREATE TABLE element_alert_state (
    rule_id    uuid PRIMARY KEY REFERENCES element_alert_rules(id) ON DELETE CASCADE,
    firing     boolean NOT NULL,
    last_value double precision,
    changed_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS element_alert_state;
DROP INDEX IF EXISTS webhook_deliveries_purge_idx;
DROP INDEX IF EXISTS webhook_deliveries_open_idx;
DROP INDEX IF EXISTS webhook_deliveries_endpoint_event_key;
ALTER TABLE webhook_deliveries ALTER COLUMN max_attempts SET DEFAULT 5;
ALTER TABLE webhook_deliveries DROP COLUMN IF EXISTS locked_until;
ALTER TABLE webhook_endpoints DROP COLUMN IF EXISTS options;
