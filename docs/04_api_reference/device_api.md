# Device WebSocket API

This is the protocol for devices: firmware, gateways, scripts and Node-RED flows. It is the legacy "v1" protocol, kept byte-compatible with the Django server, so existing devices work unchanged. The fixes listed in [Changes from the legacy server](#changes-from-the-legacy-server) are the only differences.

The behavior below is pinned by the black-box contract suite ([`server/contracttest/README.md`](https://github.com/taha2samy/node_red_-_django-quack_quack-/blob/master/server/contracttest/README.md)).

## Connection

| | |
|---|---|
| URL | `ws://<host>:8080/device/node_red/` (`wss://` behind TLS). The path also works without the trailing slash. |
| Auth | HTTP header `Authorization: Bearer <jwt>` on the upgrade request |
| Origin | Not checked. Devices authenticate with the token, not with cookies. |
| Sockets per device | Any number. They all share the device's elements. |

### The token

The device signs the JWT with **its own private key**. The server verifies it with the public key assigned to the device.

| Claim | Required | Rule |
|---|---|---|
| `id` | yes | The device UUID as a **string**. The server uses it to look up the device and its key. |
| `exp` | yes | Expiry (seconds since epoch). An expired token is rejected (30 s clock-skew leeway). |
| `iat` | no | Issued-at. If present, `exp - iat` must be ≤ `QUACK_DEVICE_JWT_MAX_LIFETIME` (default `24h`, plus 30 s). Without it, `exp - now` must be. |

The header `alg` must equal the algorithm of the stored key: `RS256` for RSA ≥ 2048 bits, `ES256` for ECDSA P-256. The algorithm always comes from the database, never from the token, so `alg: none` and algorithm-swap attacks fail.

```json
{ "id": "f53b2639-b17e-5604-8767-17254ebaa351", "iat": 1791367200, "exp": 1791410400 }
```

### Rejection

If authentication fails, the upgrade is refused with **HTTP 403** `Device authentication failed`, and no WebSocket is opened. The reasons are not sent to the client; they are logged on the server and counted in `quack_ws_rejected_total{kind="device"}`. A token is rejected when:

- the `Authorization` header is missing, is not `Bearer`, or has an empty token
- the token is malformed, or `id` is missing or not a UUID
- the device does not exist, or has no key assigned
- the key is inactive
- the signature is wrong, or `alg` does not match the key
- `exp` is missing or in the past, or the lifetime exceeds the cap

The token is only checked during the handshake. An open socket stays open after the token expires; use a fresh token when you reconnect.

Examples: [Node.js with `jose`](../03_getting_started.md#device-example-in-javascript-jose), [Node-RED](../03_getting_started.md#node-red).

## Telemetry: device to server

One JSON object per text frame. There is **no `type` field**.

```json
{
  "element_id": "98994c94-71b8-53b0-85f3-d1c6483978de",
  "message": { "value": 22.7 }
}
```

| Field | Required | Notes |
|---|---|---|
| `element_id` | yes | UUID of an element that **belongs to this device**. |
| `message` | yes | Any JSON value. It is forwarded unchanged and stored as-is. |
| `last_edit_at` | no | Your own timestamp (RFC 3339). It is stored as `client_ts` for reference only. The timestamp viewers see is always the server's receive time. |
| `auth` | no | **Ignored.** The server sets the sender from the authenticated device (see [B3](#changes-from-the-legacy-server)). |

**Recommended `message` shapes.** These are the shapes the bundled web app understands:

| Element kind | Shape | Notes |
|---|---|---|
| Sensor, switch, slider | `{"value": 21.5}`, `{"value": 1}` | Switches use `0`/`1` (booleans also work). |
| Chart series | `{"value": 21.5}` or `{"x": "2026-10-07T10:00:00Z", "y": 21.5}` | Prefer `value`: see the note below. |

The TSDB extracts a numeric `value` for aggregates from `message.value`, or from a bare number or boolean message (`true` = 1). A message without one, such as `{"x", "y"}`, is still stored and replayed, but it does not appear in the per-minute aggregates that history charts use for long ranges.

**Silently dropped:** the socket stays open, nothing is sent back, and the drop is logged and counted in `quack_dropped_total`.

- invalid JSON, a missing `element_id` or `message`, or a non-UUID `element_id`
- an element that does not belong to this device, or has been deleted
- frames over the rate limit (`QUACK_DEVICE_MSG_RATE`, default 50/s per socket, burst 50)
- binary frames

## Commands: server to device

```json
{
  "element_id": "e5b1cb93-114e-5609-9dcf-2b7360375e5a",
  "message": { "value": 1 },
  "auth": { "user_id": 5, "username": "alice" },
  "last_edit_at": "2026-10-07T11:05:10.890Z"
}
```

A device socket receives every message for its elements that it did **not** send itself:

| Sender | `auth.user_id` | `auth.username` |
|---|---|---|
| A user with `RC` on the element (a command from a dashboard) | the user id, a JSON **number** | the username |
| Another socket of the **same device** (telemetry) | the device UUID, a JSON **string** | the device name |

`last_edit_at` is the server time in RFC 3339 UTC with milliseconds. The server never echoes a frame back to the socket that sent it.

**Actuator pattern.** Apply the command, then send the new state back as telemetry (`{"element_id": ..., "message": {"value": 1}}`). Dashboards treat that echo as confirmation; the switch widget waits for it.

## Live changes while connected

| Admin action | Effect on an open device socket |
|---|---|
| Element created on this device | Usable at once, no reconnect needed |
| Element deleted | Further frames for it are dropped |
| Device renamed or description edited | The socket stays open; new messages carry the new name |
| Device deleted | Closed with **4000** `device deleted`. Reconnects get 403. |
| Device's key unassigned | Closed with **4000** `device key removed` |
| Device switched to another key | Closed with **4000** `device key changed` |
| The key itself edited (renamed, activated, deactivated) or deleted | Closed with **1000** `key changed` (rotation semantics). Reconnect with a valid token; a deactivated or deleted key gets 403. |

## Close codes

| Code | Reason text | Meaning | What the device should do |
|---|---|---|---|
| 1000 | `key changed` | The device's key was edited or deleted | Reconnect (with backoff) |
| 1001 | `ping timeout` / `server shutting down` | No pong within 15 s of a ping (sent every 30 s), or the server is restarting | Reconnect with backoff |
| 1009 | | A frame was larger than 64 KiB | Send smaller frames |
| 1013 | `slow consumer` | More than 2048 frames were queued for this socket | Read faster, then reconnect |
| 4000 | `device deleted`, `device key removed`, `device key changed` | Access was revoked | Don't retry blindly; the next handshake will probably get 403 |

Standard WebSocket libraries answer pings automatically.

## Presence

A device counts as **connected** while at least one of its sockets is open, on any gateway. Subscribed browsers get `element_connection_status` events on each transition (see [Browser API](./browser_api.md)). If a gateway crashes, its devices are marked disconnected once their lease expires: about 30–40 s with the defaults. See [Architecture → Presence](../02_architecture.md#presence-leases-and-the-sweeper).

## Changes from the legacy server

These are deliberate fixes. Everything else matches the Django `NodeRedConsumer`.

| Ref | Before (Django) | Now (Go) |
|---|---|---|
| **B1** | A key marked inactive still authenticated devices. | Inactive keys are rejected (HTTP 403). |
| **B2** | `exp` was optional, so a token without it was valid forever. | `exp` is required, the lifetime is capped by `QUACK_DEVICE_JWT_MAX_LIFETIME` (24 h), and 30 s of clock skew is allowed. |
| **B3** | A device could set `auth.user_id`, `auth.username` and `last_edit_at` and impersonate a user. Device-originated frames otherwise said `"coming from Device"`. | The server stamps the sender: `auth = {"user_id": "<device uuid>", "username": "<device name>"}`. `last_edit_at` is server time, and a client-supplied value is stored only as `client_ts`. |
| **B9** | If the server crashed, the device stayed "connected" forever. | Presence uses leases with a heartbeat. Stale leases are swept, and subscribers are told the device is disconnected. |
| **B10** | Any update to the device's connection record (for example, an admin edit) closed the device socket. | Only real connect and disconnect events change presence. Renaming a device no longer disconnects it. |
| Close codes | Django closed with **1000** for both device delete and key changes, because its `4000` lost a race. | **4000** for device delete, key unassigned or switched (and for browser session revocation). **1000** only when the key itself is edited or deleted, as legacy key rotation did. |
| New limits | None | 64 KiB per frame, 50 msg/s per socket (excess dropped), 2048-frame outbound queue (1013 on overflow). |
| History | Replayed from an in-memory cache, lost on restart | Replayed from memory plus TimescaleDB, so values survive restarts. Device telemetry only; user commands are not replayed (same as before). |
