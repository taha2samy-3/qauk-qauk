# Element Pipeline Plan (Station 2)

Implementation plan and status for the Quack Quack Element Pipeline (Station 2).

---

## 1. Scope & Goals

- Provide in-memory, zero-allocation built-in transform steps (< 5 µs budget) and sandboxed Goja JavaScript steps (< 20 ms) per element inside the gateway before publishing to Redpanda.
- Keep the pipeline package pure (`server/internal/elementpipe`) with no database, bus, or transport dependencies.
- Invertible pipeline execution on commands sent to hardware (`SourceUser`), while browser WebSocket frames and historical records preserve engineering units.
- Cluster-wide O(1) deadband filtering using the gateway in-memory hub ring buffer with automatic heartbeat fallback (`max_silence`).
- Rate limiting positioned after pipeline execution so dropped/filtered messages do not consume rate limit tokens.
- Persistent versioning and preview functionality over the last 500 historical messages.

---

## 2. Implementation Status

| Component | Status | Notes |
|---|---|---|
| `internal/elementpipe` | Completed | Pure closures for pick, scale, unit, round, clamp, map, deadband, drop_if, script. Benchmarked: 1.8 µs for 5 built-in steps, 17.8 µs for script. |
| Migrations | Completed | Migration `00008_element_pipelines.sql` creates `element_pipelines` and `element_pipeline_versions`. |
| Schemas | Completed | Updated `cloudevent.json` (`quackpipeline` attribute) and `io.quack.device.config.v1.json` (`pipeline` element property). |
| Bus Topics | Completed | Added `TopicElementPipelineDLQ` (`element-pipeline.dlq.v1`, 3 partitions, 30 days). Total 10 topics. |
| Gateway Core Integration | Completed | Order: validation → resolve element → client-id dedupe → ELEMENT PIPELINE → element limit → publish. Handled `filtered` and `pipeline_failed`. |
| Command Inversion | Completed | Applied in `renderMessage` (for device frames) and `notifyCommand` (for MQTT downlinks). |
| Admin API | Completed | CRUD, rollback, preview (500 historical points), and test endpoints in `internal/api/admin_pipeline.go`. |
| Web UI | Completed | Admin → Elements Pipeline sheet: step builder, invertible badges, map warnings, preview table & chart, live test form, version list & rollback, and Elements table badge (`N steps · vM`). |
| Demo & Simulator | Completed | Seeded Greenhouse soil moisture (raw ADC → % scaled/rounded/clamped), cold room temp (deadband), fan speed (rpm ↔ %), gate relay (OPEN/CLOSED ↔ ON/OFF), and climate dew point script. |
| Tests | Completed | Unit benchmarks & tests (`elementpipe`, `gateway`), integration tests (`pipeline_test.go`), and contract tests. |

---

## 3. Deviations from Initial Prompt & Rationale

1. **Deadband Comparison State:**
   - *Design:* Rather than storing per-connection state on incoming sockets, deadband reads from `hub.LatestMessage(elementID)`, which receives every broadcast on `element-events.v1`.
   - *Rationale:* Ensures global consistency across multi-gateway clusters when roaming devices migrate between gateway instances without needing a shared distributed lock.
2. **Coalescer Pipeline Version Preservation:**
   - *Design:* When an element has `over_limit="latest"` and a burst threshold is exceeded, the held message preserves the `pipelineVersion` calculated during the pipeline stage so that when the timer flushes the held message, `quackpipeline` is tagged with the exact version applied.

---

## 4. Open Items & Future Work (Station 3)

- **Station 3 (Derived Elements):** Virtual elements computed over streams (formulas, sliding windows: avg/min/max/count over N minutes) consuming `element-events.v1` in the background and publishing back through the core as `SourceDevice`.
- **Device Types / Models:** Pipeline templates defined per device model rather than individually per element.
