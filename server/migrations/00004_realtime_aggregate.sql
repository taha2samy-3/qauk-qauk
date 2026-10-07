-- +goose Up
-- TimescaleDB >= 2.13 creates continuous aggregates as materialized-only.
-- Enable real-time aggregation so the newest (not yet refreshed) minutes are included.
ALTER MATERIALIZED VIEW element_value_1m SET (timescaledb.materialized_only = false);

-- +goose Down
ALTER MATERIALIZED VIEW element_value_1m SET (timescaledb.materialized_only = true);
