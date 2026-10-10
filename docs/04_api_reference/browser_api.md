# Browser WebSocket API

This is the realtime protocol for logged-in users. The bundled React app uses it (`web/src/realtime/`), and any other UI or service acting as a user can use it too. Frames are byte-compatible with the legacy Django `BrowserConsumer`, except for the fixes in [Changes from the legacy server](#changes-from-the-legacy-server).

## Connection

| | |
|---|---|
| URL | `ws://<host>:8080/browser/simple/` (`wss://` behind TLS). It also works without the trailing slash. |
| Auth | The `quack_session` cookie set by `POST /api/v1/auth/login` ([REST API](./rest_api.md#authentication)) |
| Origin | It must be absent, have the same host as the request's `Host`, or be listed in `QUACK_ALLOWED_ORIGINS` |

A browser sends the cookie and `Origin` automatically:

```js
const ws = new WebSocket(`${location.origin.replace(/^http/, 'ws')}/browser/simple/`)
ws.onopen = () => ws.send(JSON.stringify({ type: 'subscribe', element_id: '98994c94-71b8-53b0-85f3-d1c6483978de' }))
ws.onmessage = (e) => console.log(JSON.parse(e.data))
```

Non-browser clients send the cookie header themselves, for example `Cookie: quack_session=<token>`.

**Rejection.** The upgrade is refused with **HTTP 403** when the `Origin` is not allowed (this blocks cross-site WebSocket hijacking), when there is no session cookie, or when the session has expired or been revoked, or belongs to an inactive user.

## Client → server

Every frame is a JSON object with `type` and `element_id`.

### `subscribe`

```json
{ "type": "subscribe", "element_id": "98994c94-71b8-53b0-85f3-d1c6483978de" }
```

Starts live updates for the element. The server replies with a confirmation, then the history replay, then live frames (all described below). Subscribing again to an element you already follow sends a new confirmation and replays the history again.

### `unsubscribe`

```json
{ "type": "unsubscribe", "element_id": "98994c94-71b8-53b0-85f3-d1c6483978de" }
```

Stops updates for **this element only**. The server always confirms with `{"type":"unsubscribe","element_id":...,"unsubscribe":true}`, even if you were not subscribed.

### `message_element`

```json
{ "type": "message_element", "element_id": "e5b1cb93-114e-5609-9dcf-2b7360375e5a", "message": { "value": 1 } }
```

Sends a command to the device. `message` is any JSON value and is forwarded unchanged. You must be **subscribed** to the element with **`RC`** permission. The device, and every other subscriber of the element, receives it; the sending socket does not. It is stored in the history store with `source = 'user'`, but it is not part of the history replay. A message containing a NUL character (`\u0000`) or an unpaired UTF-16 surrogate is refused with `invalid_format`, because history stores can't keep it.

## Server → client

### Subscribe confirmation

```json
{
  "type": "subscribe",
  "element_id": "98994c94-71b8-53b0-85f3-d1c6483978de",
  "subscribed": true,
  "permissions": "RC",
  "details": { "title": "Temperature", "unit": "°C", "minValue": -10, "maxValue": 50 },
  "connected": true
}
```

| Field | Meaning |
|---|---|
| `permissions` | Your effective permission, `"R"` or `"RC"` (the highest of your direct and group grants) |
| `details` | The element's `details` JSON (`null` if unset) |
| `connected` | Whether the element's device is connected right now (on any gateway) |

### `message_element`

```json
{
  "type": "message_element",
  "element_id": "98994c94-71b8-53b0-85f3-d1c6483978de",
  "message": { "value": 75.3 },
  "auth": { "user_id": "f53b2639-b17e-5604-8767-17254ebaa351", "username": "Main Sensor Rig" },
  "last_edit_at": "2026-10-07T10:30:00.123Z"
}
```

This frame carries both live data and the history replay:

- **Replay.** Right after the confirmation you get up to `points` frames with the element's latest **device** messages, oldest → newest. They come from the gateway's memory merged with the [history store](../05_core_concepts/history.md), so they survive restarts. An element with `points = 0` still replays **its latest value** (one frame), when there is one. No live frame can arrive between the confirmation and the end of the replay. The replay uses ordinary `message_element` frames; there is no separate history frame type.
- **Live.** Device telemetry and other users' commands.
- **`auth`.** Set by the server, never by the client. For a device, `user_id` is the device UUID (a string) and `username` is the device name. For a user, `user_id` is the user id (a number) and `username` is their username.
- **`last_edit_at`.** The server time when the message was received, in RFC 3339 UTC with milliseconds.

### `unsubscribe`

```json
{ "type": "unsubscribe", "element_id": "...", "unsubscribe": true, "reason": "Permission revoked" }
```

You get this frame without asking (a forced unsubscribe) in two cases:

| `reason` | When |
|---|---|
| `Permission revoked` | You no longer have any grant on the element: a direct grant was removed, you left a group, a group was deleted, or your account changed |
| `Element deleted` | The element, or its device, was deleted |

An explicit unsubscribe has no `reason`. After a forced unsubscribe, no more frames arrive for that element.

### `permissions_update`

```json
{ "type": "permissions_update", "element_id": "...", "permissions": "RC" }
```

Your effective permission changed (`R` ↔ `RC`) while you stayed subscribed. It takes effect immediately; you don't need to re-subscribe. Losing all access is reported as a forced `unsubscribe`, never as `permissions: null`.

### `element_connection_status`

```json
{ "type": "element_connection_status", "status": "connected", "element_id": "..." }
```

The element's device connected (its first socket on any gateway) or disconnected (its last socket closed, or its lease expired after a crash). You get one frame per subscribed element of that device.

### `error`

```json
{ "type": "error", "error_code": "permission_denied", "description": "You do not have permission to access this element.", "element_id": "..." }
```

| `error_code` | Cause | `element_id` |
|---|---|---|
| `permission_denied` | `subscribe` to an element that doesn't exist, isn't a UUID, or that you have no grant on | yes |
| `unauthorized` | `message_element` without an `RC` subscription to that element | yes |
| `invalid_format` | The frame is not valid JSON (`description`: `invalid JSON message`), `element_id` is missing (`'element_id'`), `message` is missing in `message_element` (`'message'`), or `message` contains a NUL character or an unpaired surrogate (`'message' message contains …`) | for `message` problems |
| `unknown_type` | `type` is missing or unknown (`Unknown message type: <type>`, or `None` when missing) | no |
| `rate_limited` | More than `QUACK_BROWSER_MSG_RATE` frames per second (default 100, burst 100). The frame was discarded. | no |
| `delivery_failed` | Downlink command failed to reach the device transport (e.g. MQTT broker unreachable after retries, or downlink retry buffer full) | yes |

Errors never close the socket, and they never contain server internals.

## Close codes

| Code | Reason text | Meaning |
|---|---|---|
| 1001 | `ping timeout` / `server shutting down` | No pong within 15 s (pings every 30 s), or the server is restarting. Reconnect and re-subscribe. |
| 1009 | | A frame was larger than 64 KiB |
| 1013 | `slow consumer` | More than 2048 frames were queued. Reconnect. |
| 4000 | `session revoked` | Your account was deactivated or deleted, or your password was changed. Log in again. |

`POST /api/v1/auth/logout` deletes the session but does not close sockets that are already open. Close them on the client.

## Guarantees

- **Ordering.** Frames on one socket arrive in the order the gateway queued them. For each element, the order is confirmation → replay → live.
- **No echo.** Your own `message_element` is not sent back to you.
- **Live permissions.** Grants, group membership, user status and element deletion are re-evaluated on open sockets within about a second of the change ([Permissions](../05_core_concepts/permissions.md#live-re-evaluation)).
- **Reconnects.** The server keeps no subscription state for a closed socket. After reconnecting, subscribe again; the replay fills the gap up to `points` values. For longer gaps, use `GET /api/v1/elements/{id}/history`.

## Changes from the legacy server

| Ref | Before (Django) | Now (Go) |
|---|---|---|
| **B3** | Devices could forge `auth` and `last_edit_at`, and device frames said `"coming from Device"` | `auth` is the authenticated device (`user_id` = device UUID, `username` = device name), and `last_edit_at` is server time |
| **B4** | No Origin check, so any website could open the socket with the user's cookie (cross-site WebSocket hijacking) | Strict Origin check: same host or `QUACK_ALLOWED_ORIGINS`; otherwise HTTP 403 |
| **B5** | Some errors sent a Python traceback (`server_error`, about 6 KB) to the client | Generic error codes only; details stay in the server logs |
| **B6** | Unsubscribing from one element dropped the permission listeners of **all** your elements, so later revocations were missed | Subscriptions are tracked per element; unsubscribing touches only that element |
| **B8** | Group membership changes and new grants were not pushed live | Membership, grant, group and user changes re-evaluate open subscriptions (`permissions_update` or forced `unsubscribe`) |
| **B9**, **B10** | A server crash left devices "connected" forever, and unrelated admin edits produced spurious disconnects | Lease-based presence; `element_connection_status` reflects real connections only |
| `last_edit_at` from users | Other browsers received the string `"None"` for user commands | A real server timestamp |
| Element deleted | Subscribers got `reason: "Permission revoked"` (cascaded grants) | `reason: "Element deleted"` |
| `invalid_format` | Echoed the raw frame text in `details` | `description` only |
| Session revoked | No effect on open sockets | Deactivation, deletion or a password change closes the user's sockets with **4000** |
| New | | `rate_limited` error, 64 KiB frame limit, 2048-frame queue (1013) |
