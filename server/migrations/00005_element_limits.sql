-- +goose Up
-- Per-element rate limits (NULL = server default) and what happens to
-- messages over the limit: 'drop' them, or keep only the 'latest' and send it
-- when the bucket refills.
ALTER TABLE elements
    ADD COLUMN msg_rate   double precision CHECK (msg_rate IS NULL OR msg_rate > 0),
    ADD COLUMN msg_burst  integer CHECK (msg_burst IS NULL OR msg_burst >= 1),
    ADD COLUMN over_limit text NOT NULL DEFAULT 'drop' CHECK (over_limit IN ('drop', 'latest'));

-- Tombstones for compacted topics (device-config.v1) have no payload.
ALTER TABLE outbox ALTER COLUMN payload DROP NOT NULL;

-- +goose Down
DELETE FROM outbox WHERE payload IS NULL;
ALTER TABLE outbox ALTER COLUMN payload SET NOT NULL;
ALTER TABLE elements DROP COLUMN over_limit, DROP COLUMN msg_burst, DROP COLUMN msg_rate;
