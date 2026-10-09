# Plan: Device Adapters (REST + gRPC), Element Rate Limits, Device Registry

> **Audience:** whoever implements or reviews this work (human or agent).
> **Status:** approved 2026-10-09. The implementation status table at the end is kept up to date.
> **Scope:** device-facing transports only. Alerts and webhooks are out of scope (see the last section).

## 1. Goals

1. Devices can talk to the platform over **three transports** with the same rules: the existing WebSocket protocol v1, a new **REST** API, and a new **gRPC** API.
2. Rate limits are set **per element** (an element is one data stream), not per socket.
3. Device authentication no longer reads Postgres on the hot path. A device-config snapshot lives on a **compacted Redpanda topic** that every gateway keeps in memory.
4. Nothing changes for existing devices: the WebSocket wire protocol, URL and JWT stay exactly the same.

## 2. Where device data goes (unchanged)

```
device ─► gateway ─► element-events.v1 (key = element_id) ─► ingester ─► history store
                                                                     └─► element-state.v1
```

Postgres is **not** on this path. It holds configuration (devices, elements, keys, permissions) and the presence leases.

| Topic | Writer | Key | Content |
|---|---|---|---|
| `element-events.v1` | gateway | `element_id` | every element message (CloudEvents) |
| `presence.v1` | gateway | `device_id` | device connected / disconnected |
| `element-state.v1` (compacted) | ingester | `element_id` | latest stored device message |
| `element-events.dlq.v1` | ingester | `element_id` | messages the history store rejected |
| `control-events.v1` | outbox | entity | "something changed" notifications |
| **`device-config.v1`** (compacted, new) | outbox | `device_id` | full device snapshot (identity, key, elements, limits) |

## 3. Design

### 3.1 Device core

One transport-agnostic core inside the gateway. Every transport calls it.

```
            ┌─ WebSocket  /device/node_red/          (protocol v1, unchanged)
device core ┼─ REST       /device/v1/...
            └─ gRPC       /quack.device.v1.DeviceService/...
```

- `Authenticate(token)`: verifies the device JWT against the in-memory registry and returns a session (device + elements).
- `Publish(session, element, message, clientTS, clientID)`, in this order:
  1. device guard,
  2. `ValidateMessage`,
  3. ownership (by element id or name),
  4. element limit,
  5. publish.

  It returns a typed error: `invalid`, `unstorable`, `unknown_element`, `rate_limited`.
- **Error mapping:**
  - WebSocket keeps today's behaviour: drop the message and count the metric. Protocol v1 has no device error frames.
  - REST maps the error to an HTTP status.
  - gRPC maps it to a gRPC code.
- **`clientID`:** an optional client message id. It becomes the event id `UUIDv5(device, clientID)`, so a retried request is stored once: history appends are idempotent by id. The gateway also remembers recent ids, so a retry is not fanned out twice.

### 3.2 Element rate limits

New columns on `elements`:

| Column | Meaning |
|---|---|
| `msg_rate real NULL` | messages per second; `NULL` = server default |
| `msg_burst integer NULL` | bucket size; `NULL` = same as the rate (min 1) |
| `over_limit text DEFAULT 'drop'` | `drop` = discard extra messages; `latest` = keep only the newest pending value and send it when a token frees up |

`latest` fits sensor readings: dashboards keep showing the newest value and the history store doesn't fill up.

| Setting | Default | Meaning |
|---|---|---|
| `QUACK_ELEMENT_MSG_RATE` | 50 | default element rate |
| `QUACK_ELEMENT_MSG_RATE_MAX` | 1000 | highest rate an admin can set |
| `QUACK_DEVICE_MSG_RATE` | 500 | fixed per-device guard, also covers frames that name no valid element |

Only admins can change element limits: element CRUD is admin-only. A change applies to connected devices at once, through `device-config.v1`.

### 3.3 Rate limiting across instances

| Transport | Is one device's traffic spread over instances? | Local limiter exact? |
|---|---|---|
| WebSocket | no: one long connection | yes |
| gRPC | no: streams stay on one instance (recycled every 30 min) | yes |
| REST | yes: every request may hit another instance | no: up to N× with N instances |

**Decision:**

1. A `ratelimit.Limiter` interface with a **local** in-memory driver (token buckets, idle buckets evicted). Selected with `QUACK_RATELIMIT_DRIVER=local`.
2. REST requests carry `X-Quack-Device: <device id>`. The server rejects the request if it doesn't match the JWT, so a device can't spread itself by lying. The ingress hashes on it:
   - ingress-nginx: `nginx.ingress.kubernetes.io/upstream-hash-by: "$http_x_quack_device"`
   - Envoy: ring hash on the header

   With hashing, all requests of a device land on one instance and the local limiter is exact again.
3. A **Valkey** driver (GCRA in one Lua script, token leasing to cut round-trips) is an optional later step. It is only needed for strict quotas such as billing or multi-tenant limits. If Valkey is down it falls back to local limits rather than blocking all devices.

**Why not Valkey now:** the limit protects the platform; it is not billing. WS and gRPC are already exact, and REST becomes exact with hashing. A shared store would add an infrastructure component, a network round-trip on every message, and a failure mode to every message, to fix a problem that hashing already solves.

### 3.4 `device-config.v1`: Redpanda as a replicated cache

Redpanda can't answer "get key X" (it is a log), but a compacted topic read fully by every instance works as a replicated in-memory cache. This is the same pattern as `element-state.v1` and the GlobalKTable in Kafka Streams.

