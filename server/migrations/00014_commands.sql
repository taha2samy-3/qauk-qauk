-- +goose Up

-- Commands sent through the integration API (REST or stream), with what
-- became of them. Dashboard commands aren't tracked. The id is the command's
-- element event id. Rows are purged after QUACK_COMMAND_RETENTION.
CREATE TABLE commands (
    id         uuid PRIMARY KEY,
    element_id uuid NOT NULL REFERENCES elements(id) ON DELETE CASCADE,
    user_id    bigint REFERENCES users(id) ON DELETE SET NULL,
    actor      text NOT NULL,
    message    jsonb NOT NULL,
    status     text NOT NULL DEFAULT 'accepted' CHECK (status IN ('accepted', 'delivered', 'failed', 'superseded')),
    reason     text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX commands_created_at_idx ON commands (created_at);

-- +goose Down
DROP TABLE IF EXISTS commands;
