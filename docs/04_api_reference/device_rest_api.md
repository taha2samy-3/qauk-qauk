# Device REST API

For devices that can't hold a connection open: cellular modules that wake, send and sleep; serverless functions; shell scripts; anything with an HTTP client. It uses the same device JWT, elements, permissions and [rate limits](../05_core_concepts/rate_limits.md) as the [WebSocket](./device_api.md) and [gRPC](./device_grpc_api.md) transports, and messages from all three reach dashboards the same way.

![curl against the REST device API](../imgs/screenshots/transcript-rest-curl.webp)

## At a glance

| | |
|---|---|
| Base URL | `http://<host>:8080/device/v1` (`https://` behind TLS) |
| Auth | `Authorization: Bearer <jwt>`, the [same token](./device_api.md#the-token) as the WebSocket |
| Routing hint | `X-Quack-Device: <device id>` (optional; see [Behind a load balancer](#behind-a-load-balancer)) |
| Bodies | `application/json`, or `application/senml+json` ([SenML](#senml)) |
| Limits | body ≤ 1 MiB, 1–500 messages per request, message ≤ 64 KiB |

| Endpoint | Purpose |
|---|---|
| [`POST /device/v1/messages`](#send-messages) | Send one message or a batch |
| [`GET /device/v1/sync`](#receive-commands-sync) | Long-poll for commands from dashboards |
| `GET /device/v1/elements` | The device's elements (same answer as [`GET /device/elements`](./device_api.md#listing-elements)) |

For trying it locally, `quack dev token --device "Boiler room"` prints a token for a demo device (`task demo` creates them).

```sh
TOKEN=$(server/bin/quack dev token --file server/bin/demo-devices.json --device "Boiler room")
```

## Send messages

```sh
curl http://127.0.0.1:8080/device/v1/messages \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '[{"element": "Water temperature", "message": {"value": 71.5}, "id": "boiler-0001"},
       {"element": "Pressure", "message": {"value": 1.9}, "ts": "2026-10-09T10:00:00Z"}]'
```

The body is one message object, or an array of 1–500 of them.

| Field | Required | Notes |
|---|---|---|
| `element` | yes | The element's **id or name** (names are matched exactly; if two elements share a name, the oldest wins). |
| `message` | yes | Any JSON value, as on the WebSocket. See [message shapes](./device_api.md#telemetry-device-to-server). |
| `id` | no | Your message id (≤ 128 characters, no `/`). A retry with the same id within 10 minutes is acknowledged as `duplicate` and **not stored or delivered again**. |
| `ts` | no | Your own timestamp (RFC 3339). Stored as `client_ts`; viewers see the server's receive time. |

```json
{
  "results": [
    { "index": 0, "element_id": "01a1159f-…", "status": "accepted", "event_id": "01a121cf-8777-…" },
    { "index": 1, "element_id": "01a1159f-…", "status": "rejected", "code": "rate_limited",
      "error": "element rate limit exceeded, retry after 412ms" }
  ]
}
```

Every message gets a result, in order:

| `status` | Meaning |
|---|---|
| `accepted` | Published. `event_id` is its id in the history store. |
| `filtered` | Dropped by the element's [pipeline](../05_core_concepts/element_pipeline.md) (`deadband`, `drop_if`, or script returning `null`). Does not consume rate limit tokens, and is not stored or published. |
| `duplicate` | Same `id` seen recently; the earlier `event_id` is returned. Nothing is published. |
| `coalesced` | Over the element's limit, and the element keeps the latest value ([`over_limit: latest`](../05_core_concepts/rate_limits.md#over-the-limit-drop-or-keep-latest)). It replaces any waiting value and is published when the limit allows. |
| `pipeline_failed` | An [element pipeline](../05_core_concepts/element_pipeline.md) step failed (e.g. script exception, timeout). `code` is `pipeline_failed` and `error` contains the reason. Copied to `element-pipeline.dlq.v1`. |
| `rejected` | Not published. `code` is `invalid_message`, `too_large` (over 64 KiB), `unstorable` (NUL or a lone surrogate), `unknown_element` or `rate_limited`. |

| HTTP status | When |
|---|---|
| `200` | The batch was processed. Check each result. |
| `401` | Missing or invalid token (same rules as the [WebSocket handshake](./device_api.md#rejection)) |
| `403` | `X-Quack-Device` doesn't match the token |
| `413` | Body over 1 MiB |
| `422` | Not a message object or array, or not 1–500 messages, or invalid SenML |
| `429` + `Retry-After` | The whole batch is over the device's limit, or every message in it was rate limited |

Errors use one shape: `{"status": 422, "code": "invalid_body", "detail": "…"}`.

**Retries.** Give messages an `id` and retry on network errors and `429`/`5xx`. Ids are remembered by the gateway that handled the request. Behind a load balancer, [route each device to one instance](#behind-a-load-balancer) so retries reach it.

### SenML

With `Content-Type: application/senml+json`, the body is a [SenML](https://www.rfc-editor.org/rfc/rfc8428) (RFC 8428) JSON pack:

```sh
curl http://127.0.0.1:8080/device/v1/messages -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/senml+json' \
  -d '[{"bn": "", "bt": 1.796e9, "n": "Pressure", "v": 1.92, "u": "bar"},
       {"n": "Water temperature", "v": 71.9, "u": "Cel", "t": 10}]'
```

| SenML | Becomes |
|---|---|
| `bn` + `n` | the element name (or id) |
| `v` (+ `bv`), `vs`, `vb`, `vd`, `s` | `{"value": …}` |
| `u` or `bu` | `"unit"` in the message, e.g. `{"value": 1.92, "unit": "bar"}` |
| `bt` + `t` | the client timestamp. Values under 2²⁸ are relative to now (RFC 8428 §4.5.3). |

Records with only base fields are skipped. A named record without a value is a `422`.

## Receive commands (sync)

Dashboard commands (a switch, a slider) are delivered to REST devices by **long-polling**:

```sh
curl "http://127.0.0.1:8080/device/v1/sync?wait=30s&cursor=$CURSOR" -H "Authorization: Bearer $TOKEN"
```

```json
{
  "cursor": "eyIwMWExMTU5Zi1kM2JlLTc2…",
  "messages": [
    {
      "element": "Burner", "element_id": "01a1159f-d3be-…", "event_id": "01a121cf-…",
      "message": { "value": 1 },
      "auth": { "user_id": 1, "username": "admin" },
      "last_edit_at": "2026-10-09T17:58:01.512Z"
    }
  ]
}
```

| Parameter | Notes |
|---|---|
| `cursor` | The `cursor` of the previous answer. Omit it on the first call. Opaque; don't parse it. |
| `wait` | How long to hold the request if there is nothing new: seconds (`30`) or a duration (`30s`). Default `0` (answer at once), max `QUACK_SYNC_MAX_WAIT` (60 s). |

- **What comes back:** for each of the device's elements, the **newest** message written by someone else (a user) that is newer than the cursor. This is "desired state", not a queue: if a user toggles a switch three times while the device sleeps, the device gets the last value. The device's own telemetry never comes back.
- **Command inversion:** commands for elements with invertible [pipeline](../05_core_concepts/element_pipeline.md) steps (`scale`, `round`, `clamp`, `map`) are transformed with the pipeline's inverse so the device receives physical actuator units.
- **When it returns:** at once if something is waiting, otherwise as soon as a command arrives, or when `wait` runs out (with an empty list and the same cursor).
- **Loop:** call it again with the new cursor. Apply each command and send the new state back as telemetry; dashboards wait for that echo, as with the [actuator pattern](./device_api.md#commands-server-to-device).
- **Cold start:** a first call without a cursor returns the latest commands the gateway has seen since it started. To ignore those, make the first call with `wait=0` and only keep its cursor.

Every gateway instance sees every event, so any instance can answer a sync. That is why no sticky routing is needed for sync.

## Presence

A REST device counts as **connected** for `QUACK_PRESENCE_TTL` (30 s) after its last request, then disconnected. A device that should look online must call at least that often. A long-poll counts as a request when it starts. The presence lease is written once per device and gateway, not on every request.

## Behind a load balancer

Rate-limit buckets and retry ids are kept in memory per gateway instance (see [Rate limits](../05_core_concepts/rate_limits.md#several-gateway-instances)). Send `X-Quack-Device: <device id>` and let the load balancer hash on it, so every request of a device reaches the same instance:

```yaml
# ingress-nginx
nginx.ingress.kubernetes.io/upstream-hash-by: "$http_x_quack_device"
```

Envoy and most cloud load balancers can do the same with consistent hashing on a header. The gateway checks the header against the token (`403` if it differs), so a device can't spread its traffic over instances by lying about it.

## Measured

On a laptop (i7-7820HQ, 8 threads), with 20 devices on one instance and a dashboard on another, limits off:

| | Latency p50 / p99 | Messages/s |
|---|---|---|
| 1 message per request | 6.6 / 11.3 ms | 8 173 |
| 100 messages per request | 6.5 / 8.1 ms | 62 060 |

Batch when you can. Details in [Performance](../refactor/baseline.md#device-transports-2026-10-09).
