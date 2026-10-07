-- +goose Up
CREATE TABLE users (
    id            bigserial PRIMARY KEY,
    username      text NOT NULL UNIQUE CHECK (length(username) BETWEEN 1 AND 150),
    email         text NOT NULL DEFAULT '',
    password_hash text NOT NULL,
    is_active     boolean NOT NULL DEFAULT true,
    is_admin      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);

CREATE TABLE groups (
    id   bigserial PRIMARY KEY,
    name text NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 150)
);

CREATE TABLE user_groups (
    user_id  bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, group_id)
);
CREATE INDEX user_groups_group_idx ON user_groups (group_id);

CREATE TABLE sessions (
    token_hash   bytea PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    ip           text NOT NULL DEFAULT '',
    user_agent   text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE jwt_public_keys (
    id         uuid PRIMARY KEY,
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    pem        text NOT NULL,
    algorithm  text NOT NULL CHECK (algorithm IN ('RS256', 'ES256')),
    key_size   integer NOT NULL,
    is_active  boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE devices (
    id            uuid PRIMARY KEY,
    name          text NOT NULL CHECK (length(name) BETWEEN 1 AND 50),
    description   text NOT NULL DEFAULT '',
    public_key_id uuid REFERENCES jwt_public_keys(id) ON DELETE SET NULL
);
CREATE INDEX devices_key_idx ON devices (public_key_id);

CREATE TABLE elements (
    id          uuid PRIMARY KEY,
    device_id   uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 50),
    points      integer NOT NULL CHECK (points BETWEEN 0 AND 1000),
    description text NOT NULL DEFAULT '',
    details     jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX elements_device_idx ON elements (device_id);

CREATE TABLE element_styles (
    id         bigserial PRIMARY KEY,
    element_id uuid NOT NULL REFERENCES elements(id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    details    jsonb
);
CREATE INDEX element_styles_element_idx ON element_styles (element_id);

-- One table for user and group grants (replaces ElementPermissionsUser/Group; fixes id collision B7).
CREATE TABLE element_permissions (
    id         bigserial PRIMARY KEY,
    element_id uuid NOT NULL REFERENCES elements(id) ON DELETE CASCADE,
    user_id    bigint REFERENCES users(id) ON DELETE CASCADE,
    group_id   bigint REFERENCES groups(id) ON DELETE CASCADE,
    permission text NOT NULL CHECK (permission IN ('R', 'RC')),
    CHECK ((user_id IS NULL) <> (group_id IS NULL))
);
CREATE UNIQUE INDEX element_permissions_user_uq ON element_permissions (element_id, user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX element_permissions_group_uq ON element_permissions (element_id, group_id) WHERE group_id IS NOT NULL;
CREATE INDEX element_permissions_user_idx ON element_permissions (user_id);
CREATE INDEX element_permissions_group_idx ON element_permissions (group_id);

-- Live presence leases written by gateways (fixes stale "connected", B9).
CREATE TABLE device_presence (
    device_id    uuid NOT NULL,
    gateway_id   text NOT NULL,
    conn_id      text NOT NULL,
    connected_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (device_id, gateway_id, conn_id)
);
CREATE INDEX device_presence_gateway_idx ON device_presence (gateway_id);

-- Append-only connection audit log.
CREATE TABLE device_connections (
    id              uuid PRIMARY KEY,
    device_id       uuid NOT NULL,
    gateway_id      text NOT NULL,
    details         jsonb NOT NULL,
    connected_at    timestamptz NOT NULL DEFAULT now(),
    disconnected_at timestamptz
);
CREATE INDEX device_connections_device_idx ON device_connections (device_id, connected_at DESC);

-- Transactional outbox: rows are written in the same tx as the change they describe.
CREATE TABLE outbox (
    id           bigserial PRIMARY KEY,
    topic        text NOT NULL,
    key          text NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX outbox_unpublished_idx ON outbox (id) WHERE published_at IS NULL;

CREATE TABLE audit_log (
    id            bigserial PRIMARY KEY,
    actor_user_id bigint,
    actor_name    text NOT NULL,
    action        text NOT NULL,
    entity        text NOT NULL,
    entity_id     text NOT NULL,
    data          jsonb,
    at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_at_idx ON audit_log (at DESC);

-- +goose Down
DROP TABLE audit_log, outbox, device_connections, device_presence, element_permissions,
    element_styles, elements, devices, jwt_public_keys, sessions, user_groups, groups, users;
