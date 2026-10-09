# Realtime events

Everything that moves between server processes goes over Redpanda as a **CloudEvents 1.0** JSON event. This page is for people who operate the bus or write their own consumers, for example for analytics, alerting or an AI agent. Browsers and devices never see these events directly; the gateway translates them into the [WebSocket frames](../04_api_reference/browser_api.md).

## Topics

There are six topics, all created by `quack migrate`. Topic auto-creation is disabled in the bundled Redpanda config. No topic is ever created per device or per element.

| Topic | Partitions | Retention | Key | Event type | Producers | Consumers |
|---|---|---|---|---|---|---|
| `element-events.v1` | 12 | 7 days | `element_id` | `io.quack.element.message.v1` | gateways | every gateway (broadcast), `quack ingest` (consumer group) |
| `control-events.v1` | 3 | 7 days | entity id | `io.quack.control.changed.v1` | outbox relay (`api` role) | every gateway (broadcast) |
| `presence.v1` | 3 | compacted | `device_id` | `io.quack.device.presence.v1` | gateways | every gateway (broadcast) |
| `element-state.v1` | 12 | compacted | `element_id` | `io.quack.element.message.v1` (the newest stored device message) | `quack ingest`, after each batch | every gateway, read in full on start to warm the latest values |
| `device-config.v1` | 3 | compacted (tombstones kept 1 day) | `device_id` | `io.quack.device.config.v1` (a full device snapshot); a deleted device gets a tombstone | outbox relay, in the transaction of every device, element or key change | every gateway, read in full on start and followed ([below](#device-config-v1)) |
| `element-events.dlq.v1` | 3 | 30 days | `element_id` | the original record, unchanged, plus an `error` header with the reason and a `source` header (topic/partition/offset) | `quack ingest`, for events the history store rejected | operators, to inspect and replay |

Keying by `element_id` keeps each element's messages in order. Gateways read all partitions from the current end, without a consumer group. The ingester reads in a consumer group from the earliest retained offset. You can watch the topics live with `task console` (http://127.0.0.1:8090).

## Envelope

Records use the CloudEvents **Kafka binding in structured mode**: the record value is the whole event, the header `content-type` is `application/cloudevents+json`, and the record key is `partitionkey`.

```json
{
  "specversion": "1.0",
  "id": "01926f6e-8a4b-7c1d-9a55-3f0e2c1b7a90",
  "source": "/quack/gateway/gw-7f3a",
  "type": "io.quack.element.message.v1",
  "subject": "98994c94-71b8-53b0-85f3-d1c6483978de",
  "time": "2026-10-07T10:00:00.130Z",
  "datacontenttype": "application/json",
  "dataschema": "urn:quack:schema:io.quack.element.message.v1",
  "partitionkey": "98994c94-71b8-53b0-85f3-d1c6483978de",
  "data": { "...": "see below" }
}
```

| Attribute | Value |
|---|---|
| `id` | A UUIDv7 (time-ordered). Consumers use it as the idempotency key; the history store keeps each `id` once. |
| `source` | `/quack/gateway/<QUACK_GATEWAY_ID>` or `/quack/api` |
| `subject` / `partitionkey` | The entity the event is about (element, device, or the changed entity's id) |
| `time` | Server clock, RFC 3339 UTC, millisecond precision. Ties are broken by `id`. |
| `dataschema` | The `$id` of the JSON Schema in [`server/schemas/`](https://github.com/taha2samy-3/qauk-qauk/tree/main/server/schemas), embedded in the binary |

Versioning: an additive change (a new optional field) keeps the type. A breaking change gets a new `.v2` type **and** a new topic, and producers write to both during the migration. Background: [MESSAGE_FORMATS.md](../refactor/MESSAGE_FORMATS.md).

## Element messages

Type `io.quack.element.message.v1`. Every telemetry message and every user command, exactly once per message:

```json
{
  "element_id": "98994c94-71b8-53b0-85f3-d1c6483978de",
  "device_id": "f53b2639-b17e-5604-8767-17254ebaa351",
  "source": "device",
  "actor": { "id": "f53b2639-b17e-5604-8767-17254ebaa351", "name": "Main Sensor Rig" },
  "origin": { "gateway_id": "gw-7f3a", "conn_id": "c-000123" },
  "message": { "value": 75.3 },
  "client_ts": "2026-10-07T10:00:00.123Z"
}
```

| Field | Meaning |
|---|---|
| `source` | `device` (telemetry) or `user` (a command from a browser) |
| `actor` | Stamped by the gateway from the authenticated identity: the device UUID and name, or the user id (as a string) and username |
| `origin` | The gateway and connection that received it. Gateways use it to skip their own events (local fast path) and to avoid echoing to the sender. |
| `message` | The client's payload, unchanged |
| `client_ts` | The device's own `last_edit_at`, if it sent a valid one; otherwise `null`. For reference only. |

## Control events

Type `io.quack.control.changed.v1`. These are written to the `outbox` table **in the same transaction** as the change. The relay in the `api` role publishes them, triggered by `LISTEN/NOTIFY quack_outbox` with a 1 s fallback poll. Rows are claimed with `FOR UPDATE SKIP LOCKED` and marked published after the broker acks; published rows are purged after 7 days. Control events carry **ids only**, so consumers must re-read Postgres.

```json
{ "kind": "permission", "op": "update", "id": "42", "element_id": "98994c94-…", "user_id": 7 }
```

| `kind` | Emitted on | `op` | Extra fields | Gateway reaction |
|---|---|---|---|---|
| `device` | Device create, update, delete | all | `device_id` | delete: close its sockets (4000). update: close the sockets if the key was unassigned, switched or is inactive (4000); otherwise pick up the new name. |
| `element` | Element create, update, delete (and each element of a deleted device) | all | `element_id`, `device_id` | create: connected sockets of that device may publish to it at once. update: new `points`. delete: drop it; subscribers get `unsubscribe` "Element deleted". |
| `permission` | Grant create, update, delete | all | `element_id`, `user_id` or `group_id` | Re-check affected subscriptions ([Permissions](./permissions.md#live-re-evaluation)) |
| `group_membership` | Member added or removed; group deleted (once per member) | `create`, `delete` | `user_id`, `group_id` | Re-check all of that user's subscriptions |
| `user` | User create, update, delete | all | `user_id` | Close the user's sockets (4000) if the session is gone; otherwise re-check their subscriptions |
| `jwt_key` | Key update, delete | `update`, `delete` | | Close every device socket using that key (1000 `key changed`) |

Renaming a group, creating a key and editing styles emit no control event (an audit row only). Every admin mutation, including those, writes `audit_log` in the same transaction.

## device-config.v1

Type `io.quack.device.config.v1`, one snapshot per device:

```json
{
  "device": { "id": "01a1159f-d3a3-728e-85f6-2092dd7c17b7", "name": "Boiler room" },
  "key": { "id": "01a1159f-…", "pem": "-----BEGIN PUBLIC KEY-----…", "algorithm": "RS256", "active": true },
  "elements": [
    { "id": "01a1159f-d3a6-…", "name": "Water temperature", "points": 100, "rate": 2, "burst": null, "over_limit": "latest" }
  ],
  "version": 1791568123456789
}
```

This topic is a **replicated cache**. Redpanda can't answer "get key X", but a compacted topic that every gateway reads in full and keeps following works as one (the same idea as a Kafka Streams GlobalKTable). With it, device authentication, element lookup by name and [rate limits](./rate_limits.md) need no database round-trip on any device transport.

- **Written** by the service layer in the same transaction as the change, through the outbox. A transaction first takes `SELECT … FOR UPDATE` on the device row, so snapshots of one device are built in commit order. The outbox relay drains with one relay at a time (an advisory lock), so the newest snapshot is always the last record for its key, even with several API replicas.
- **`version`** (microseconds) grows with every change. A reader ignores a snapshot older than the one it holds.
- **Deleted devices** get a tombstone (`null` value). `quack dev seed`, which truncates tables, writes tombstones for the devices it removed.
- **Backfill:** `quack migrate` republishes every device (also available as `quack registry sync`), and so does `quack import-django` after an import.
- **Reading:** a gateway reports not ready on `/readyz` until it has read the topic up to its end offsets at start (at most 30 s). A device not found in memory (created a moment ago) is read from Postgres once; "not found" is cached for 10 s.
- **Size:** about 2–3 KB per device, so 100,000 devices take roughly 250 MB per gateway.

`control-events.v1` is unchanged: it still tells gateways *that* something changed (permissions, users, sockets to close), while `device-config.v1` carries *what* a device looks like now.

## Presence events

Type `io.quack.device.presence.v1`:

```json
{ "device_id": "f53b2639-b17e-5604-8767-17254ebaa351", "connected": true, "gateway_id": "gw-7f3a" }
```

A gateway emits this only on a **transition**:

- `connected: true` when a device goes from no live lease to one.
- `connected: false` when its last lease is removed on disconnect, or expired by the sweeper after a gateway crash.

The topic is compacted, so it keeps the latest state per device. The source of truth is the `device_presence` lease table; see [Architecture → Presence](../02_architecture.md#presence-leases-and-the-sweeper). Gateways turn these events into `element_connection_status` frames for subscribers of the device's elements.

## Writing your own consumer

- Use your **own consumer group** and treat the stream as at-least-once: deduplicate on the CloudEvent `id`.
- For historical queries, use the history API or read the history store (`element_event`, `element_point`, `element_point_1m` in TimescaleDB or ClickHouse) instead of replaying the topic, which keeps only 7 days. For the latest value of every element, read the compacted `element-state.v1`.
- Never produce to these topics from outside the platform. Element events must come through a gateway (authentication, actor stamping), and control events through the API (outbox and audit). To act on devices, use the normal APIs with a dedicated user and an `RC` grant, so RBAC and auditing apply.
