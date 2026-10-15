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
| `internal/elementpipe` | Completed | Go closures for pick, scale, unit, round, clamp, map, deadband, drop_if; script via the `jsbox` sandbox (20 ms, size and recursion caps). Copy-on-write messages. Measured: ~1.6 µs for 5 built-in steps, ~8.7 µs for a script step ([baseline](./baseline.md#element-pipeline-benchmarks)). |
| Migrations | Completed | `00008_element_pipelines.sql`: `element_pipelines` and `element_pipeline_versions`. A delete saves an empty version, so version numbers never repeat. |
| Schemas | Completed | `cloudevent.json` (`quackpipeline` = the version), `io.quack.device.config.v1.json` (`pipeline` per element), `io.quack.command.failed.v1`. |
| Bus Topics | Completed | `element-pipeline.dlq.v1` (3 partitions, 30 days): plain JSON with the message cut to 4 KiB and its full size. |
| Gateway Core Integration | Completed | validation → resolve element → client-id dedupe → ELEMENT PIPELINE → element limit → publish. Statuses `filtered` and `pipeline_failed` on every transport; MQTT counts `filtered`. |
| Command Inversion | Completed | Reverse steps in `renderMessage` (device frames, built lazily) and `notifyCommand` (MQTT). A command the reverse can't convert is refused where the user sends it (error frame `pipeline_failed`) and never delivered unconverted. MQTT delivery failures reach the user over `io.quack.command.failed.v1`. |
| Admin API | Completed | get, save, rollback, delete, preview (500 stored messages, with `basis`), test (with the inverse), validate, units. |
| Web UI | Completed | Admin → Elements → Pipeline: step editor (numbers kept as typed, map tables checked), per-step command badges and errors from `validate`, unit suggestions, preview chart and table (stored / input / output), test, versions and rollback. |
| Demo & Simulator | Completed | Greenhouse soil moisture (raw ADC → %), cold room temperature (deadband), fan speed (rpm ↔ %), gate relay (OPEN/CLOSED ↔ ON/OFF), climate dew point script. Pipelines are re-saved only when they changed. |
| Tests | Completed | Unit tests and benchmarks (`elementpipe`, `jsbox`, `gateway`), integration tests (`pipeline_test.go`), web unit tests (`pipelineSteps.test.ts`), e2e (`pipeline.spec.ts`). |

---

## 3. Design decisions

1. **Deadband compares with the last accepted value.** It reads the message held by `over_limit: latest` if any, else `hub.LatestMessage(elementID)`, which sees every message on `element-events.v1`: consistent across gateways with no shared state. `max_silence` uses the time that value was published.
2. **Deadband order rule.** The "last value" is pipeline output, so a step after a deadband that changes its field (or `pick`/`script`) is refused at validation.
3. **Fail closed.** A version that doesn't compile on a gateway keeps the last working one; with none, the element's messages fail instead of being published in device units.
4. **Held messages keep their version.** A message held by `over_limit: latest` keeps the pipeline version that transformed it, so `quackpipeline` is right when it is flushed (also on shutdown).
5. **Previews say what they ran on.** Stored values are pipeline output; the preview reverses the version active then when it can (`reconstructed`), else runs on the stored value (`stored`).

---

## 4. Open Items & Future Work (Station 3)

- **Station 3 (Derived Elements):** Virtual elements computed over streams (formulas, sliding windows: avg/min/max/count over N minutes) consuming `element-events.v1` in the background and publishing back through the core as `SourceDevice`.
- **Device Types / Models:** Pipeline templates defined per device model rather than individually per element.
