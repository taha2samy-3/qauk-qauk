-- +goose Up

CREATE TABLE mqtt_connections (
    id text PRIMARY KEY,
    name text NOT NULL,
    broker_url text NOT NULL,
    client_id_prefix text NOT NULL,
    keepalive integer NOT NULL DEFAULT 60,
    session_expiry integer NOT NULL DEFAULT 3600,
    receive_maximum integer NOT NULL DEFAULT 100,
    replicas integer NOT NULL DEFAULT 1,
    auth jsonb NOT NULL DEFAULT '{}'::jsonb,
    tls jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE decoders (
    id text PRIMARY KEY,
    name text NOT NULL,
    language text NOT NULL DEFAULT 'javascript',
    source text NOT NULL,
    version integer NOT NULL DEFAULT 1,
    updated_by text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE decoder_versions (
    decoder_id text NOT NULL REFERENCES decoders(id) ON DELETE CASCADE,
    version integer NOT NULL,
    source text NOT NULL,
    updated_by text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (decoder_id, version)
);

CREATE TABLE mqtt_uplinks (
    id text PRIMARY KEY,
    connection_id text NOT NULL REFERENCES mqtt_connections(id) ON DELETE CASCADE,
    topic_filter text NOT NULL,
    qos integer NOT NULL DEFAULT 0,
    format text NOT NULL DEFAULT 'json',
    decoder_id text REFERENCES decoders(id) ON DELETE SET NULL,
    device jsonb NOT NULL,
    field_map jsonb NOT NULL DEFAULT '[]'::jsonb,
    "time" text,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mqtt_downlinks (
    id text PRIMARY KEY,
    connection_id text NOT NULL REFERENCES mqtt_connections(id) ON DELETE CASCADE,
    device_external_id text NOT NULL,
    element text NOT NULL,
    topic_template text NOT NULL,
    encoder jsonb NOT NULL,
    qos integer NOT NULL DEFAULT 0,
    retain boolean NOT NULL DEFAULT false,
    content_type text,
    message_expiry integer,
    response_topic text,
    user_properties jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mqtt_connection_devices (
    connection_id text NOT NULL REFERENCES mqtt_connections(id) ON DELETE CASCADE,
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    external_id text NOT NULL,
    PRIMARY KEY (connection_id, device_id),
    UNIQUE (connection_id, external_id)
);

CREATE TABLE mqtt_connection_status (
    connection_id text NOT NULL REFERENCES mqtt_connections(id) ON DELETE CASCADE,
    slot integer NOT NULL,
    gateway_id text NOT NULL,
    connected boolean NOT NULL DEFAULT false,
    reason_code integer,
    last_error text,
    counters jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (connection_id, slot)
);

-- +goose Down
DROP TABLE mqtt_connection_status, mqtt_connection_devices, mqtt_downlinks, mqtt_uplinks, decoder_versions, decoders, mqtt_connections;
