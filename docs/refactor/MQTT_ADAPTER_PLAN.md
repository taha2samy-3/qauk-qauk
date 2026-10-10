# MQTT Adapter Implementation Plan & Status

This document tracks the progress of the MQTT adapter integration as requested.

## Status Overview

| Phase | Description | Status |
|---|---|---|
| **Phase A** | Platform Config & API (Schema, Outbox, Registry Sync, Store CRUD) | 🟡 In Progress (Missing REST API endpoints and Secrets Encryption) |
| **Phase B** | Source Pipeline (JavaScript Decoder Sandbox, Declarative Field Map) | 🟢 Complete (Tests passing) |
| **Phase C** | Membership, HRW Assignment, Uplink logic | 🔴 Not Started |
| **Phase D** | Downlink, Replicas, SCRAM auth | 🔴 Not Started |
| **Phase E** | UI, Dev stack (Mosquitto), CI, Docs | 🟡 In Progress (Docs updated, React UI pending) |
| **Phase F** | Future split deployment | 📝 Designed for, not implemented yet |

## What Was Built

### Phase A (Platform Config)
- **Database Schema:** `00006_mqtt.sql` created with tables for `mqtt_connections`, `decoders`, `decoder_versions`, `mqtt_uplinks`, `mqtt_downlinks`, `mqtt_connection_devices`, and `mqtt_connection_status`.
- **Outbox & Syncing:** `GetMQTTConfig` correctly bundles all relationships. `publishMQTTConnection` queues a snapshot to Redpanda (`mqtt-config.v1`), and `SyncMQTTConfigs` fires automatically during boot via `main.go`.
- **Store CRUD:** Added `Insert` and `Get` methods for connections, uplinks, downlinks, decoders, and granted devices across `store/mqtt.go` and `store/mqtt_extended.go`. 

### Phase B (Source Pipeline)
- **Decoder Sandbox (`internal/sourcepipe`):** Integrated `goja` to provide a TTN/ChirpStack-compatible JavaScript sandbox. Includes a 20ms execution timeout via `vm.Interrupt()` to prevent locking up the gateway.
- **Field Map Engine:** Added `sourcepipe.Process` which executes the decoder (if present), falls back to JSON decoding, and runs the result through a declarative JSON path mapping engine (including `when: exists` skipping logic).
- **Tests:** `goja` float64 casting panics were resolved. All tests pass cleanly (`mise exec -- task check`).

### Phase E (Documentation)
- **Architecture (`02_architecture.md`):** Updated the Mermaid diagram and `quack serve` roles table to include the new `mqtt` role.
- **Getting Started (`03_getting_started.md`):** Added `task start MQTT=1`.
- **Core Concepts (`05_core_concepts/realtime_events.md`):** Added the new `quackvia` extension attribute to the CloudEvents section.
- **Screenshots (`07_screenshots.md`):** Added a placeholder for the MQTT administration UI.
- **Readme:** Added MQTT 5 support to the platform features list.

## Deviations

- **Store CRUD completeness:** We skipped the explicit `Update` and `Delete` methods in the `store` for uplinks, downlinks, and decoders initially, opting to just scaffold `Insert` and `Get` to satisfy the config snapshot requirement first.
- **Integration Tests:** The full `Mosquitto` integration test suite requested in the prompt could not be written yet, as those tests require the actual `internal/gateway/mqtt` transport to exist (Phase C).

## Open Items

1. **Phase A:** Write the remaining Admin REST API endpoints (huma) for connections, uplinks, and downlinks.
2. **Phase A:** Implement AES-256-GCM encryption for secrets at the database layer.
3. **Phase C:** Add Mosquitto to `docker/compose.yaml` and implement the Paho MQTT 5 client (`internal/gateway/mqtt`) and HRW assignment logic.
4. **Phase E:** Write the React front-end components in `web/src/features/admin/mqtt`.
