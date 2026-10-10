# 6. Database schema

Two stores, both migrated by `quack migrate`:

- **PostgreSQL 17** holds identity, devices, permissions, dashboards, runtime state, the outbox and the audit log. Its schema is the goose migrations in `server/migrations/`, embedded in the binary.
- **The history store** holds the telemetry time series: TimescaleDB (the default, which can be the same Postgres) or ClickHouse. Each driver owns its schema. See [History store](#history-store) below and [History storage](../05_core_concepts/history.md).

| Core migration | Contents |
|---|---|
| `00001_core.sql` | Identity, devices, permissions, presence, connections, outbox, audit log |
| `00003_dashboards.sql` | `dashboards` |
| `00005_element_limits.sql` | Per-element rate limits (`elements.msg_rate`, `msg_burst`, `over_limit`); `outbox.payload` nullable for tombstones |

Migrations 00002 and 00004 used to create the time series in the core schema. They now live in the TimescaleDB driver (`server/internal/history/timescale/migrations/`, version table `goose_history_version`). A database that already ran them upgrades in place: the existing `element_event` table is reused, and the old `element_value_1m` aggregate is replaced.

## Entity-relationship diagram

Solid lines are foreign keys. Dotted lines are logical references without a foreign key: time-series and runtime rows must not block deletes, and must survive them.

```mermaid
erDiagram
  users {
    bigserial id PK
    text username UK "1-150 chars"
    text email
    text password_hash "argon2id or Django pbkdf2"
    boolean is_active
    boolean is_admin
    timestamptz created_at
    timestamptz last_login_at
  }
  groups {
    bigserial id PK
    text name UK
  }
  user_groups {
    bigint user_id PK, FK
    bigint group_id PK, FK
  }
  sessions {
    bytea token_hash PK "SHA-256 of the cookie"
    bigint user_id FK
    timestamptz created_at
    timestamptz expires_at
    timestamptz last_seen_at
    text ip
    text user_agent
  }
  jwt_public_keys {
    uuid id PK
    text name
    text pem
    text algorithm "RS256 or ES256"
    integer key_size
    boolean is_active
    timestamptz created_at
  }
  devices {
    uuid id PK
    text name "1-50 chars"
    text description
    uuid public_key_id FK "nullable, SET NULL"
  }
  elements {
    uuid id PK
    uuid device_id FK
    text name "1-50 chars"
    integer points "0-1000, replay window"
    text description
    jsonb details "widget config"
    timestamptz created_at
    float8 msg_rate "nullable: server default"
    integer msg_burst "nullable: = rate"
    text over_limit "drop | latest"
  }
  element_styles {
    bigserial id PK
    uuid element_id FK
    text name
    jsonb details
  }
  element_permissions {
    bigserial id PK
    uuid element_id FK
    bigint user_id FK "exactly one of user_id"
    bigint group_id FK "or group_id"
    text permission "R or RC"
  }
  dashboards {
    uuid id PK
    bigint owner_id FK
    text name
    boolean shared
    jsonb layout
    timestamptz created_at
    timestamptz updated_at
  }
  device_presence {
    uuid device_id PK
    text gateway_id PK
    text conn_id PK
    timestamptz connected_at
    timestamptz last_seen_at
  }
  device_connections {
    uuid id PK
    uuid device_id
    text gateway_id
    jsonb details
    timestamptz connected_at
    timestamptz disconnected_at
  }
  element_event {
    timestamptz time "history store (own schema)"
    uuid event_id "CloudEvent id"
    uuid element_id
    uuid device_id
    text source "device or user"
    text actor_id
    text actor_name
    timestamptz client_ts
    jsonb payload
    float8 value
  }
  outbox {
    bigserial id PK
    text topic
    text key
    jsonb payload "full CloudEvent, NULL = tombstone"
    timestamptz created_at
    timestamptz published_at
  }
  audit_log {
    bigserial id PK
    bigint actor_user_id
    text actor_name
    text action
    text entity
    text entity_id
    jsonb data
    timestamptz at
  }

  users ||--o{ user_groups : ""
  groups ||--o{ user_groups : ""
  users ||--o{ sessions : ""
  jwt_public_keys |o--o{ devices : "assigned to"
  devices ||--o{ elements : owns
  elements ||--o{ element_styles : ""
  elements ||--o{ element_permissions : ""
  users |o--o{ element_permissions : "direct grant"
  groups |o--o{ element_permissions : "group grant"
  users ||--o{ dashboards : owns
  devices ||..o{ device_presence : "live leases"
  devices ||..o{ device_connections : "connection audit"
  elements ||..o{ element_event : "history store (no FK)"
  users |o..o{ audit_log : actor
```

**Delete behavior.**

- Deleting a **user** cascades to their sessions, memberships, direct grants and dashboards.
- Deleting a **group** cascades to its memberships and grants.
- Deleting a **device** cascades to its elements, and from there to styles and grants.
- Deleting a **key** sets `devices.public_key_id` to `NULL`, so those devices can no longer connect.
- `device_connections`, `device_presence` and `audit_log` keep their rows. The history store keeps a deleted element's events until retention drops them (it has no foreign keys into Postgres).

**Constraints worth knowing:**

- `element_permissions` has `CHECK ((user_id IS NULL) <> (group_id IS NULL))`, plus partial unique indexes on `(element_id, user_id)` and `(element_id, group_id)`. There is one grant per subject per element, and the API upserts it.
- `elements.points` is constrained to `BETWEEN 0 AND 1000`.
- `elements.msg_rate > 0` and `msg_burst >= 1` when set, and `over_limit IN ('drop', 'latest')`. The API also caps the rate at `QUACK_ELEMENT_MSG_RATE_MAX`. See [Rate limits](../05_core_concepts/rate_limits.md).
- `jwt_public_keys.algorithm` is constrained to `IN ('RS256', 'ES256')`.

**IDs.** New devices, elements, keys, dashboards and connection rows get **UUIDv7** ids (time-ordered). Rows imported from Django keep their original ids. Users and groups use `bigserial`, so imported Django user ids are preserved too.

## Runtime and infrastructure tables

| Table | Written by | Purpose |
|---|---|---|
| `device_presence` | Gateways | One lease per open device socket. Refreshed every `QUACK_PRESENCE_HEARTBEAT`; dead after `QUACK_PRESENCE_TTL`. The sweeper deletes expired rows under an advisory lock. See [Architecture → Presence](../02_architecture.md#presence-leases-and-the-sweeper). |
| `device_connections` | Gateways | An append-only audit of connections (client address, path, user agent, gateway, connection id), with `disconnected_at` set on close. Readable at `GET /api/v1/admin/connections`. |
| `outbox` | Every admin mutation (same transaction) | Events waiting to be published: control events (`control-events.v1`), and full device snapshots for any change to a device, its key or its elements (`device-config.v1`; a `NULL` payload is a tombstone for a deleted device). A partial index on unpublished rows keeps the relay query cheap. One relay drains at a time (advisory lock), so rows of a key are published in order. Published rows are purged after 7 days. |
| `audit_log` | Every admin mutation (same transaction) | Who changed what, with the new state in `data`. Readable at `GET /api/v1/admin/audit`. |
| `sessions` | Login | Only token hashes. Expired rows are purged hourly. |

## History store

The same three tables in every driver:

| Table | One row per | Purpose |
|---|---|---|
| `element_event` | element message | The message unchanged (`payload`), the server receive `time`, `event_id` (UUIDv7, the idempotency key), `source` (`device`/`user`), the server-stamped actor, the device's own `client_ts`, and the main numeric `value` |
| `element_point` | numeric attribute of a message | `(time, element_id, field, value, event_id)`, where `field` is a path such as `temperature`, `gps.lat` or `sensors[0].temp` |
| `element_point_1m` | element, field and minute | `sum`, `min`, `max`, `n`, re-bucketed into any step by the history API |

Retention (`QUACK_HISTORY_RETENTION`, default 365 days) applies to all three and is re-applied by every `quack migrate`.

### TimescaleDB driver

```sql
CREATE TABLE element_event (
    time timestamptz NOT NULL, event_id uuid NOT NULL, element_id uuid NOT NULL, device_id uuid NOT NULL,
    source text NOT NULL CHECK (source IN ('device', 'user')),
    actor_id text NOT NULL, actor_name text NOT NULL, client_ts timestamptz,
    payload jsonb NOT NULL, value double precision,
    UNIQUE (time, event_id)
);
SELECT create_hypertable('element_event', 'time', chunk_time_interval => interval '1 day');
CREATE INDEX element_event_element_time_idx ON element_event (element_id, time DESC);
ALTER TABLE element_event SET (timescaledb.compress,
    timescaledb.compress_segmentby = 'element_id', timescaledb.compress_orderby = 'time DESC');

CREATE TABLE element_point (
    time timestamptz NOT NULL, element_id uuid NOT NULL, field text NOT NULL,
    value double precision NOT NULL, event_id uuid NOT NULL,
    UNIQUE (time, event_id, field)
);
SELECT create_hypertable('element_point', 'time', chunk_time_interval => interval '1 day');
CREATE INDEX element_point_element_field_time_idx ON element_point (element_id, field, time DESC);
ALTER TABLE element_point SET (timescaledb.compress,
    timescaledb.compress_segmentby = 'element_id, field', timescaledb.compress_orderby = 'time DESC');

CREATE MATERIALIZED VIEW element_point_1m
WITH (timescaledb.continuous, timescaledb.materialized_only = false) AS
SELECT time_bucket('1 minute', time) AS bucket, element_id, field,
       sum(value) AS sum, min(value) AS min, max(value) AS max, count(*) AS n
FROM element_point GROUP BY 1, 2, 3 WITH NO DATA;
SELECT add_continuous_aggregate_policy('element_point_1m',
    start_offset => interval '3 days', end_offset => interval '1 minute', schedule_interval => interval '1 minute');
```

- **Idempotency.** The unique keys plus `ON CONFLICT DO NOTHING`. Points are written only for events that were actually inserted, in the same transaction, so the rollup never counts a redelivered event twice.
- **Real-time aggregation** (`materialized_only = false`): queries combine materialized minutes with raw points not yet materialized, so charts include the newest minute. When late events arrive (more than 2 minutes old: catch-up after downtime, or a backfill), the driver refreshes exactly the affected range, because real-time aggregation only covers data newer than the watermark.
- **Compression** applies to chunks older than `QUACK_HISTORY_COMPRESS_AFTER` (7 days). It is segmented by element (and field), so per-element queries stay fast. Retention policies drop chunks, and rollup buckets, after `QUACK_HISTORY_RETENTION`.
- `element_point_1m` stores `sum` and `n` rather than `avg`, so buckets of any size re-aggregate exactly: `sum(sum) / sum(n)`.

### ClickHouse driver

```sql
CREATE TABLE element_event (
    time DateTime64(3, 'UTC') CODEC(Delta, ZSTD), event_id UUID, element_id UUID, device_id UUID,
    source LowCardinality(String), actor_id String, actor_name String,
    client_ts Nullable(DateTime64(3, 'UTC')), payload String CODEC(ZSTD(3)), value Nullable(Float64)
) ENGINE = ReplacingMergeTree PARTITION BY toYYYYMM(time) ORDER BY (element_id, time, event_id)
  TTL toDateTime(time) + toIntervalSecond(<retention>);

CREATE TABLE element_point (
    time DateTime64(3, 'UTC') CODEC(Delta, ZSTD), element_id UUID, field LowCardinality(String),
    value Float64 CODEC(Gorilla, ZSTD), event_id UUID
) ENGINE = ReplacingMergeTree PARTITION BY toYYYYMM(time) ORDER BY (element_id, field, time, event_id)
  TTL toDateTime(time) + toIntervalSecond(<retention>);

CREATE TABLE element_point_1m (
    bucket DateTime('UTC'), element_id UUID, field LowCardinality(String),
    sum SimpleAggregateFunction(sum, Float64), min SimpleAggregateFunction(min, Float64),
    max SimpleAggregateFunction(max, Float64), n SimpleAggregateFunction(sum, UInt64)
) ENGINE = AggregatingMergeTree PARTITION BY toYYYYMM(bucket) ORDER BY (element_id, field, bucket)
  TTL bucket + toIntervalSecond(<retention>);

CREATE MATERIALIZED VIEW element_point_1m_mv TO element_point_1m AS
SELECT toStartOfMinute(time) AS bucket, element_id, field,
       sum(value) AS sum, min(value) AS min, max(value) AS max, count() AS n
FROM element_point GROUP BY bucket, element_id, field;
```

- **Sorting keys start with the element**, so per-element reads touch few granules.
- **`payload` is text**, returned byte for byte.
- **Duplicates.** `ReplacingMergeTree` collapses duplicate events on merge, and raw reads use `FINAL`. The materialized view counts every inserted row, so `Append` first checks which events are already stored and inserts only new ones. It writes points before events, so a crash between the two is repaired by the redelivery.
- **Rollup at insert.** The materialized view fills `element_point_1m` as rows are inserted, so late data is included without any refresh.
- **Retention.** A changed `QUACK_HISTORY_RETENTION` is applied with `ALTER TABLE … MODIFY TTL` on the next `quack migrate`.

## Useful queries

```sql
-- latest 10 values of an element (history store: TimescaleDB)
SELECT time, actor_name, payload FROM element_event
WHERE element_id = '<uuid>' ORDER BY time DESC LIMIT 10;

-- hourly averages of an attribute for the last day (TimescaleDB)
SELECT time_bucket('1 hour', bucket) AS hour, sum(sum) / sum(n) AS avg
FROM element_point_1m WHERE element_id = '<uuid>' AND field = 'temperature' AND bucket > now() - interval '1 day'
GROUP BY 1 ORDER BY 1;

-- the same in ClickHouse
-- SELECT toStartOfHour(bucket) AS hour, sum(sum) / sum(n) AS avg FROM element_point_1m
-- WHERE element_id = '<uuid>' AND field = 'temperature' AND bucket > now() - INTERVAL 1 DAY GROUP BY hour ORDER BY hour;

-- devices connected right now
SELECT DISTINCT device_id FROM device_presence WHERE last_seen_at > now() - interval '30 seconds';

-- effective permission of user 7 on an element (same rule as the server)
SELECT max(ep.permission) FROM element_permissions ep JOIN users u ON u.id = 7 AND u.is_active
WHERE ep.element_id = '<uuid>'
  AND (ep.user_id = 7 OR ep.group_id IN (SELECT group_id FROM user_groups WHERE user_id = 7));
```

To connect in local dev: `psql postgres://quack:quack@127.0.0.1:5433/quack`. With `HISTORY=clickhouse`: `clickhouse client --port 19000 --user quack --password quack --database quack`, or the HTTP interface on http://127.0.0.1:18123/play.

## MQTT connections

Migrations `00006_mqtt.sql` and `00007_mqtt_cluster.sql`. The tables are the source of truth for [MQTT connections](../10_mqtt.md); every change is published, in the same transaction, as a snapshot of the whole connection to the compacted topic `mqtt-config.v1` (through the outbox), which is what the gateways read.

| Table | Holds |
|---|---|
| `mqtt_connections` | One broker connection: `broker_url`, `client_id_prefix` (slot *n* connects as `<prefix>-<n>`), `keepalive`, `session_expiry`, `receive_maximum`, `replicas`, `auth` and `tls` (JSON with **secret references** such as `env:NAME` or `file:/path`, never secret values), `enabled`. |
| `mqtt_connection_devices` | The grants: which devices a connection may write, and the `external_id` each one has in topics or payloads (unique per connection). |
| `mqtt_uplinks` | Rules: `topic_filter`, `qos`, `format` (`json`, `text`, `number`, `bytes`), an optional `decoder_id`, `device` (where the external id comes from: a topic level, a payload field or a fixed value), `field_map`, `time`, `enabled`, and `capture_until` (raw messages are copied to `mqtt-capture.v1` until then). |
| `mqtt_downlinks` | Commands to the broker: `device_external_id` + `element`, `topic_template` (`{device}`, `{element}`, `{user}`), `encoder` (a payload template or a decoder's `encodeDownlink`), `qos`, `retain` and MQTT 5 properties. |
| `decoders`, `decoder_versions` | JavaScript decoders and every saved version of each. |
| `mqtt_connection_status` | Per connection and slot: the owning `gateway_id`, `connected`, the last MQTT reason code and error, and counters. Written by the owning gateway. |
| `gateway_members` | Live gateways: `roles`, `weight` (`QUACK_MQTT_WEIGHT`) and `last_seen`, refreshed with the presence heartbeat. Members seen within `QUACK_PRESENCE_TTL` share the MQTT slots. |
