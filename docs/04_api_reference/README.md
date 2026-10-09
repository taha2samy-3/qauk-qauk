# 4. API reference

Quack Quack exposes these interfaces. All of them are served by `quack serve` on the same port (8080 by default).

| API | Path | Who uses it | Auth |
|---|---|---|---|
| [Device WebSocket API](./device_api.md) | `/device/node_red/` | Firmware, gateways, Node-RED flows | `Authorization: Bearer <JWT>` signed with the device's private key |
| [Device REST API](./device_rest_api.md) | `/device/v1/*` | Devices that wake, send and sleep; scripts; serverless | The same device JWT |
| [Device gRPC API](./device_grpc_api.md) | `/quack.device.v1.DeviceService/*` | gRPC clients, gateways with generated code | The same device JWT (metadata) |
| [Browser WebSocket API](./browser_api.md) | `/browser/simple/` | The web app or any client acting as a user | `quack_session` cookie, plus an Origin check |
| [REST API](./rest_api.md) | `/api/v1/*` | The web app, admin scripts, integrations | `quack_session` cookie; CSRF via cross-origin protection |

The live, always-current REST reference is at **`/api/docs`** (OpenAPI 3.1 at `/api/openapi.json`).

### Which device transport?

| | WebSocket | REST | gRPC |
|---|---|---|---|
| Connection | Long-lived | One request per batch | HTTP/2; unary or long-lived streams |
| Commands to the device | Pushed | Long-poll (`/sync`), newest value per element | Pushed (`Watch`, `Session`) |
| Errors back to the device | None (dropped silently, protocol v1) | Per message | Per message |
| Retries without duplicates | | Message `id` | Message `id` |
| SenML | | Yes | |
| Best for | Node-RED, always-on devices | Sleepy devices, scripts, anything with curl | Typed clients, gateways with many devices |

![Three transports and Node-RED on one dashboard](../imgs/screenshots/transports-dashboard-light.webp)

The demo (`task start`) runs one device on each transport: *Greenhouse A* over WebSocket, *Boiler room* over REST and *Weather station* over gRPC (`task simulate TRANSPORT=…` picks one for all).

## Conventions

- **JSON everywhere.** WebSocket frames are single JSON objects in text frames.
- **Timestamps** are RFC 3339. WebSocket frames use UTC with milliseconds (`2026-10-07T10:00:00.123Z`).
- **IDs.** Devices, elements, keys and dashboards use UUIDs (v7 for new rows; imported legacy ids are kept as they are). Users, groups, styles and grants use integers.
- **Permissions** are `R` (read) and `RC` (read and control). See [Permissions](../05_core_concepts/permissions.md).
- **Compatibility.** The two WebSocket protocols are the legacy v1 protocol, kept byte-compatible with the Django server. Each page ends with a "Changes from the legacy server" section that lists the deliberate fixes.
- **Events on the bus** (Redpanda) are not a client API. They are documented in [Realtime events](../05_core_concepts/realtime_events.md) for people who build backend consumers.
