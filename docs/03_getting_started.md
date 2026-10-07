# 3. Getting started

- [Local development](#local-development)
- [Full stack in containers](#full-stack-in-containers)
- [Create the first admin](#create-the-first-admin)
- [Connect a device or Node-RED](#connect-a-device-or-node-red)
- [Configuration reference](#configuration-reference)
- [Migrating from the legacy Django server](#migrating-from-the-legacy-django-server)

## Prerequisites

- **Docker** with the Compose plugin (for TimescaleDB and Redpanda).
- **[mise](https://mise.jdx.dev/)**. `mise install` installs the pinned Go, [Task](https://taskfile.dev/) and golangci-lint from `mise.toml`.
- **Node.js 22+ and pnpm** for the web app (`web/package.json` pins `pnpm@11`; `corepack enable` provides it).

Every command is a task. Run `task --list` to see them all. The tasks set the development environment for you (`QUACK_DATABASE_URL`, `QUACK_KAFKA_BROKERS`, `QUACK_COOKIE_SECURE=false`, allowed origins for :5173 and :8080). If you run `server/bin/quack` directly, export those variables yourself (see [Configuration reference](#configuration-reference)).

## Local development

```sh
mise install        # Go, Task, golangci-lint
task infra:up       # TimescaleDB (127.0.0.1:5433) + Redpanda (127.0.0.1:19092)
task demo           # build, migrate, seed demo data, write server/bin/demo-devices.json
task dev            # api + gateway on http://127.0.0.1:8080 (keep running)
task simulate       # new terminal: the demo devices connect and stream values
task web:install    # new terminal: install web dependencies
task web:dev        # Vite dev server on http://127.0.0.1:5173
```

Open **http://127.0.0.1:5173** and log in:

| User | Password | Access |
|---|---|---|
| `admin` | `admin12345` | Admin, `RC` on every demo element (can move switches and sliders) |
| `viewer` | `viewer12345` | Member of the `operators` group, which has `R` on every demo element (read-only) |

`task demo` creates two demo devices: *Greenhouse A* (ES256: temperature, humidity, soil moisture, an irrigation pump switch, a fan-speed slider) and *Boiler room* (RS256: water temperature, pressure, a burner switch). Their private keys go to `server/bin/demo-devices.json`, which `task simulate` reads. The simulator echoes every command back as the new state, the way a real actuator would.

> `task demo` adds data every time it runs. A second run creates a second set of demo devices and overwrites `demo-devices.json` with only the new keys. To start from scratch, run `task infra:reset`, then `task infra:up` and `task demo` again.

**Optional pieces:**

| Command | What it adds |
|---|---|
| `task ingest` | The TSDB writer (metrics on :9100). Without it, live data and the replay of recent values still work, but nothing is written to `element_event`. History charts stay empty, and a restarted gateway cannot replay older values. |
| `task console` | Redpanda Console on http://127.0.0.1:8090 to inspect topics and CloudEvents. |
| `task migrate` | Applies migrations and creates the topics. `task dev` and `task demo` already run it. |
| `task web:build` | Builds the web app into `web/dist`. `task dev` then also serves it on http://127.0.0.1:8080. |

**Ports:**

| Port | Service |
|---|---|
| 5173 | Vite dev server. It proxies `/api/`, `/browser/` and `/device/` to :8080. |
| 8080 | `quack serve`: REST, WebSockets, API docs at `/api/docs` |
| 9100 | `quack ingest` health and metrics |
| 5433 | PostgreSQL + TimescaleDB (user, password and database are all `quack`) |
| 19092 | Redpanda Kafka API (external listener) |
| 8090 | Redpanda Console (`task console`) |

**Tests:**

```sh
task check            # lint + unit (-race) + integration + contract suite; needs task infra:up
task test             # unit tests only, no infrastructure needed
task contract:go      # WebSocket contract suite (own database quack_contract, server on :8766)
```

`task infra:down` stops the containers and keeps the data.

## Full stack in containers

```sh
task up               # builds the image (backend + frontend) and starts everything
```

This starts `db`, `redpanda`, a one-shot `migrate`, `quack` (api+gateway on http://127.0.0.1:8080) and `ingest`. It waits until they are healthy. `task docker:build` only builds the image.

The database starts empty. The database port is published on 127.0.0.1:5433, so the host tasks work against this stack too:

```sh
task admin:create ADMIN=alice   # your first admin, see below
task demo                      # optional: demo users, devices and grants
task simulate                  # optional: stream demo values to the container on :8080
```

Don't run `task dev` at the same time: it also listens on :8080.

For a non-local deployment, set at least `QUACK_ALLOWED_ORIGINS` (your public origin) and `QUACK_COOKIE_SECURE=true`. Both are read from your shell by `docker/compose.yaml`.

Compose also defines a `node-red` service in the `tools` profile. Start it with `docker compose -f docker/compose.yaml --profile tools up -d node-red`; it listens on http://127.0.0.1:1880. Inside the compose network the gateway is reachable at `ws://quack:8080/device/node_red/`.

## Create the first admin

```sh
task admin:create ADMIN=alice
```

It prompts for the password on stdin; the minimum is 8 characters. For scripts, set `QUACK_PASSWORD` instead. Always pass `USER=` explicitly. Without it, Task falls back to your shell's `$USER` environment variable and creates an admin with your login name.

Other admin commands use the binary directly. Export `QUACK_DATABASE_URL` first (for local dev: `postgres://quack:quack@127.0.0.1:5433/quack?sslmode=disable`):

```sh
server/bin/quack admin set-password --username alice          # revokes alice's sessions
server/bin/quack admin import-key --name sensor-7 --file pub.pem
```

Everything else (users, groups, keys, devices, elements, permissions) is managed in the web app's **Admin** pages or through the [REST API](./04_api_reference/rest_api.md).

> Admins are **not** granted element access automatically. To see an element on a dashboard, an admin also needs an `R` or `RC` grant, like any other user.

## Connect a device or Node-RED

A device needs four things: a **key pair**, a **device** record that uses the public key, one or more **elements**, and someone with a **grant** to look at them.

### Generate a key pair

ES256 (ECDSA P-256) is recommended. RS256 needs RSA with at least 2048 bits.

```sh
# ES256
openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 -out device-key.pem
openssl pkey -in device-key.pem -pubout -out device-pub.pem

# or RS256
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out device-key.pem
openssl pkey -in device-key.pem -pubout -out device-pub.pem
```

`device-key.pem` (PKCS#8) stays on the device. Only `device-pub.pem` goes to the server.

### Register the key, device, element and grant

Use **Admin → Keys / Devices / Elements / Permissions** in the web app, or script it against the REST API. This script logs in, then creates everything:

```sh
API=http://127.0.0.1:8080
curl -sf -c jar.txt -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"..."}' "$API/api/v1/auth/login" > /dev/null

# 1. The public key (algorithm and size are detected from the PEM)
KEY_ID=$(jq -n --rawfile pem device-pub.pem '{name: "greenhouse-b", pem: $pem}' |
  curl -sf -b jar.txt -H 'Content-Type: application/json' -d @- "$API/api/v1/admin/keys" | jq -r .id)

# 2. The device, using that key
DEVICE_ID=$(jq -n --arg key "$KEY_ID" '{name: "Greenhouse B", public_key_id: $key}' |
  curl -sf -b jar.txt -H 'Content-Type: application/json' -d @- "$API/api/v1/admin/devices" | jq -r .id)

# 3. An element on the device (points = how many recent values new viewers get)
ELEMENT_ID=$(jq -n --arg dev "$DEVICE_ID" '{device_id: $dev, name: "Temperature", points: 100,
    details: {title: "Temperature", unit: "°C", minValue: -10, maxValue: 50}}' |
  curl -sf -b jar.txt -H 'Content-Type: application/json' -d @- "$API/api/v1/admin/elements" | jq -r .id)

# 4. Optional widget hint for the dashboard editor: sensor | chart | switch | slider
curl -sf -b jar.txt -H 'Content-Type: application/json' \
  -d '{"name": "widget", "details": {"widget": "sensor"}}' \
  "$API/api/v1/admin/elements/$ELEMENT_ID/styles" > /dev/null

# 5. Grant yourself read + control
ME=$(curl -sf -b jar.txt "$API/api/v1/auth/me" | jq .id)
jq -n --arg el "$ELEMENT_ID" --argjson me "$ME" '{element_id: $el, user_id: $me, permission: "RC"}' |
  curl -sf -b jar.txt -X PUT -H 'Content-Type: application/json' -d @- "$API/api/v1/admin/permissions" > /dev/null

echo "DEVICE_ID=$DEVICE_ID ELEMENT_ID=$ELEMENT_ID"
```

curl sends no `Origin` or `Sec-Fetch-Site` header, so the CSRF check lets it through ([REST API → CSRF](./04_api_reference/rest_api.md#csrf)). An element created while its device is connected is usable immediately; the device does not need to reconnect.

### Device example in JavaScript (jose)

The token must carry `id` (the device UUID, a string) and `exp`. Its lifetime (`exp - iat`, or `exp - now` without `iat`) must not exceed `QUACK_DEVICE_JWT_MAX_LIFETIME` (24 h by default; 30 s of clock skew is allowed). The algorithm must match the stored key.

```sh
npm i jose ws
```

```js
// device.mjs: a minimal Quack Quack device
import { readFileSync } from 'node:fs'
import { SignJWT, importPKCS8 } from 'jose'
import WebSocket from 'ws'

const SERVER = process.env.QUACK_WS ?? 'ws://127.0.0.1:8080'
const DEVICE_ID = process.env.DEVICE_ID   // device UUID
const ELEMENT_ID = process.env.ELEMENT_ID // one of this device's elements
const ALG = process.env.ALG ?? 'ES256'    // must match the stored key: ES256 or RS256

const key = await importPKCS8(readFileSync('device-key.pem', 'utf8'), ALG)
const token = await new SignJWT({ id: DEVICE_ID }) // `id` = device UUID (required)
  .setProtectedHeader({ alg: ALG })
  .setIssuedAt()
  .setExpirationTime('12h')                         // `exp` is required; <= QUACK_DEVICE_JWT_MAX_LIFETIME
  .sign(key)

const ws = new WebSocket(`${SERVER}/device/node_red/`, {
  headers: { Authorization: `Bearer ${token}` },
})

ws.on('open', () => {
  console.log('connected')
  // Telemetry: no `type` field, just element_id + message.
  ws.send(JSON.stringify({ element_id: ELEMENT_ID, message: { value: 21.5 } }))
})

// Commands from dashboards arrive in the same shape, plus auth and last_edit_at.
ws.on('message', (data) => {
  const { element_id, message, auth } = JSON.parse(data.toString())
  console.log(`command for ${element_id} from ${auth.username}:`, message)
})

ws.on('unexpected-response', (_req, res) => { console.error('rejected: HTTP', res.statusCode); process.exit(1) })
ws.on('close', (code, reason) => console.log('closed', code, reason.toString()))
```

```sh
DEVICE_ID=... ELEMENT_ID=... node device.mjs
```

A rejected token fails the handshake with **HTTP 403**. The token is only checked at connect time; when it expires, sign a new one before you reconnect. The [Device API](./04_api_reference/device_api.md) covers frames, close codes and limits.

### Node-RED

**Recommended:** install the [Quack Quack nodes](./09_node_red.md) (`node-red-contrib-quackquack`, from **Manage palette**). They sign tokens, reconnect, pick elements by name, and receive commands, with an example flow included. To try them against this checkout without installing anything:

```sh
task nodered:dev   # Node-RED on http://127.0.0.1:1880 with the nodes loaded from source
```

![Node-RED next to the dashboard it feeds](./imgs/screenshots/nodered-with-dashboard.webp)

#### Without the nodes: plain websocket nodes

Node-RED's built-in **websocket** nodes (Node-RED 4.0+) can send an `Authorization` header from the client config node.

1. Create a token once (or on a schedule) with the device key:

   ```js
   // token.mjs: prints "Bearer <jwt>"
   import { readFileSync } from 'node:fs'
   import { SignJWT, importPKCS8 } from 'jose'
   const alg = process.env.ALG ?? 'ES256'
   const key = await importPKCS8(readFileSync('device-key.pem', 'utf8'), alg)
   console.log('Bearer ' + await new SignJWT({ id: process.env.DEVICE_ID })
     .setProtectedHeader({ alg }).setIssuedAt().setExpirationTime('24h').sign(key))
   ```

2. Give the value to Node-RED as the environment variable `QUACK_DEVICE_AUTH` (or paste it as a literal header value).
3. Import this flow (**Menu → Import**) and set the URL and `QUACK_ELEMENT_ID`:

```json
[
  {
    "id": "qq_client", "type": "websocket-client",
    "path": "ws://127.0.0.1:8080/device/node_red/",
    "tls": "", "wholemsg": "false", "hb": "0", "subprotocol": "",
    "headers": [
      { "keyType": "Authorization", "keyValue": "", "valueType": "env", "valueValue": "QUACK_DEVICE_AUTH" }
    ]
  },
  {
    "id": "qq_tick", "type": "inject", "name": "every 5 s",
    "props": [{ "p": "payload" }], "payload": "", "payloadType": "date",
    "repeat": "5", "crontab": "", "once": true, "onceDelay": "1", "topic": "",
    "wires": [["qq_frame"]]
  },
  {
    "id": "qq_frame", "type": "function", "name": "to Quack frame", "outputs": 1,
    "func": "msg.payload = {\n  element_id: env.get('QUACK_ELEMENT_ID'),\n  message: { value: Math.round((20 + Math.random() * 5) * 10) / 10 }\n};\nreturn msg;",
    "wires": [["qq_out"]]
  },
  { "id": "qq_out", "type": "websocket out", "name": "to Quack", "server": "", "client": "qq_client", "wires": [] },
  { "id": "qq_in", "type": "websocket in", "name": "from Quack", "server": "", "client": "qq_client", "wires": [["qq_json"]] },
  { "id": "qq_json", "type": "json", "name": "", "property": "payload", "action": "obj", "pretty": false, "wires": [["qq_debug"]] },
  { "id": "qq_debug", "type": "debug", "name": "commands", "active": true, "tosidebar": true, "complete": "payload", "wires": [] }
]
```

In the editor, the client node shows **Headers → Authorization = env `QUACK_DEVICE_AUTH`**. The websocket-out node JSON-encodes an object `msg.payload`. Incoming commands are JSON strings, so parse them with the `json` node before you route on `payload.element_id`.

> **Token lifetime with Node-RED.** The header is fixed when the flow is deployed, and Node-RED reconnects with the same token. Once the token has expired, reconnects fail with HTTP 403. You have two options:
> - Regenerate the token and redeploy (or restart) before it expires.
> - Raise `QUACK_DEVICE_JWT_MAX_LIFETIME` on the server (for example `720h`), accepting longer-lived credentials. The key can still be revoked at any time by deactivating it.

## Configuration reference

All configuration comes from environment variables. Durations use Go syntax (`30s`, `10m`, `24h`), and lists are comma-separated.

| Variable | Default | Used by | Description |
|---|---|---|---|
| `QUACK_DATABASE_URL` | *(required)* | all | PostgreSQL/TimescaleDB URL, e.g. `postgres://quack:quack@127.0.0.1:5433/quack?sslmode=disable`. |
| `QUACK_KAFKA_BROKERS` | `localhost:19092` | serve, ingest, migrate | Redpanda seed brokers. |
| `QUACK_KAFKA_ACKS_ALL` | `true` | serve | `true` means `acks=all` (durable). `false` means `acks=1` (lower latency, may lose events on broker failure). |
| `QUACK_INGEST_GROUP` | `quack-ingest` | ingest | Consumer group of the ingester. **Use a different value per environment/database** sharing a Redpanda cluster ([why](./02_architecture.md#the-ingester-consumer-group)). |
| `QUACK_HTTP_ADDR` | `:8080` | serve, ingest | Listen address. The ingester only serves `/healthz` and `/metrics` on it (the tasks and compose use `:9100`). |
| `QUACK_ROLES` | `api,gateway` | serve | Roles to enable: `api`, `gateway` or both. |
| `QUACK_GATEWAY_ID` | `gw-<hostname>-<random>` | serve | Unique id of this gateway instance (presence leases, own-event detection). |
| `QUACK_ALLOWED_ORIGINS` | *(empty)* | serve | Extra trusted origins (`scheme://host[:port]`) for the browser WebSocket and for cross-origin API writes. Same-host requests are always allowed. |
| `QUACK_COOKIE_SECURE` | `true` | serve | Sets `Secure` on the session cookie. Set it to `false` only for plain-HTTP development. |
| `QUACK_SESSION_TTL` | `336h` (14 days) | serve | Session lifetime from login. It is not extended by activity. |
| `QUACK_DEVICE_JWT_MAX_LIFETIME` | `24h` | serve | Maximum `exp - iat` (or `exp - now`) for device tokens. 30 s of leeway is added. |
| `QUACK_DEVICE_MSG_RATE` | `50` | serve | Messages per second per device socket (token bucket, burst = rate). The excess is dropped. `0` disables the limit. |
| `QUACK_BROWSER_MSG_RATE` | `100` | serve | Frames per second per browser socket. The excess gets a `rate_limited` error. `0` disables the limit. |
| `QUACK_PRESENCE_HEARTBEAT` | `10s` | serve | How often a gateway refreshes its presence leases and runs the sweeper. |
| `QUACK_PRESENCE_TTL` | `30s` | serve | A lease older than this counts as dead (the device shows as disconnected). |
| `QUACK_WEB_DIR` | `../web` | serve | Directory of the built web app, served at `/` with SPA fallback. It is ignored if missing; set it to empty to disable. The tasks use `web/dist`, and the image uses `/web`. |
| `QUACK_LOG_LEVEL` | `info` | all | `debug`, `info`, `warn`, `error`. Logs are JSON on stderr. |
| `QUACK_KAFKA_DEBUG` | *(unset)* | ingest | Any value enables franz-go client logs for the consumer group. |
| `QUACK_PASSWORD` | *(unset)* | `admin create-user`, `admin set-password` | Password, when you don't pass `--password` or type it on stdin. |
| `DJANGO_DATABASE_URL` | *(unset)* | `import-django` | Default for `--from`. |

Test-only variables (`CONTRACT_*`, `QUACK_IT_*`, `LOAD_*`, `QUACK_BUILD_FLAGS`) are described in [`server/contracttest/README.md`](https://github.com/taha2samy-3/qauk-qauk/blob/main/server/contracttest/README.md).

## Migrating from the legacy Django server

`quack import-django` copies a legacy Django database into the Go schema. Deployed devices and Node-RED flows keep working **without changes**: same WebSocket paths, frames, JWT scheme and UUIDs.

```sh
task infra:up
task migrate                                          # the importer needs the schema
task import:django FROM=postgres://user:pass@host:5432/django_db
```

It prints a summary such as `imported: users=12 groups=3 memberships=15 keys=8 devices=8 elements=31 styles=31 permissions=54`, followed by any warnings.

**What is preserved:**

- **UUIDs** of devices, elements and keys. They are byte-for-byte identical, so device tokens (`id` claim) still match.
- **Public keys**, including their `is_active` flag.
- **User ids, usernames, emails and password hashes.** `is_superuser` becomes `is_admin`.
- **Groups and memberships, element styles, and both permission tables**, merged into `element_permissions` (`R`/`RC`).

**Passwords.** Django `pbkdf2_sha256` hashes are verified as they are. On a user's first successful login, the hash is replaced with **argon2id**. Nobody has to reset a password. Hashes in any other format cannot log in; set a new password with `quack admin set-password`.

**Idempotent.** The whole import runs in one transaction and upserts by id, so you can re-run it as often as you like, for example once for testing and once more at cutover. Styles are replaced per element. ID sequences are moved past the imported ids.

**Not imported:** sessions (users log in again), connection history, and the in-memory value cache (Django kept no history).

**Notes:**

- The importer reads **PostgreSQL** only. If the old deployment used SQLite (the Django default settings did), load it into PostgreSQL first, for example with `pgloader`.
- It writes no control events. Run it before you start the gateways, or restart them afterwards.
- Keys that fail today's policy (RSA under 2048 bits, curves other than P-256) are still imported, with a warning, so that devices keep connecting. Keys that cannot be parsed at all are skipped. Read the warnings.
- The new server is stricter. Tokens **without `exp`**, tokens that live **longer than `QUACK_DEVICE_JWT_MAX_LIFETIME`**, and keys marked **inactive** are now rejected. Check your fleet before cutover, or raise the lifetime cap.
- The behavior changes visible to clients are listed in [Device API → Changes](./04_api_reference/device_api.md#changes-from-the-legacy-server) and [Browser API → Changes](./04_api_reference/browser_api.md#changes-from-the-legacy-server).
