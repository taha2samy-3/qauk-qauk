# REST API

The control plane: login, the current user's elements, history, dashboards and administration. Realtime data uses the [Browser](./browser_api.md) and [Device](./device_api.md) WebSocket APIs.

The API is generated from Go types with [huma](https://huma.rocks/), so the spec always matches the code:

| | |
|---|---|
| OpenAPI 3.1 spec | `GET /api/openapi.json` (also `/api/openapi.yaml`) |
| Interactive docs | **`/api/docs`**. Log in first (same origin) to try authenticated calls. |
| JSON Schemas | `/api/schemas/<Name>.json`, linked from each response's `$schema` field and `Link: rel="describedBy"` header |
| Typed TS client | `web/` generates `src/api/schema.d.ts` from the spec (`pnpm gen:api`) |

All bodies are JSON. Timestamps are RFC 3339. UUIDs identify keys, devices, elements and dashboards. Integers identify users, groups, styles and permission grants.

## Authentication

Sessions are server-side, and the browser holds an opaque cookie.

```sh
curl -c jar.txt -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin12345"}' http://127.0.0.1:8080/api/v1/auth/login
curl -b jar.txt http://127.0.0.1:8080/api/v1/auth/me
```

- **Login** (`POST /api/v1/auth/login`) checks the password (argon2id, or a legacy Django pbkdf2 hash, which is then upgraded). It returns the user with their groups and sets the cookie `quack_session`:
  - attributes: `HttpOnly; SameSite=Lax; Path=/`, plus `Secure` unless `QUACK_COOKIE_SECURE=false`
  - lifetime: `QUACK_SESSION_TTL` (14 days)
  - the database stores only a SHA-256 hash of the token
- **Failed logins** return `401 invalid username or password` for an unknown user, a wrong password or an inactive account alike. After **10 attempts in 5 minutes** for the same client address and username, the server returns `429`.
- **Logout** (`POST /api/v1/auth/logout`, `204`) deletes the session and clears the cookie.
- **Password change** (`POST /api/v1/auth/password`, `{old_password, new_password}`, at least 8 characters) ends **all** of your sessions, and open WebSockets are closed with 4000.
- **Admin endpoints** (`/api/v1/admin/*`) need `is_admin`; otherwise you get `403 admin privileges required`. Every other endpoint needs a valid session (`401 authentication required`).

Details: [Authentication](../05_core_concepts/authentication.md).

## CSRF

The API uses Go's [`http.CrossOriginProtection`](https://pkg.go.dev/net/http#CrossOriginProtection) instead of CSRF tokens. There is no token to fetch or send.

- `GET`, `HEAD` and `OPTIONS` are always allowed.
- Any other method is **rejected with `403`** (plain text, not Problem Details) when the browser marks it as cross-site:
  - `Sec-Fetch-Site` is present and is not `same-origin` or `none`, or
  - `Sec-Fetch-Site` is absent but `Origin` is present and its host differs from `Host`.
- Origins listed in `QUACK_ALLOWED_ORIGINS` are trusted.
- Requests with neither header (curl, scripts, server-to-server) pass. They cannot carry a victim's cookie through a browser anyway.

Combined with `SameSite=Lax` cookies, this is why the web app must be served from the **same site** as the API, either by the `api` role itself or through a proxy such as the Vite dev server.

## Errors

Errors use **RFC 9457 Problem Details** with `Content-Type: application/problem+json`:

```json
{
  "$schema": "http://127.0.0.1:8080/api/schemas/ErrorModel.json",
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "validation failed",
  "errors": [{ "message": "expected length >= 8", "location": "body.new_password", "value": "x" }]
}
```

| Status | When |
|---|---|
| 401 | No or invalid session; failed login |
| 403 | Not an admin (Problem Details); cross-origin write blocked by CSRF protection (plain text) |
| 404 | Unknown id, or a resource you may not see (for example, history of an element you have no grant on) |
| 409 | Unique constraint conflict (`already exists`), for example a duplicate username or group name |
| 422 | Request validation failed. `errors[].location` points to the field, such as `body.points`. |
| 429 | Too many login attempts |
| 500 | Unexpected error (`internal error`). Details are only in the server logs. |

## Resources

### Auth and the current user

| Method | Path | Description |
|---|---|---|
| POST | `/api/v1/auth/login` | Log in, set the cookie, return the user and groups |
| POST | `/api/v1/auth/logout` | End the current session (`204`) |
| GET | `/api/v1/auth/me` | The logged-in user (`id`, `username`, `email`, `is_active`, `is_admin`, `created_at`, `last_login_at`, `groups`) |
| POST | `/api/v1/auth/password` | Change your own password (`204`; revokes all your sessions) |
| GET | `/api/v1/me/elements` | Every element you can read, with your effective `permission` (`R`/`RC`), `details` and `styles` |

### History

`GET /api/v1/elements/{id}/history`. You need at least `R` on the element; without it you get `404`.

| Query | Default | Description |
|---|---|---|
| `from` | `to - 1h` | Start (RFC 3339, inclusive) |
| `to` | now | End (RFC 3339, exclusive) |
| `step` | `raw` | `raw`, or a bucket size: `1m`, `5m`, `15m`, `1h`, `1d` |
| `limit` | `1000` | Max raw events (1–10000). |
| `newest` | `false` | Raw mode: return the **newest** `limit` events of the range instead of the oldest. Results are in ascending order either way. |
| `field` | the element's value | Aggregate this attribute of the messages: `temperature`, `gps.lat`, `sensors[0].temp`. Without it, the element's value (`message.value`, a chart's `y`, or a bare number or boolean). Invalid paths return `422`. |

With `step=raw`, the response holds raw events from devices and users:

```json
{ "element_id": "…", "step": "raw",
  "events": [{ "t": "2026-10-07T12:34:34.198Z", "source": "device", "actor_id": "…", "actor_name": "Greenhouse A",
               "message": { "value": 24.9 }, "value": 24.9 }] }
```

With any other step, the response holds numeric aggregates of one attribute, read from the history store's 1-minute rollup and re-bucketed. The newest minutes are included. Buckets align to the epoch (to midnight UTC for `1d`), not to `from`:

```json
{ "element_id": "…", "step": "5m",
  "buckets": [{ "t": "2026-10-07T12:30:00Z", "avg": 28.35, "min": 24.22, "max": 30.72, "n": 26 }] }
```

Numbers, booleans (1/0) and numeric strings count; other values are skipped. See [History storage → What is stored](../05_core_concepts/history.md#what-is-stored). The web app's charts use `raw` (newest 5000) up to 1 h, `1m` for 6 h, `5m` for 24 h and `1h` for 7 days.

**Guards.** One request may ask for at most `QUACK_HISTORY_MAX_BUCKETS` buckets (default 1500, so `1m` covers up to 25 h). Larger requests get `422` with the limit in the message. Each query runs with a timeout (`QUACK_HISTORY_QUERY_TIMEOUT`, default 10 s), and each instance runs at most `QUACK_HISTORY_MAX_QUERIES` (16) at a time. A query that can't start or finish in time gets `503`; retry it, or ask for a shorter range or a larger step.

### Dashboards

Any logged-in user can create dashboards. See [Dashboards](../05_core_concepts/dashboards.md) for the layout format.

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/dashboards` | Your dashboards, then dashboards others have shared |
| POST | `/api/v1/dashboards` | Create `{name, shared?, layout?}` (`201`) |
| GET | `/api/v1/dashboards/{id}` | Your dashboard, or a shared one |
| PUT | `/api/v1/dashboards/{id}` | Replace `name`, `shared`, `layout`. **Owner only**; for anyone else it returns `404`. |
| DELETE | `/api/v1/dashboards/{id}` | Delete (owner only, `204`) |

### Administration (`is_admin` only)

Every mutation below writes an `audit_log` row and emits a control event in the same transaction (see [Realtime events](../05_core_concepts/realtime_events.md)).

| Resource | Endpoints | Notes |
|---|---|---|
| Users | `GET, POST /api/v1/admin/users`<br/>`GET, PATCH, DELETE /api/v1/admin/users/{id}` | `POST {username, password, email?, is_active?, is_admin?}`. `PATCH` any field. Changing `password` or setting `is_active:false` revokes sessions and closes the user's sockets. |
| Groups | `GET, POST /api/v1/admin/groups`<br/>`PATCH, DELETE /api/v1/admin/groups/{id}` | `{name}` |
| Group members | `GET /api/v1/admin/groups/{id}/members`<br/>`PUT, DELETE /api/v1/admin/groups/{id}/members/{user_id}` | Takes effect live on open subscriptions |
| Keys | `GET, POST /api/v1/admin/keys`<br/>`PATCH, DELETE /api/v1/admin/keys/{id}` | `POST {name, pem}`: the PEM is analyzed, and `algorithm` (`RS256`/`ES256`) and `key_size` are returned. RSA under 2048 bits and curves other than P-256 return 422. `PATCH {name?, is_active?}`. Any change force-reconnects the devices using the key. |
| Devices | `GET, POST /api/v1/admin/devices`<br/>`GET, PATCH, DELETE /api/v1/admin/devices/{id}` | `POST {name, description?, public_key_id?}`. `PATCH public_key_id: ""` unassigns the key. Deleting a device deletes its elements. |
| Elements | `GET /api/v1/admin/elements?device_id=`, `POST /api/v1/admin/elements`<br/>`GET, PATCH, DELETE /api/v1/admin/elements/{id}` | `POST {device_id, name, points (0–1000), description?, details?}`. `details` is free-form widget configuration. |
| Element styles | `GET, POST /api/v1/admin/elements/{id}/styles`<br/>`PATCH, DELETE /api/v1/admin/styles/{id}` | `{name, details}`. The web app reads the style named `widget` (`{"widget": "sensor"\|"chart"\|"switch"\|"slider"}`) as a widget hint. |
| Permissions | `GET /api/v1/admin/permissions?element_id=&user_id=&group_id=`<br/>`PUT /api/v1/admin/permissions`<br/>`DELETE /api/v1/admin/permissions/{id}` | `PUT {element_id, user_id \| group_id, permission: "R"\|"RC"}` creates or updates one grant (idempotent). Exactly one of `user_id` / `group_id`. |
| Connections | `GET /api/v1/admin/connections?device_id=&limit=` | Device connection audit log, newest first (limit 1–1000, default 100) |
| Presence | `GET /api/v1/admin/presence` | Live presence leases (`device_id`, `gateway_id`, `conn_id`, `connected_at`, `last_seen_at`) |
| Audit log | `GET /api/v1/admin/audit?limit=` | Admin actions, newest first: actor, action, entity, data |

Lists are not paginated, except the log endpoints, which take `limit`. For exact request and response schemas, see `/api/docs`.

## Operational endpoints

These are outside `/api` and need no authentication:

| Path | Description |
|---|---|
| `/healthz` | `200 ok` while the process runs |
| `/readyz` | `200 ok` if the database answers within 2 s, else `503` |
| `/metrics` | Prometheus metrics |
