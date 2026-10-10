# Element Pipeline

The **Element Pipeline** (Station 2) runs in-memory transformations and filters on device telemetry **locally inside each gateway**, after element resolution and client deduplication, but **before** messages are published to Redpanda and evaluated against rate limits.

---

## 1. Where It Sits: Three Stations, One Contract

```
(1) Source mapping            (2) Element pipeline              (3) Derived elements
    per connection                per element                       virtual elements
    raw → element values   →      pick/scale/deadband/…/script  →   CloudEvent on Redpanda  →  formula/window → CloudEvent
    MQTT, HRW slots               in gateway memory closures,        (background bus)            consumer in background,
                                  before Redpanda                                               back through the core
    ──────────────────────────────▲ THIS STATION
```

- **Station 1 (Source mapping):** Per-connection raw payload decoders (e.g. TTN/ChirpStack JavaScript decoders and field maps for MQTT topics), distributed across gateways using Highest Random Weight (HRW) hashing.
- **Station 2 (Element pipeline):** Per-element compiled Go closures running locally in gateway memory. Applies uniformly to every transport: WebSocket, REST, gRPC, MQTT, and Node-RED. Every gateway receives element pipelines from `device-config.v1`.
- **Station 3 (Derived elements):** Virtual elements computed over streams (formulas, rolling averages, windows), executing as background bus consumers that publish back through the core.

### The Single Contract
Everything published to the event bus is an `io.quack.element.message.v1` CloudEvent. Pipeline steps return transformed data or indicate filtering; they never write directly to the bus, access databases, or open network sockets.

---

## 2. Hard Rules & Architecture

1. **Hot Path Budget:** Built-in steps are pure closures executed with no reflection per message and zero allocations beyond standard JSON parsing. A 5-step built-in pipeline executes in **< 2 µs** (well under the 5 µs budget). An element without a pipeline pays only a single `nil` pointer check (~3.8 ns).
2. **Compile Once:** Pipelines are compiled when the device configuration snapshot arrives (`device-config.v1`), never per message. If a snapshot's pipeline fails to compile, the gateway logs the error, increments `quack_pipeline_compile_errors_total`, and retains the previous working compiled pipeline.
3. **Pure Package Boundary:** `server/internal/elementpipe` compiles and runs pipelines with zero dependencies on gateway, store, service, database, or bus packages. This enables the API server to run the identical code for live historical previews.
4. **Sandboxed JavaScript Engine:** The `script` step uses the Goja runtime with strict limits: 20 ms interrupt timeout, 64 KiB input/output ceiling, no filesystem/network access, and rejection of `NaN`/`Infinity`.

---

## 3. Pipeline Steps

A pipeline is an ordered list of up to 32 steps on an element. Most steps operate on a specified `field` (defaulting to `value`):

| Step | Configuration | Uplink (Device → Platform) | Commands (Platform → Device) |
|---|---|---|---|
| `pick` | `path` | Extracts target path as `{"value": <path>}` | Not applied |
| `scale` | `field`, `mul`, `add` | `v × mul + add` (`mul ≠ 0`) | **Inverted:** `(v − add) / mul` |
| `unit` | `field`, `from`, `to` | Canonical unit conversion lookup table | **Inverted:** Reversed conversion |
| `round` | `field`, `decimals` (0–6) | Round half away from zero | Not applied |
| `clamp` | `field`, `min`, `max` | Restricts value between min and max | Not applied |
| `map` | `field`, `table`, `default` | Key-value dictionary translation | **Inverted** if table is one-to-one |
| `deadband` | `field`, `abs`, `pct`, `max_silence` | Drops if delta from last value is below threshold | Not applied |
| `drop_if` | `field`, `op`, `value` | Drops if condition (`<`, `<=`, `>`, `>=`, `==`, `!=`) holds | Not applied |
| `script` | `source` (JS) | `function transform(msg, ctx) { return msg }` | Optional `function untransform(msg, ctx)` |

