-- +goose Up
-- Live gateways and their roles, for spreading MQTT connections (HRW).
CREATE TABLE gateway_members (
    gateway_id text PRIMARY KEY,
    roles      text[] NOT NULL,
    weight     real NOT NULL DEFAULT 1 CHECK (weight > 0),
    last_seen  timestamptz NOT NULL DEFAULT now()
);
-- Capture mode of an uplink rule: received messages go to mqtt-capture.v1 until then.
ALTER TABLE mqtt_uplinks ADD COLUMN capture_until timestamptz;
ALTER TABLE mqtt_uplinks ALTER COLUMN qos SET DEFAULT 1;

-- +goose Down
ALTER TABLE mqtt_uplinks ALTER COLUMN qos SET DEFAULT 0;
ALTER TABLE mqtt_uplinks DROP COLUMN capture_until;
DROP TABLE gateway_members;
