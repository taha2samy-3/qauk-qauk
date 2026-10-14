# Element Pipeline

<p align="center">
  <img src="/brand/logo-pipeline-wordmark.svg" alt="Quack Quack Element Pipeline" width="370" />
</p>

The **element pipeline** (station 2) turns what a device sends into the values the platform stores and shows: scaling, unit conversion, rounding, lookup tables, deadband and other filters, or a small JavaScript function. It runs **inside each gateway, in memory**, after the message is resolved to its element and before it is rate-limited and published to Redpanda. Commands to the device go through the same steps in reverse.

---

## 1. Where it sits: three stations, one contract

```
(1) Source mapping            (2) Element pipeline              (3) Derived elements
    per connection                per element                       virtual elements
    raw → element values   →      pick/scale/deadband/…/script  →   CloudEvent on Redpanda  →  formula/window → CloudEvent
    MQTT, HRW slots               in gateway memory,                 (background bus)            consumer in background,
                                  before Redpanda                                               back through the core
    ──────────────────────────────▲ THIS STATION
```

- **Station 1 (source mapping):** per-connection decoders and field maps (for example the JavaScript decoder of a LoRaWAN frame on an MQTT topic). See [MQTT](../10_mqtt.md).
- **Station 2 (element pipeline):** per element. It applies the same way whatever the transport: WebSocket, REST, gRPC, MQTT and Node-RED.
- **Station 3 (derived elements):** virtual elements computed over streams (formulas, windows). Not built yet.

Whatever the station, what reaches the bus is an `io.quack.element.message.v1` CloudEvent. A step only returns a message (or "drop it", or an error): it never writes to the bus, reads a database or opens a socket.

---

## 2. Rules

