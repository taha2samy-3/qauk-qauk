-- +goose NO TRANSACTION
-- +goose Up
-- The replay (Store.Last) and the history API (Store.Events) read one
-- element's events ORDER BY time DESC, event_id DESC. With only
-- (element_id, time DESC) the planner preferred the (time, event_id) unique
-- index and filtered by element: an element with few events in the window
-- scanned every chunk (millions of rows, seconds to minutes per dashboard
-- widget). This index matches the filter and the order exactly.
CREATE INDEX IF NOT EXISTS element_event_element_time_event_idx ON element_event (element_id, time DESC, event_id DESC);
DROP INDEX IF EXISTS element_event_element_time_idx;

-- +goose Down
CREATE INDEX IF NOT EXISTS element_event_element_time_idx ON element_event (element_id, time DESC);
DROP INDEX IF EXISTS element_event_element_time_event_idx;