- **Value:** a CloudEvent `io.quack.device.config.v1` whose data is:

  ```json
  {"device": {"id", "name"}, "key": {"id", "pem", "algorithm", "active"} | null,
   "elements": [{"id", "name", "points", "rate", "burst", "over_limit"}], "version"}
  ```

- **Writer:** the service layer, in the same transaction as the change, through the outbox. The snapshot is built after taking `SELECT … FOR UPDATE` on the device row, so concurrent changes to one device are serialized and their outbox ids are in commit order.
- **Delete:** a deleted device gets a tombstone (`payload NULL` → Kafka record with a null value).
- **Relay:** one drainer at a time (`pg_try_advisory_xact_lock`), so rows for one key are published in id order even with several API replicas.
- **Backfill:** `quack migrate` publishes a snapshot for every device. The same thing is available as `quack registry sync`.
- **Reader:** every gateway reads the topic from the start and follows it. `/readyz` reports not ready until the first full read is done.
  - On a miss (a device created a moment ago), it reads Postgres once and caches the answer. "Not found" is cached for 10 s, so made-up ids can't hammer the database.
- **Size:** about 2–3 KB per device, so 100k devices ≈ 250 MB per gateway. Past that, switch to an on-demand cache.

`control-events.v1` stays as it is. Gateways still use it for permissions and browsers.

### 3.5 REST adapter

Everything lives under `/device/v1/`, with the same JWT in `Authorization: Bearer`.

| Endpoint | Purpose |
|---|---|
| `POST /device/v1/messages` | batch `[{element, message, id?, ts?}]` or `application/senml+json` |
| `GET /device/v1/elements` | the device's elements (the old `/device/elements` stays) |
| `GET /device/v1/sync?cursor=&wait=30s` | long-poll: newest value per element written by others (users) since the cursor |

- **Limits:**
  - body ≤ 1 MiB
  - batch ≤ 500 items
  - message ≤ 64 KiB, as on the WebSocket
- **Responses:**

  | Status | When |
  |---|---|
  | `200` | per-item results |
  | `401` / `403` | authentication failed |
  | `413` | body too large |
  | `422` | bad request body |
  | `429` + `Retry-After` | every item was rate limited |

- **SenML (RFC 8428):**
  - `bn` + `n` is the element name.
  - `v` / `vs` / `vb` / `vd` become `{"value": …}`.
  - `bt` + `t` becomes the client timestamp.
- **Sync:**
  - The cursor is opaque. It holds the last event id per element; event ids are UUIDv7, so they order by time.
  - If something newer is already in memory, the call returns at once. Otherwise it waits for a delivery, up to `wait` (max 60 s).
  - Any instance can answer, because every gateway sees every event.
- **Presence:**
  - A REST device counts as online for `QUACK_PRESENCE_TTL` after its last request.
  - The lease in Postgres is written once per device and gateway, and refreshed by the existing heartbeat, not on every request.

### 3.6 gRPC adapter

- **Schema:** `proto/quack/device/v1/device.proto`. Code is generated with `buf` (pinned in `mise.toml`, `task proto:gen`), and the generated code is committed.
- **Library:** [connect-go](https://connectrpc.com/). One handler serves gRPC, gRPC-Web and the Connect protocol on the main HTTP server. Unencrypted HTTP/2 (h2c) is enabled so gRPC works behind a TLS-terminating proxy.
- **Methods:**

  | Method | Kind | Purpose |
  |---|---|---|
  | `Publish` | unary | batch, same rules as REST |
  | `Watch` | server stream | element values written by others, as they happen |
  | `Session` | bidi stream | WebSocket-equivalent: publish and receive on one stream |
  | `ListElements` | unary | the device's elements |

- **Payloads:** values are `google.protobuf.Value`.
- **Errors:**

  | Code | When |
  |---|---|
  | `RESOURCE_EXHAUSTED` | rate limited |
  | `UNAUTHENTICATED` | bad or missing token |
  | `NOT_FOUND` | foreign or unknown element |
  | `INVALID_ARGUMENT` | invalid message |

- **Stream lifetime:** a stream is closed with `UNAVAILABLE` after 30 min + jitter, so clients reconnect and spread over new instances.
- **Presence:** an open stream counts as a connection (like a socket).

## 4. Phases

| Phase | Content | Done when |
|---|---|---|
| 0 | element limits (schema, API, web form), `ratelimit` local driver, `device-config.v1` + registry, device core, WebSocket on the core | all existing suites green unchanged; auth reads no Postgres; per-element limits enforced |
| 1 | REST adapter | contract scenarios pass over REST; retries don't duplicate |
| 2 | gRPC adapter | same scenarios over gRPC; `buf lint` in CI |
| 3 | docs, screenshots, benchmark | docs complete; numbers published |
| later | Valkey limiter driver; presence off Postgres; alerts (evaluator on Redpanda, `alert-state.v1` compacted + `alert-events.v1`, history in the history store, rules in Postgres) | separate plans |

## 5. Behaviour changes

- The WebSocket limit moves from "per socket" to "per element + per-device guard". A device with two sockets now shares one budget.
- `QUACK_DEVICE_MSG_RATE` changes meaning: it was the per-socket rate (default 50), and is now the per-device guard (default 500).

## 6. Implementation status

| Phase | Status | Notes |
|---|---|---|
| 0 | in progress | |
| 1 | not started | |
| 2 | not started | |
| 3 | not started | |