1. **Compiled once.** A pipeline is compiled when the device's configuration (`device-config.v1`) reaches the gateway, never per message. Built-in steps are plain Go functions. An element without a pipeline costs a nil check.
2. **Never published untransformed.** If a saved version doesn't compile on a gateway (for example a newer server saved a step this one doesn't know), the gateway keeps the last version it had working and counts `quack_pipeline_compile_errors_total`. If it has none, the element's messages fail (`pipeline_failed`) instead of reaching the bus in device units.
3. **A pure package.** `server/internal/elementpipe` doesn't depend on the gateway, store, service or bus, so the API runs the very same code for previews and tests.
4. **Sandboxed scripts.** A `script` step runs in [goja](https://github.com/dop251/goja) with no file system, network, timers or modules. Each run has 20 ms, including the script's top-level code. Strings, arrays and typed-array buffers are capped at 1 MiB / 100,000 items / 1 MiB, recursion at 256 calls, and the result at 64 KiB. The result must be an object of finite JSON values (no `NaN` or `Infinity`).

### Limits

| | Limit |
|---|---|
| Steps per pipeline | 32 |
| One pipeline (steps JSON) | 64 KiB |
| Script source | 40 KiB |
| Map table | 256 entries |
| `max_silence` | at least 1 s (default 10 min) |
| A device's configuration (all its elements and pipelines) | 900 KiB (a save that would exceed it is refused) |

---

## 3. Steps

A pipeline is an ordered list of steps. Most act on one `field` of the message: `value` by default, or a path such as `data.temperature`, `sensors[0].t` or `["odd key"]`.

| Step | Configuration | Device → platform | Commands (platform → device) |
|---|---|---|---|
| `pick` | `path` | The object at `path` becomes the message; anything else becomes `{"value": x}` | Not applied |
| `scale` | `field`, `mul` (≠ 0), `add` | `v × mul + add` | `(v − add) / mul` |
| `unit` | `field`, `from`, `to` | Converts within one dimension (temperature, pressure, speed, energy, power, length, volume, flow, mass, time, angle, frequency) | The reverse conversion |
| `round` | `field`, `decimals` (0–6) | Rounds half away from zero | Not applied |
| `clamp` | `field`, `min`, `max` | Keeps the value within `min..max` | Not applied |
| `map` | `field`, `table`, `default` | Replaces the value by the table's entry (keys compare as text: `"1"`, `"ON"`, `"true"`); a value not in the table takes `default`, or fails the message when there is none | Mapped back when no two keys share a value |
| `deadband` | `field`, `abs` and/or `pct`, `max_silence` | Drops a value whose change from the last published value is below every threshold; always passes after `max_silence` | Not applied |
| `drop_if` | `field`, `op` (`<` `<=` `>` `>=` `==` `!=`), `value` (a number) | Drops the message when the condition holds; non-numbers pass | Not applied |
| `script` | `source` | `function transform(msg, ctx)` returns the message, or `null` to drop it | `function untransform(msg, ctx)`, if the script defines one |

`GET /api/v1/admin/pipelines/units` lists the units and their aliases (`degC`, `°C`, `celsius`…).

A script's `ctx` holds `element` and `device` (names), `time` (now, RFC 3339), `last` (the last published message, or `null`) and `last_time` (when it was published, or `null`).

```js
// dew point from temperature and humidity
function transform(msg, ctx) {
  var a = 17.62, b = 243.12
  var g = Math.log(msg.h / 100) + (a * msg.t) / (b + msg.t)
  return { value: Math.round(((b * g) / (a - g)) * 10) / 10, t: msg.t, h: msg.h }
}
```

### Order rules

- A `deadband` compares with the last **published** message, which is the pipeline's output. A step after it that changes the field it compares (or the whole message: `pick`, `script`) would make it compare different units, so that order is refused: put the deadband after the steps that change its field.
- Save, preview and test all validate the steps the same way. `POST /api/v1/admin/pipelines/validate` checks steps without saving them.

---

## 4. Commands: the pipeline in reverse

Dashboards and history keep engineering units (`50 %`, `"ON"`). When a user sends a command, the steps that apply to commands run **in reverse order** on the message before it reaches the device (`50 %` → `1500 rpm`, `"ON"` → `"OPEN"`). The other steps are skipped: rounding, clamping and filtering lose nothing a device needs.

| Where | What happens when the reverse fails |
|---|---|
| The user's browser socket (`message_element`) | Checked first: the command is refused with an error frame `pipeline_failed` and isn't published |
| Device frames (WebSocket, REST sync, gRPC), built in `renderMessage` | The device gets nothing, never the unconverted value |
| MQTT downlinks (`notifyCommand`) | Already refused where the user sent it (first row). A gateway that still can't convert it (one holding another pipeline version) doesn't send it and logs a warning |

A pipeline where a value-changing step doesn't apply to commands (a `pick`, a `map` with repeated values, a script without `untransform`) still delivers commands, just without that step. The admin UI warns about it: the validate endpoint's `round_trips` is false.

When the device echoes its state in its own units, the forward pipeline turns it back into engineering units, and the dashboard confirms the command.

---

## 5. In the gateway

In `publishDeviceMessage` (`internal/gateway/core.go`):

```
validate → resolve element → client message-id dedupe → ELEMENT PIPELINE → element rate limit → publish
```

| Outcome | Status (REST, gRPC) | MQTT | Bus |
|---|---|---|---|
| Passed | `accepted` (or `coalesced` / rate-limited by the limit after it) | published | the transformed message, with the CloudEvent extension `quackpipeline: <version>` |
| Dropped by `deadband`, `drop_if` or a script returning `null` | `filtered` | acknowledged, counted in the connection's `filtered` counter | nothing; no rate-limit token is used |
| A step failed (wrong type, script error or timeout, value not in a map) | `pipeline_failed` with the reason | not published | a record on `element-pipeline.dlq.v1` |

On a device WebSocket a failure is an error frame `pipeline_failed`.

The dead-letter record is plain JSON (not a CloudEvent):

```json
{
  "element_id": "…", "device_id": "…", "pipeline_version": 3,
  "step": "scale", "reason": "field \"value\" is missing or not a number",
  "message": "{\"value\":\"n/a\"}", "message_size": 17,
  "gateway_id": "gw-1", "time": "2026-10-14T09:00:00Z"
}
```

`message` is the device's message as text, cut to its first 4 KiB; `message_size` is its full size.

### Deadband across gateways

The deadband's "last value" is the newest value the gateway accepted for this element: a message held by `over_limit: latest` if there is one, else the latest message in the gateway hub (`hub.LatestMessage`). The hub sees every message on `element-events.v1`, so a device that reconnects to another gateway is compared with the same value, with no shared store. `max_silence` lets a value through when nothing was published for that long, so a quiet sensor still reports.

---

## 6. Admin API

| Endpoint | |
|---|---|
| `GET /api/v1/admin/elements/{id}/pipeline` | The current pipeline and every version |
| `PUT /api/v1/admin/elements/{id}/pipeline` | `{"steps": [...]}`: validates, saves a new version and republishes the device's configuration in one transaction |
| `POST /api/v1/admin/elements/{id}/pipeline/rollback` | `{"version": n}`: saves a new version with the steps of version `n` |
| `DELETE /api/v1/admin/elements/{id}/pipeline` | Saves an empty version (values pass as sent); older versions stay for rollback |
| `POST /api/v1/admin/elements/{id}/pipeline/preview` | Runs steps over the element's last 500 stored messages |
| `POST /api/v1/admin/elements/{id}/pipeline/test` | Runs steps on one message (`message`, optional `last`) and sends the output back through the command path |
| `POST /api/v1/admin/pipelines/validate` | `{valid, error, invertible, round_trips, steps_invertible}` |
| `GET /api/v1/admin/pipelines/units` | The units the `unit` step knows |

### Preview: what the steps run on

Stored messages are the output of the pipeline version active when they were stored, not what the device sent. The preview first turns each one back into a device value, and says how it did, in `basis`:

| `basis` | Input of the steps |
|---|---|
| `raw` | No pipeline was active: the stored value is what the device sent |
| `reconstructed` | The reverse of the version active then (rounding and clamping can't be undone exactly) |
| `stored` | That version can't be reversed, so the steps run on the stored value |
| `mixed` | Rows differ; each row has its own `basis` |

```json
{
  "basis": "reconstructed",
  "note": "The stored messages are pipeline output; …",
  "summary": { "total": 500, "passed": 482, "filtered": 18, "failed": 0 },
  "rows": [
    { "time": "2026-10-10T06:00:00Z", "basis": "reconstructed",
      "stored": {"value": 50.0}, "before": {"value": 512}, "after": {"value": 50.0}, "filtered": false }
  ]
}
```

Messages that a pipeline filtered were never stored, so a preview can't show them.

### Test

```json
POST /api/v1/admin/elements/{id}/pipeline/test
{ "steps": [{"kind": "scale", "mul": 0.1}], "message": {"value": 512} }

{ "after": {"value": 51.2}, "filtered": false,
  "inverse": {"value": 512}, "invertible": true, "steps_invertible": [true] }
```

`inverse_error` says why a command with that value would be refused.

The admin UI (Admin → Elements → Pipeline) is built on these endpoints: step editor with per-step "also applied to commands" badges from `validate`, preview chart and table, test, versions and rollback.

---

## 7. Metrics

| Metric | Type | Labels | |
|---|---|---|---|
| `quack_pipeline_seconds` | histogram | `kind` = `builtin` \| `script` | Time per message |
| `quack_pipeline_filtered_total` | counter | `reason` = `deadband` \| `drop_if` \| `script` | Messages dropped by a step |
| `quack_pipeline_failed_total` | counter | `step` (`inverse` for commands) | Failures |
| `quack_pipeline_compile_errors_total` | counter | | Versions that didn't compile on a gateway |
| `quack_mqtt_values_filtered_total` | counter | | Values from MQTT messages a pipeline dropped |

The `element-pipeline.dlq.v1` topic has 3 partitions and 30 days of retention.

Measured costs are in [the baseline](../refactor/baseline.md#element-pipeline-benchmarks).
