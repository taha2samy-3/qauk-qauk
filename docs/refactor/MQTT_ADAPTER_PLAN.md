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

Everything else follows the task prompt as agreed, including the assignment itself: weighted rendezvous hashing, top K per connection (`score = -weight / ln(u)`, the `replicas` best members own slots `0..K-1` in score order), recomputed on every heartbeat and every `mqtt-config.v1` change, fenced by the broker's session takeover.

| Agreed | Built | Why |
|---|---|---|
| Secrets as `env:`/`file:` references, **or** typed in the UI and encrypted (AES-256-GCM) | References only; the API refuses plain values | Nothing secret is stored at all. Encryption at rest can be added later without changing the API. |
| JWT as password (re-signed on every reconnect); SCRAM-SHA-256 optional | Not built: none, password, mTLS | A static token works through the password method with a `file:` reference, but it isn't re-signed. Open item. |
| Rate-limited values held and retried after `retry_after`, to the DLQ after 30 s | Dead-lettered at once with the reason | Simpler and never stalls the session; the retry is an open item. |
| `not_granted` counted in `quack_dropped_total{reason="mqtt_not_granted"}`; metrics labelled by connection **and** slot | Counted in `quack_mqtt_rejected_total{reason="not_granted"}`; metrics labelled by connection (and reason/result) | One MQTT-specific counter family. |
| HRW hash input `connection_id + "/" + gateway_id` | `connection_id + "\x00" + gateway_id` | Same algorithm; the separator can't appear in an id. |
| Decoder UI with version history, rollback and "paste a TTN codec" | Editor with the version number; history is stored (`decoder_versions`) | Rollback UI is an open item; pasting a codec works in the editor. |
| EMQX test profile (optional) | Not built; tests use Mosquitto | Optional in the prompt. |
| Device presence not specified | An MQTT device is online while its messages arrive (`QUACK_PRESENCE_TTL`), like REST | Without it, dashboards showed MQTT devices offline and disabled their switches. |

## Open items

- The MQTT benchmark for [baseline.md](./baseline.md): MQTT → dashboard latency (p50/p95/p99) and sustained messages/s through Mosquitto, with and without a decoder, with 1 and 3 gateways.
- JWT-as-password with re-signing, SCRAM, and the rate-limit retry (above).
- Decoder rollback in the UI.
- Phase F: separate deployments per role.
