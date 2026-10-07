-- +goose NO TRANSACTION
-- +goose Up
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Every element message ever produced (device telemetry and user commands).
CREATE TABLE IF NOT EXISTS element_event (
    time       timestamptz NOT NULL,
    event_id   uuid        NOT NULL,
    element_id uuid        NOT NULL,
    device_id  uuid        NOT NULL,
    source     text        NOT NULL CHECK (source IN ('device', 'user')),
    actor_id   text        NOT NULL,
    actor_name text        NOT NULL,
    client_ts  timestamptz,
    payload    jsonb       NOT NULL,
    value      double precision,
    UNIQUE (time, event_id)
);
SELECT create_hypertable('element_event', 'time', chunk_time_interval => interval '1 day', if_not_exists => true);
CREATE INDEX IF NOT EXISTS element_event_element_time_idx ON element_event (element_id, time DESC);

ALTER TABLE element_event SET (timescaledb.compress, timescaledb.compress_segmentby = 'element_id', timescaledb.compress_orderby = 'time DESC');
SELECT add_compression_policy('element_event', interval '7 days', if_not_exists => true);
SELECT add_retention_policy('element_event', interval '365 days', if_not_exists => true);

CREATE MATERIALIZED VIEW IF NOT EXISTS element_value_1m WITH (timescaledb.continuous) AS
SELECT time_bucket('1 minute', time) AS bucket,
       element_id,
       avg(value) AS avg,
       min(value) AS min,
       max(value) AS max,
       count(*)   AS n
FROM element_event
WHERE value IS NOT NULL
GROUP BY 1, 2
WITH NO DATA;

SELECT add_continuous_aggregate_policy('element_value_1m',
    start_offset => interval '2 hours', end_offset => interval '1 minute',
    schedule_interval => interval '1 minute', if_not_exists => true);

-- +goose Down
DROP MATERIALIZED VIEW IF EXISTS element_value_1m;
DROP TABLE IF EXISTS element_event;
