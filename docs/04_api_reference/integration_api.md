# Integration API

<p align="center">
  <img src="/brand/logo-api.svg" alt="Quack Quack integration API" width="96" />
</p>

What a dashboard does, for programs outside the platform: list the elements you may see with their newest values, read alerts, and send commands to devices. Build your own dashboard on another site, or automate from a script, Node-RED, n8n or a cloud function, without being part of the platform.

Calls are authenticated with an [API token](./api_tokens.md) (`Authorization: Bearer qk_…`) and limited to the elements its machine user is granted. A logged-in session works too, so the web app can use the same endpoints.

Two parts, with the same token:

- **REST** for requests: elements, newest values, history, alerts, commands.
- **The [stream](#stream)** (WebSocket) for what happens live: values, device presence, alerts, and commands with their answers.

Automations that shouldn't keep a connection open can also receive alerts as [webhooks](../05_core_concepts/webhooks_and_alerts.md).

## Endpoints

| Endpoint | Scope | |
|---|---|---|
| `GET /api/v1/elements` | `read` | The elements the token may read, with their device and permission (`R` or `RC`). `?latest=true` adds each one's newest stored device message. |
| `GET /api/v1/elements/{id}/latest` | `read` | One element's newest stored device message (`latest: null` if none yet) |
| `GET /api/v1/elements/{id}/history` | `read` | History: raw events or aggregates ([reference](./rest_api.md)) |
| `GET /api/v1/alerts` | `alerts` | `?state=firing` (default): the enabled rules firing now; `?state=all`: every rule of its elements, with its state |
| `POST /api/v1/elements/{id}/commands` | `control` | Send a command, like a dashboard. Needs the `RC` grant. `202` |
| `GET /api/v1/commands/{id}` | `control` | What became of a command you sent ([below](#what-became-of-a-command)) |

An element the token isn't granted answers `404`, as if it didn't exist.

### Elements and their newest values

```bash
curl -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/elements?latest=true"
```

```json
[
  {
    "id": "01a1159f-…", "name": "Soil moisture", "description": "", "details": {"unit": "%"},
    "device_id": "01a1159f-…", "device_name": "Greenhouse A", "permission": "RC",
    "latest": {"time": "2026-10-19T09:00:00.120Z", "message": {"value": 41.2}, "value": 41.2}
  }
]
```

`latest.value` is the element's numeric value (`message.value`, a chart's `y`, or a bare number), when it has one; `message` is the whole message, so other attributes (`climate.humidity`…) are there too.

### Alerts

```bash
curl -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/alerts"            # firing now
curl -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/alerts?state=all"  # every rule
```

```json
[
  {
    "id": "…", "name": "Humid", "field": "climate.humidity", "condition": "above", "threshold": 80,
    "severity": "warning", "enabled": true, "element_id": "…", "element_name": "Climate",
    "device_name": "Greenhouse A", "firing": true, "last_value": 85, "changed_at": "2026-10-19T08:58:02Z"
  }
]
```

### Commands

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"message": {"value": 1}}' "$BASE/api/v1/elements/$ELEMENT_ID/commands"
```

```json
{"command_id": "0199…", "element_id": "…", "status": "accepted", "time": "2026-10-19T09:01:00.004Z"}
```

- The message is in the element's (engineering) units, as a dashboard sends it. The element's [pipeline](../05_core_concepts/element_pipeline.md#_4-commands-the-pipeline-in-reverse) converts it for the device; a command it can't convert is refused (`422`).
- It reaches the device's WebSocket, REST (`/device/v1/sync`), gRPC and MQTT connections, on whichever gateway they are, and it is stored in history as a user command.
- `accepted` means it is on its way; [what becomes of it](#what-became-of-a-command) is reported.
- Each command is in the audit log, by machine user and token name (`integ-bot (token full)`).
- Commands are limited per caller: `QUACK_API_COMMAND_RATE` per second (default 10, bursts of twice that) per instance; over it, `429`.

| Answer | When |
|---|---|
| `202` | Accepted |
| `403 sending commands needs the RC permission on the element` | The grant is `R` |
| `403 the API token lacks the control scope` | |
| `404` | Not granted |
| `422` | No message, not JSON, over 64 KiB, or the pipeline can't convert it |
| `429` | Over the command rate |

### What became of a command

Every command sent through this API (REST or stream) is tracked; dashboard commands aren't. It starts `accepted`, and the first report moves it on:

| Status | When | `reason` |
|---|---|---|
| `accepted` | On the bus, not delivered yet (no device connection took it, e.g. the device is offline) | |
| `delivered` | Handed to a device WebSocket or gRPC stream, taken by the device's REST poll (`/device/v1/sync`), or published to the MQTT broker | which one |
| `failed` | The pipeline couldn't convert it on the gateway, or the MQTT downlink failed after its retries | why |
| `superseded` | A newer command for the same element replaced it before the MQTT downlink sent it (newest wins) | |

`delivered` means the device's connection got it, not that the device acted on it: devices confirm by sending their new state back as a value (the actuator pattern), which a stream sees as a `value`.

```bash
curl -H "Authorization: Bearer $TOKEN" "$BASE/api/v1/commands/$COMMAND_ID"
```

```json
{"command_id": "0199…", "element_id": "…", "actor": "integ-bot (token full)", "message": {"value": 1},
 "status": "delivered", "reason": "taken by the device's REST poll", "created_at": "…", "updated_at": "…"}
```

Only the account that sent a command can read it (`404` otherwise). On the stream, the sender's streams get a second `command_result` with the status, carrying the request `id` on the stream that sent it. Commands are kept `QUACK_COMMAND_RETENTION` (7 days). A command sent while a WebSocket, gRPC or MQTT device is offline isn't queued for it: it stays `accepted`. A REST device gets the newest command per element at its next poll.

## Stream

`GET /api/v1/stream` upgrades to a WebSocket, served by every gateway. Frames are JSON text.

### Connecting

| Client | Authentication |
|---|---|
| A server, a script | `Authorization: Bearer qk_…` on the upgrade request (`401` if the token is bad) |
| A browser (can't set headers on WebSockets) | Connect, then send `{"type": "auth", "token": "qk_…"}` as the first frame within 10 s |
| The web app | The session cookie, from an allowed origin |

The server answers with `welcome`:

```json
{"type": "welcome", "protocol": 1, "user": "integ-bot", "scopes": ["read", "control", "alerts"]}
```

A bad or missing token closes the socket with **4001**; a token revoked or expired while connected closes it with **4000** (checked at once on revocation, and every minute). While an instance shuts down it closes streams with **1012**: reconnect after a short random delay and subscribe again (with `since`).

### Subscribing

```jsonc
→ {"type": "subscribe", "id": "1", "all": true, "alerts": true}
→ {"type": "subscribe", "id": "2", "elements": ["01a1…"], "devices": ["01a0…"], "since": "2026-10-19T09:00:00Z"}
← {"type": "subscribed", "id": "1", "alerts": true, "denied": [],
   "elements": [{"id": "01a1…", "name": "Soil moisture", "device_id": "01a0…", "device_name": "Greenhouse A", "permission": "RC", "connected": true}]}
```

| Field | |
|---|---|
| `elements`, `devices` | Elements by id, or every element of devices, that the token may read (`read` scope) |
| `all` | Every element it may read, **and those granted later** (it follows Admin → Permissions live) |
| `alerts` | Alert events of every element it may read (`alerts` scope) |
| `since` | Replay the values after this time, from the element's history window (its *points*); without it, only the newest value is replayed |
| `id` | Your id, echoed in the answer and in errors |

Elements asked for by id that the token may not read are listed in `denied`. After `subscribed`, each element's replay comes (values with `"replay": true`), then live frames.

`{"type": "unsubscribe", "id": "3", "elements": [...], "devices": [...], "all": true, "alerts": true}` stops some or all of it; the answer is `unsubscribed`.

### What you receive

```jsonc
// a value: from the device, or a command a user sent ("source": "user")
{"type": "value", "element_id": "01a1…", "time": "2026-10-19T09:00:00.120Z", "source": "device", "actor": "Greenhouse A",
 "message": {"value": 41.2, "climate": {"humidity": 40}}}
// the device connected or disconnected
{"type": "presence", "element_id": "01a1…", "connected": false}
// an alert started firing or resolved
{"type": "alert", "state": "firing", "rule_id": "…", "rule_name": "Humid", "severity": "warning", "field": "climate.humidity",
 "value": 85, "message": "85 is above 80 (Greenhouse A Climate climate.humidity)", "element_id": "…", "element_name": "Climate",
 "device_id": "…", "device_name": "Greenhouse A", "time": "2026-10-19T08:58:02Z"}
// a grant changed, or the element went away
{"type": "permission", "element_id": "01a1…", "permission": "R"}
{"type": "unsubscribed", "elements": ["01a1…"], "reason": "Permission revoked"}
// something you sent was refused, or a command couldn't be delivered (MQTT)
{"type": "error", "id": "7", "code": "forbidden", "description": "…", "element_id": "01a1…"}
```

### Commands

```jsonc
→ {"type": "command", "id": "7", "element_id": "01a1…", "message": {"value": 1}}
← {"type": "command_result", "id": "7", "element_id": "01a1…", "command_id": "0199…", "status": "accepted"}
// a moment later, what became of it
← {"type": "command_result", "id": "7", "element_id": "01a1…", "command_id": "0199…", "status": "delivered", "reason": "handed to a device connection"}
```

The same rules as `POST …/commands`: the `control` scope, the `RC` grant, the element's pipeline must be able to convert it, and each command of a token is in the audit log. Error codes: `forbidden`, `not_found`, `invalid_format`, `pipeline_failed`. An element doesn't need to be subscribed to send it commands. A stream doesn't get its own commands back as values; other streams and dashboards do.

### Limits

Like a dashboard socket: `QUACK_BROWSER_MSG_RATE` frames per second (then `rate_limited` errors), a bounded queue (a client that can't keep up is closed with 1013), frames up to 64 KiB, pings every 30 s.

### Example: a live dashboard in a page

```html
<pre id="out"></pre>
<script type="module">
  const TOKEN = 'qk_…' // a machine user that may only read, or put a backend in front
  const out = document.getElementById('out')
  const values = {}
  const ws = new WebSocket('wss://quack.example.com/api/v1/stream')
  ws.onopen = () => ws.send(JSON.stringify({ type: 'auth', token: TOKEN }))
  ws.onmessage = (e) => {
    const f = JSON.parse(e.data)
    if (f.type === 'welcome') ws.send(JSON.stringify({ type: 'subscribe', id: 'all', all: true, alerts: true }))
    if (f.type === 'subscribed') for (const el of f.elements) values[el.id] = { name: `${el.device_name} · ${el.name}` }
    if (f.type === 'value') values[f.element_id].value = f.message.value ?? f.message
    if (f.type === 'alert') console.warn(`${f.state}: ${f.rule_name} (${f.value})`)
    out.textContent = Object.values(values).map((v) => `${v.name}: ${JSON.stringify(v.value)}`).join('\n')
  }
</script>
```

### From Python

```python
import asyncio, json, os, websockets  # pip install websockets

async def main():
    headers = {"Authorization": f"Bearer {os.environ['QUACK_TOKEN']}"}
    async with websockets.connect("wss://quack.example.com/api/v1/stream", additional_headers=headers) as ws:
        await ws.send(json.dumps({"type": "subscribe", "id": "1", "alerts": True}))
        async for raw in ws:
            f = json.loads(raw)
            if f["type"] == "alert" and f["state"] == "firing":
                print("ALERT", f["rule_name"], f["device_name"], f["field"], f["value"])

asyncio.run(main())
```

## From a browser on another site

A dashboard served from another origin can call the API with a token if that origin is listed in `QUACK_API_CORS_ORIGINS` (comma-separated, or `*`). Credentials (cookies) are never allowed cross-origin, only the token's header.

```js
const api = (path, init = {}) =>
  fetch(`https://quack.example.com${path}`, {
    ...init,
    headers: { Authorization: `Bearer ${TOKEN}`, 'Content-Type': 'application/json', ...init.headers },
  }).then((r) => (r.ok ? r.json() : Promise.reject(r)))

const elements = await api('/api/v1/elements?latest=true')
await api(`/api/v1/elements/${elements[0].id}/commands`, { method: 'POST', body: JSON.stringify({ message: { value: 1 } }) })
```

A token in a page's JavaScript can be read by anyone who opens the page: give such a page a machine user that may only read, or put a small backend in front of it.

## From a script

```python
import os, time, requests

s = requests.Session()
s.headers["Authorization"] = f"Bearer {os.environ['QUACK_TOKEN']}"
BASE = "https://quack.example.com"

# turn the pump on when the soil is dry
while True:
    els = {e["name"]: e for e in s.get(f"{BASE}/api/v1/elements", params={"latest": "true"}).json()}
    moisture = (els["Soil moisture"].get("latest") or {}).get("value")
    if moisture is not None and moisture < 25:
        s.post(f"{BASE}/api/v1/elements/{els['Pump']['id']}/commands", json={"message": {"value": 1}}).raise_for_status()
    time.sleep(30)
```

## Configuration

| Variable | Default | |
|---|---|---|
| `QUACK_API_CORS_ORIGINS` | *(empty)* | Origins allowed to call the API from browsers with a token (`https://wall.example.com`, or `*`) |
| `QUACK_API_COMMAND_RATE` | `10` | Commands per second per caller and instance through the REST API (burst: twice that); `0` disables the limit |
| `QUACK_COMMAND_RETENTION` | `168h` | How long tracked commands and their status are kept |
