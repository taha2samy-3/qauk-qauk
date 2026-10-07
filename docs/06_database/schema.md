# 6. Database schema

One PostgreSQL database (17, with the TimescaleDB extension) holds everything: identity, devices, permissions, dashboards, runtime state, the outbox, the audit log and the telemetry time series. The schema is defined by the goose migrations in `server/migrations/`, which are embedded in the binary and applied by `quack migrate`:

| Migration | Contents |
|---|---|
| `00001_core.sql` | Identity, devices, permissions, presence, connections, outbox, audit log |
| `00002_timeseries.sql` | The `element_event` hypertable, compression, retention, the `element_value_1m` continuous aggregate |
| `00003_dashboards.sql` | `dashboards` |
| `00004_realtime_aggregate.sql` | Turns on real-time aggregation for `element_value_1m` |

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
    timestamptz time "hypertable dimension"
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
    jsonb payload "full CloudEvent"
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
  elements ||..o{ element_event : "time series"
  users |o..o{ audit_log : actor
```

**Delete behavior.**

- Deleting a **user** cascades to their sessions, memberships, direct grants and dashboards.
- Deleting a **group** cascades to its memberships and grants.
- Deleting a **device** cascades to its elements, and from there to styles and grants.
- Deleting a **key** sets `devices.public_key_id` to `NULL`, so those devices can no longer connect.
- `element_event`, `device_connections`, `device_presence` and `audit_log` keep their rows.

**Constraints worth knowing:**

- `element_permissions` has `CHECK ((user_id IS NULL) <> (group_id IS NULL))`, plus partial unique indexes on `(element_id, user_id)` and `(element_id, group_id)`. There is one grant per subject per element, and the API upserts it.
- `elements.points` is constrained to `BETWEEN 0 AND 1000`.
- `jwt_public_keys.algorithm` is constrained to `IN ('RS256', 'ES256')`.

**IDs.** New devices, elements, keys, dashboards and connection rows get **UUIDv7** ids (time-ordered). Rows imported from Django keep their original ids. Users and groups use `bigserial`, so imported Django user ids are preserved too.

## Runtime and infrastructure tables

| Table | Written by | Purpose |
|---|---|---|
| `device_presence` | Gateways | One lease per open device socket. Refreshed every `QUACK_PRESENCE_HEARTBEAT`; dead after `QUACK_PRESENCE_TTL`. The sweeper deletes expired rows under an advisory lock. See [Architecture → Presence](../02_architecture.md#presence-leases-and-the-sweeper). |
| `device_connections` | Gateways | An append-only audit of connections (client address, path, user agent, gateway, connection id), with `disconnected_at` set on close. Readable at `GET /api/v1/admin/connections`. |
| `outbox` | Every admin mutation (same transaction) | Control events waiting to be published. A partial index on unpublished rows keeps the relay query cheap. Published rows are purged after 7 days. |
| `audit_log` | Every admin mutation (same transaction) | Who changed what, with the new state in `data`. Readable at `GET /api/v1/admin/audit`. |
| `sessions` | Login | Only token hashes. Expired rows are purged hourly. |

## Time series: TimescaleDB

### `element_event` hypertable

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
```

- **One row per element message**, written by `quack ingest` from `element-events.v1`. That covers device telemetry (`source = 'device'`) and user commands (`source = 'user'`).
- `time` is the gateway's receive time, which is authoritative. `client_ts` is what the device claimed.
- `actor_id` and `actor_name` are the server-stamped sender: a device UUID and name, or a user id and username.
- `payload` is the original `message`. `value` is the number extracted for aggregates: `message.value`, a bare number, or a boolean as 1/0. Otherwise it is `NULL`.
- **Idempotency.** `UNIQUE (time, event_id)` (the time column must be part of any unique index on a hypertable) plus `ON CONFLICT DO NOTHING` makes re-delivery and full topic replays harmless.
- **Chunks** are 1 day each. The `(element_id, time DESC)` index serves both the history replay (newest `points` device rows) and range queries.

### Compression and retention

```sql
ALTER TABLE element_event SET (timescaledb.compress,
    timescaledb.compress_segmentby = 'element_id', timescaledb.compress_orderby = 'time DESC');
SELECT add_compression_policy('element_event', interval '7 days');
SELECT add_retention_policy('element_event', interval '365 days');
```

- Chunks older than **7 days** are compressed, segmented by element so that per-element queries stay fast.
- Chunks older than **365 days** are dropped.
- To change either, run `remove_*_policy` and `add_*_policy` in a new migration.

### Continuous aggregate `element_value_1m` (real-time)

```sql
CREATE MATERIALIZED VIEW element_value_1m WITH (timescaledb.continuous) AS
SELECT time_bucket('1 minute', time) AS bucket, element_id,
       avg(value) AS avg, min(value) AS min, max(value) AS max, count(*) AS n
FROM element_event WHERE value IS NOT NULL GROUP BY 1, 2 WITH NO DATA;

SELECT add_continuous_aggregate_policy('element_value_1m',
    start_offset => interval '2 hours', end_offset => interval '1 minute', schedule_interval => interval '1 minute');

ALTER MATERIALIZED VIEW element_value_1m SET (timescaledb.materialized_only = false);  -- 00004
```

- A background job materializes 1-minute buckets every minute. It covers the window from 2 hours ago up to 1 minute ago.
- `materialized_only = false` turns on **real-time aggregation**: queries combine the materialized buckets with raw rows that are not materialized yet. Charts therefore include the latest minute. Migration 00004 is needed because TimescaleDB 2.13+ creates continuous aggregates as materialized-only.
- `GET /api/v1/elements/{id}/history` with any `step` other than `raw` (`1m`, `5m`, `15m`, `1h`, `1d`) re-buckets this view. It computes `avg` weighted by `n`, `min`/`max` of the bucket mins and maxes, and `sum(n)`. `step=raw` reads `element_event`.
- Only rows with a numeric `value` are aggregated.

## Useful queries

```sql
-- latest 10 values of an element
SELECT time, actor_name, payload FROM element_event
WHERE element_id = '<uuid>' ORDER BY time DESC LIMIT 10;

-- hourly averages for the last day
SELECT time_bucket('1 hour', bucket) AS hour, sum(avg * n) / sum(n) AS avg
FROM element_value_1m WHERE element_id = '<uuid>' AND bucket > now() - interval '1 day'
GROUP BY 1 ORDER BY 1;

-- devices connected right now
SELECT DISTINCT device_id FROM device_presence WHERE last_seen_at > now() - interval '30 seconds';

-- effective permission of user 7 on an element (same rule as the server)
SELECT max(ep.permission) FROM element_permissions ep JOIN users u ON u.id = 7 AND u.is_active
WHERE ep.element_id = '<uuid>'
  AND (ep.user_id = 7 OR ep.group_id IN (SELECT group_id FROM user_groups WHERE user_id = 7));
```

To connect in local dev: `psql postgres://quack:quack@127.0.0.1:5433/quack`.
