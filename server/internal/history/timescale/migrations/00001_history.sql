-- +goose NO TRANSACTION
-- +goose Up
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Every element message (device telemetry and user commands), unchanged.
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
ALTER TABLE element_event SET (timescaledb.compress, timescaledb.compress_segmentby = 'element_id',
    timescaledb.compress_orderby = 'time DESC');

-- One row per numeric attribute of each event (history.Points).
CREATE TABLE IF NOT EXISTS element_point (
    time       timestamptz      NOT NULL,
    element_id uuid             NOT NULL,
    field      text             NOT NULL,
    value      double precision NOT NULL,
    event_id   uuid             NOT NULL,
    UNIQUE (time, event_id, field)
);
SELECT create_hypertable('element_point', 'time', chunk_time_interval => interval '1 day', if_not_exists => true);
CREATE INDEX IF NOT EXISTS element_point_element_field_time_idx ON element_point (element_id, field, time DESC);
ALTER TABLE element_point SET (timescaledb.compress, timescaledb.compress_segmentby = 'element_id, field',
    timescaledb.compress_orderby = 'time DESC');

-- 1-minute rollup per element and attribute. sum and n (not avg) so buckets
-- of any size re-aggregate exactly. Real-time: unrefreshed minutes are read
-- from element_point.
CREATE MATERIALIZED VIEW IF NOT EXISTS element_point_1m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT time_bucket('1 minute', time) AS bucket,
       element_id,
       field,
       sum(value) AS sum,
       min(value) AS min,
       max(value) AS max,
       count(*)   AS n
FROM element_point
GROUP BY 1, 2, 3
WITH NO DATA;

SELECT add_continuous_aggregate_policy('element_point_1m',
    start_offset => interval '3 days', end_offset => interval '1 minute',
    schedule_interval => interval '1 minute', if_not_exists => true);

-- The previous schema kept a value-only aggregate; element_point_1m replaces it.
DROP MATERIALIZED VIEW IF EXISTS element_value_1m;

-- +goose Down
DROP MATERIALIZED VIEW IF EXISTS element_point_1m;
DROP TABLE IF EXISTS element_point;
DROP TABLE IF EXISTS element_event;
