-- +goose Up

-- The message attribute a rule watches, named as history stores it: "value"
-- (the element's value), "temperature", "gps.lat", "sensors[0].t".
ALTER TABLE element_alert_rules ADD COLUMN field text NOT NULL DEFAULT 'value';

-- +goose Down
ALTER TABLE element_alert_rules DROP COLUMN IF EXISTS field;
