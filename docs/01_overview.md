# 1. Overview

Quack Quack is the realtime core of an IoT system. It sits between **devices**, meaning anything that can open a WebSocket and sign a JWT (an ESP32, a Raspberry Pi, a Node-RED flow), and **people** who watch and control those devices from a browser.

## Goals

- **Low latency in both directions.** Telemetry reaches dashboards, and commands reach devices, in milliseconds. A slow or misbehaving client never slows everyone else down.
- **Secure by default.**
  - Devices prove their identity with asymmetric keys.
  - Users get exactly the access they were granted, per element.
  - Permission changes take effect on open connections immediately.
  - Clients cannot forge who sent a message.
- **Nothing gets lost.** Every message is written to a time-series database, and every admin change goes through a transactional outbox. History survives restarts.
- **Scales horizontally without code changes.** Run more gateway instances behind a load balancer, and more ingesters for write throughput.
- **Backward compatible.** Devices and Node-RED flows built for the legacy Django server keep working: same WebSocket paths, frames, JWT scheme and UUIDs.
- **Easy to operate.** One static binary, configured only through environment variables, with `/healthz`, `/readyz` and Prometheus `/metrics`.

## Concepts in one minute

| Term | Meaning |
|---|---|
| **Device** | A client that connects to `/device/node_red/` with a JWT signed by its private key. It has a UUID and is assigned one public key. |
| **Element** | One data point or control that belongs to a device: a temperature sensor, a pump switch, a fan-speed slider. It has a UUID, a free-form `details` JSON (unit, range, title) and `points`, the number of recent values replayed to new viewers (0–1000). |
| **Message** | Any JSON value sent for an element, for example `{"value": 21.5}`. A device sends telemetry; a user sends a command. |
| **Grant** | `R` (read) or `RC` (read and control) on one element, for a user or a group. |
| **Dashboard** | A user-owned grid of widgets bound to elements. It can be shared read-only with all users. |
| **Gateway** | The WebSocket role of `quack serve`. It authenticates sockets, fans out messages and tracks device presence. |

## Features

- A realtime gateway: the device and browser WebSocket protocols, in-memory fan-out, a history replay window per element, presence with leases, bounded queues and rate limits.
- A REST API (OpenAPI 3.1) for login, the current user's elements, element history, dashboards and a full admin surface (users, groups, keys, devices, elements, styles, permissions, connections, presence, audit log).
- A React web app with drag-and-drop dashboards, live widgets, history charts with time-range selection, admin pages, and light and dark themes.
- An ingester that writes every element event from Redpanda into the history store (TimescaleDB or ClickHouse), batched and idempotent, with a dead-letter topic for unstorable data.
- Admin tooling: `quack admin create-user | set-password | import-key`, and `quack import-django` for migration.
- Quality gates: unit and integration tests, a 62-test black-box WebSocket contract suite, a load test and Playwright end-to-end tests.

## Tech stack

| Layer | Technology | Role |
|---|---|---|
| Backend language | [Go](https://go.dev/) 1.26 | One binary `quack` with the subcommands `serve`, `ingest`, `migrate`, `admin`, `import-django`, `dev` |
| HTTP and REST | [chi](https://github.com/go-chi/chi) v5, [huma](https://huma.rocks/) v2 | Routing, an OpenAPI 3.1 spec generated from Go types, request validation, docs UI at `/api/docs` |
| WebSockets | [coder/websocket](https://github.com/coder/websocket) | Device and browser sockets |
| Database | [PostgreSQL](https://www.postgresql.org/) 17 | Relational data: identity, devices, permissions, dashboards, outbox, audit |
| History store | [TimescaleDB](https://www.timescale.com/) (default) or [ClickHouse](https://clickhouse.com/), via [clickhouse-go](https://github.com/ClickHouse/clickhouse-go) v2 | Pluggable time-series backend: events, per-attribute points and 1-minute rollups ([History storage](./05_core_concepts/history.md)) |
| DB access and migrations | [pgx](https://github.com/jackc/pgx) v5 (hand-written SQL), [goose](https://github.com/pressly/goose) v3 | SQL migrations embedded in the binary |
| Event bus | [Redpanda](https://redpanda.com/) (Kafka API) via [franz-go](https://github.com/twmb/franz-go) | `element-events.v1`, `control-events.v1`, `presence.v1`, `element-state.v1`, `element-events.dlq.v1` |
| Event format | [CloudEvents](https://cloudevents.io/) 1.0 + JSON Schema 2020-12 | Bus envelope and payload schemas (`server/schemas/`) |
| Auth | [golang-jwt](https://github.com/golang-jwt/jwt) v5, argon2id (`x/crypto`) | Device JWTs (RS256/ES256), user passwords, opaque session cookies |
| Observability | `log/slog` (JSON), [Prometheus client](https://github.com/prometheus/client_golang) | Structured logs and `/metrics` |
| Frontend | [React](https://react.dev/) 19, [Vite](https://vite.dev/) 8, TypeScript, [Tailwind CSS](https://tailwindcss.com/) 4 | Web app in `web/` |
| Frontend libraries | [react-grid-layout](https://github.com/react-grid-layout/react-grid-layout), [ECharts](https://echarts.apache.org/) 6, TanStack Query, Radix UI, zod, openapi-fetch | Dashboard grid, charts and gauges, data fetching, a typed API client |
| Testing | `go test -race`, integration tests against the compose infra (`quack_it` database), contract suite, Vitest, Playwright | `task check`, CI |
| Tooling | [mise](https://mise.jdx.dev/), [Task](https://taskfile.dev/), golangci-lint, Docker Compose | Pinned toolchain; every command is a task |
| Delivery | Multi-stage Dockerfile (distroless, non-root), GitHub Actions, Dependabot | One image holds the backend and the built frontend |
