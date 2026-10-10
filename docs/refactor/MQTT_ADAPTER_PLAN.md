# MQTT adapter: plan and status

The user guide is [MQTT connections](../10_mqtt.md). This page records what was planned, what was built, and where the build differs from the plan.

## The design in one paragraph

MQTT runs inside the gateway as the `mqtt` role; Quack Quack is an MQTT 5 **client** (a subscriber), never a broker. Each connection has `replicas` slots, and slot *n* connects with the fixed client id `<prefix>-<n>`. Gateways publish themselves in `gateway_members` on the presence heartbeat, and each one computes the same **weighted rendezvous hash (top-K)** over the live members to decide which slots it owns, with no leader or lock. The broker's session takeover on the shared client id fences out a stale owner, and persistent sessions (`clean_start=false`, QoS 1, acknowledged only once the values are durable) mean nothing is lost on handover. Messages go through the **source pipeline** (an optional TTN/ChirpStack-compatible JavaScript decoder, then a declarative field map) and then the same **device core** as WebSocket, REST and gRPC. Configuration is a snapshot per connection on the compacted topic `mqtt-config.v1`, written through the transactional outbox.

## Status

| Phase | Scope | Status |
|---|---|---|
| A | Schema (`00006_mqtt.sql`, `00007_mqtt_cluster.sql`), `mqtt-config.v1` through the outbox (row lock + database clock version, tombstones), service validation, admin REST API | Done |
| B | Source pipeline (`internal/sourcepipe`): goja sandbox, TTN/ChirpStack contract, field map, limits; a vendored TTN codec in the tests | Done |
| C | Membership (`gateway_members`), weighted HRW (`internal/cluster`), slots on autopaho, uplink with manual acknowledgement, grants, DLQ, capture, status | Done |
| D | Downlinks (slot 0 owner), replicas with shared subscriptions, password and mTLS authentication | Done |
| E | Admin UI (`web/src/features/admin/mqtt`), Mosquitto in the dev stack (`task start MQTT=1`) and CI, the Cold room demo, integration tests, docs, screenshots | Done |
| F | Splitting roles into separate deployments by configuration only | Designed for, not built |

## Tests

- `internal/mqttspec`, `internal/sourcepipe`, `internal/cluster`, `internal/gateway/mqtt` (including an import boundary test: the transport doesn't import the store, database, service, history or API packages).
- `integrationtest/mqtt_test.go` against a real Mosquitto: uplink to a browser with `quackvia` and presence; decoder, grants and DLQ; downlink round trip; HRW assignment and failover with no loss; replicas on a shared subscription; password, mTLS and a refused rogue certificate; live configuration changes and the role opt-in; the admin API and capture.
- `web/e2e/mqtt.spec.ts`: the Cold room dashboard, a command round trip, and the admin screenshots.

## Deviations from the plan

| Planned | Built | Why |
|---|---|---|
| Secrets encrypted in the database (AES-256-GCM) | **References only** (`env:`, `file:`), never values | Nothing secret is stored at all, which is simpler and safer than encrypting; the API refuses plain values. |
| SCRAM and JWT broker authentication | None, password, mTLS | MQTT 5 enhanced authentication isn't supported consistently by brokers; JWT-as-password works through the password method with a `file:` reference. |
| Values refused by a rate limit retried later | Dead-lettered with the reason | Holding acknowledgements to retry would stall the session; the DLQ makes it visible. |
| A hash per slot | Top-K owners per connection | Slots of one connection then always sit on different gateways. A join can shift a connection's slot order; persistent sessions make that move lossless. |
| Device presence from MQTT not specified | Online while messages arrive (`QUACK_PRESENCE_TTL`), like REST | Dashboards showed MQTT devices as offline and disabled their controls. |
| Downlink encoder syntax open | `{"template": …}` with `"{{value}}"` / `"{{message}}"`, or a decoder's `encodeDownlink` | Covers JSON devices without code and binary ones with the same decoder as the uplink. |

## Open items

- Phase F: separate deployments per role.
- Per-connection metrics labels are by connection id; a dashboard of MQTT metrics isn't provided yet.
- Decoder version history is stored (`decoder_versions`) but the UI doesn't offer a rollback yet.
