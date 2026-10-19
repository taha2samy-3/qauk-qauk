-- +goose Up

-- Machine users: accounts for integrations. They have no password (no
-- login), are never admins, and get element grants like any user.
ALTER TABLE users ADD COLUMN kind text NOT NULL DEFAULT 'human' CHECK (kind IN ('human', 'service'));
ALTER TABLE users ADD CONSTRAINT users_service_not_admin CHECK (kind = 'human' OR NOT is_admin);

-- API tokens of machine users. Only the SHA-256 of a token is stored; the
-- prefix lets people recognize one. Deleting a row revokes the token.
CREATE TABLE api_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    prefix       text NOT NULL,
    token_hash   bytea NOT NULL UNIQUE,
    scopes       text[] NOT NULL CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['read', 'control', 'alerts']),
    expires_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    created_by   text NOT NULL DEFAULT '',
    last_used_at timestamptz
);
CREATE INDEX api_tokens_user_id_idx ON api_tokens (user_id);

-- +goose Down
DROP TABLE IF EXISTS api_tokens;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_service_not_admin;
ALTER TABLE users DROP COLUMN IF EXISTS kind;
