# Integration API Plan (API tokens, REST and a realtime stream)

A public API so that systems outside the platform can do what the web app does: read devices' values and history, send commands, and hear alerts, without being part of the platform. Use cases: a custom dashboard on its own site, an external automation (a script, Node-RED, n8n, a cloud function), another business system.

---

## 1. Decisions

| Question | Decision | Why |
|---|---|---|
| Protocol | **HTTP (REST)** for requests, **WebSocket** for live data, both with the same token; **webhooks** stay for automations without a connection | Requests are easiest over HTTP from any tool; live values need a push channel, and the gateway is built around WebSockets already |
| Not now | SSE (cheap later, same hub), gRPC streams (backends only), MQTT (we are a client, not a broker) | |
| Who a token acts as | A **machine user** (`users.kind = 'service'`), granted elements through the existing permissions (R / RC, groups) | One permission model; a token can't see more than its account |
| What a token may do | **Scopes** on top of the grants: `read`, `control`, `alerts` | A dashboard token can be read-only |
| Admin over the API | Not in phase 1: tokens never pass admin checks | Least privilege first |
| Default | **Deny**: an endpoint serves tokens only if it says so | Existing endpoints (dashboards, password, admin) can't be reached by accident |
| Protocol of the stream | A **new, versioned** protocol, not the Django-compatible `/browser/simple/` | Room for ids, resume, commands with results, alerts; the web app can move to it later |

---

## 2. API tokens (step 1)

- **Machine users**: `users.kind` is `human` or `service`. A service account has no password and can't log in, can't be admin, and gets grants like any user (Admin → Permissions).
- **Tokens**: `api_tokens(id, user_id, name, prefix, token_hash, scopes, expires_at, created_at, created_by, last_used_at)`.
  - The token is `qk_` + 32 random bytes (base64url), shown **once**; only its SHA-256 is stored, plus a short prefix to recognize it.
  - `scopes ⊆ {read, control, alerts}`; an optional expiry; deleting it revokes it at once.
  - `last_used_at` is updated at most once a minute.
- **Requests**: `Authorization: Bearer qk_…`. A bearer request ignores cookies and skips the cross-origin (CSRF) check, which only matters for cookies.
- **Admin**: `GET/POST /api/v1/admin/service-accounts`, `POST/DELETE …/{id}/tokens`, an admin page, and audit entries for every change.
- Step 1 opens `GET /api/v1/me/elements` and `GET /api/v1/elements/{id}/history` to tokens with `read`.

## 3. REST (step 2)

```
GET  /api/v1/elements                  # what the token may see (+ device, permission)
GET  /api/v1/elements/{id}/latest
GET  /api/v1/elements/{id}/history     # exists
GET  /api/v1/alerts?state=firing       # alerts scope
POST /api/v1/elements/{id}/commands    # control scope, RC grant → 202 {command_id}
GET  /api/v1/commands/{id}             # pending / delivered / failed (after step 5)
```

CORS for browser dashboards on other sites: `QUACK_API_CORS_ORIGINS` (no credentials; the token is in the header).

## 4. The stream (step 3)

`GET /api/v1/stream` (WebSocket), token in the `Authorization` header or, for browsers, a first `auth` frame.

```jsonc
→ {"type":"subscribe","id":"1","elements":["…"],"devices":["…"],"all":false,"alerts":true,"presence":true,"since":"…"}
← {"type":"subscribed","id":"1","elements":["…"]}
← {"type":"value","element_id":"…","message":{"value":21.5},"time":"…"}
← {"type":"presence","device_id":"…","connected":false}
→ {"type":"command","id":"7","element_id":"…","message":{"value":1}}
← {"type":"command_result","id":"7","status":"accepted"}         // delivered / failed after step 5
← {"type":"alert","state":"firing","rule":"Humid","field":"climate.humidity","value":85,"element_id":"…"}
← {"type":"error","id":"7","code":"forbidden","description":"…"}
```

- `since` replays what was missed from the history replay the gateway already has.
- Permission and token changes apply live (control events); a revoked token's sockets close (4000).
- Same limits as the browser socket: bounded queue, ping/pong, a frame rate per socket.

## 5. Alerts on the bus (step 4)

The alert engine also publishes `io.quack.alert.changed.v1` (through the outbox, in the transaction of the state change, so exactly once) on `control-events.v1`; gateways forward it to streams subscribed to alerts that may see the element.

## 6. Command results (step 5, X2)

A command gets an id; devices and MQTT downlinks report delivery; the stream and `GET /commands/{id}` report `accepted → delivered | failed`.

## 7. Then

Docs (an AsyncAPI document for the stream), small TypeScript and Python clients, an example external dashboard; later per-token rate limits, personal tokens for humans, admin scopes, SSE.

---

## Status

| Step | Status |
|---|---|
| 1. API tokens, machine users, scopes | Done: [API tokens](../04_api_reference/api_tokens.md) |
| 2. REST endpoints + CORS | Planned |
| 3. Stream | Planned |
| 4. Alerts on the bus | Planned |
| 5. Command results (X2) | Planned |