### Step Invertibility & Commands
When a user issues a command from a dashboard (e.g. setting a slider or toggling a switch):
- Dashboards and history keep engineering units (e.g. `50%` or `"ON"`).
- Device-facing frames run invertible steps in **reverse order** before transmission to the hardware (e.g. converting `50%` to `1500 rpm` or `"ON"` to `"OPEN"`).
- Inversion occurs at two integration points:
  1. `renderMessage` in `internal/gateway/frames.go` for WebSocket, REST sync, and gRPC device frames.
  2. `notifyCommand` in `internal/gateway/mqtt_bridge.go` for MQTT downlink messages.

When the hardware echoes its state in device units, the uplink pipeline transforms it back to engineering units for dashboard confirmation.

---

## 4. Execution Order & Gateway Integration

Inside `publishDeviceMessage` (`internal/gateway/core.go`):

```
Validation → Resolve Element → Client-ID Dedupe → ELEMENT PIPELINE → Element Limit → Publish
```

### Result Statuses
- **Filtered (`filtered`):** Values dropped by `deadband`, `drop_if`, or a `script` step returning `null`.
  - Filtered values **do not consume rate limit tokens**.
  - On REST and gRPC, returns per-item status `"filtered"`.
  - On MQTT, acknowledged (`Done(nil)`) so QoS 1 messages do not loop.
- **Pipeline Failed (`pipeline_failed`):** Triggered when a step encounters invalid types (e.g. non-numeric input to `scale`) or a script throws/times out.
  - Returned as `ErrPipeline{Step, Reason}`.
  - Emits error frame on WebSockets, returns status `"pipeline_failed"` with reason on REST/gRPC, and routes rejection to DLQ on MQTT.
  - Automatically logged and written to the dedicated topic **`element-pipeline.dlq.v1`**.
- **CloudEvent Pipeline Tag:** Whenever an element pipeline transforms a message, the CloudEvent carries the extension attribute:
  ```json
  "quackpipeline": 1
  ```

---

## 5. Deadband Across Gateways

Deadband compares the current telemetry against the **latest known value in the gateway hub** (`hub.LatestMessage`), which is updated by every broadcast on `element-events.v1`.

Even if a device roams between gateways or switches connections, deadband remains globally consistent across the cluster in O(1) memory lookup time without querying a shared database.

To ensure quiet devices remain monitored, deadband supports `max_silence` (defaulting to 10 minutes). When no value has passed the deadband threshold within `max_silence`, the step forces the current message through to serve as a periodic heartbeat.

---

## 6. Admin API & History Preview

Administrators configure pipelines via the authenticated REST API:
- `GET /api/v1/admin/elements/{id}/pipeline`: Retrieves current pipeline configuration and historical versions.
- `PUT /api/v1/admin/elements/{id}/pipeline`: Validates, compiles, saves a new version, and republishes the device snapshot in a single database transaction.
- `POST /api/v1/admin/elements/{id}/pipeline/rollback`: Creates a new active version duplicating a previous version's steps.
- `DELETE /api/v1/admin/elements/{id}/pipeline`: Clears the pipeline for raw passthrough.
- `POST /api/v1/admin/elements/{id}/pipeline/preview`: Runs the proposed pipeline over the element's **last 500 stored device messages** in chronological order, returning:
  ```json
  {
    "summary": { "total": 500, "passed": 482, "filtered": 18, "failed": 0 },
    "rows": [
      { "time": "2026-10-10T06:00:00Z", "before": {"value": 512}, "after": {"value": 50.0}, "filtered": false }
    ]
  }
  ```
- `POST /api/v1/admin/elements/{id}/pipeline/test`: Evaluates single test messages and calculates forward transformation and command inverse.

---

## 7. Metrics & Observability

| Metric | Type | Labels | Description |
|---|---|---|---|
| `quack_pipeline_seconds` | Histogram | `kind="builtin"\|"script"` | Execution duration per pipeline message |
| `quack_pipeline_filtered_total` | Counter | `reason="deadband"\|"drop_if"\|"script"` | Messages filtered out by pipeline steps |
| `quack_pipeline_failed_total` | Counter | `step="scale"\|...` | Failures per pipeline step |
| `quack_pipeline_compile_errors_total` | Counter | — | Compilation failures on incoming snapshots |
| `element-pipeline.dlq.v1` | Redpanda Topic | 3 partitions, 30-day retention | Unhandled message failures with truncated payloads |
