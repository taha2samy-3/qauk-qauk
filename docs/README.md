# Quack Quack documentation

<p align="center">
  <img src="./imgs/logo-wordmark.svg" alt="Quack Quack" width="300"/>
</p>

Quack Quack is a realtime IoT platform. Devices and Node-RED flows stream telemetry over an authenticated WebSocket. Users watch it on drag-and-drop dashboards and send commands back. Per-element permissions decide who sees and controls what, and every message is stored in TimescaleDB.

These docs are for people who **run** the platform (operators), **integrate** with it (firmware and Node-RED authors), and **extend** it (backend and frontend developers).

## Where to start

| If you want to... | Read |
|---|---|
| Get it running locally in five minutes | [Getting started](./03_getting_started.md) |
| Connect a device or a Node-RED flow | [Getting started → Connect a device](./03_getting_started.md#connect-a-device-or-node-red) and the [Device API](./04_api_reference/device_api.md) |
| Build a frontend or another WebSocket client | [Browser API](./04_api_reference/browser_api.md) and [REST API](./04_api_reference/rest_api.md) |
| Understand how the pieces fit together | [Architecture](./02_architecture.md) |
| Deploy and scale it | [Architecture → Scaling](./02_architecture.md#scaling) and the [configuration reference](./03_getting_started.md#configuration-reference) |
| Move off the legacy Django server | [Getting started → Migrating from Django](./03_getting_started.md#migrating-from-the-legacy-django-server) and the "Changes from the legacy server" sections in the API docs |

## Table of contents

1. [Overview](./01_overview.md): goals, features and the tech stack.
2. [Architecture](./02_architecture.md): components, sequence diagrams for every data flow, and scaling.
3. [Getting started](./03_getting_started.md): local dev, containers, first admin, connecting devices and Node-RED, environment variables, Django migration.
4. [API reference](./04_api_reference/README.md)
   - [Device WebSocket API](./04_api_reference/device_api.md)
   - [Browser WebSocket API](./04_api_reference/browser_api.md)
   - [REST API](./04_api_reference/rest_api.md)
5. [Core concepts](./05_core_concepts/README.md)
   - [Authentication](./05_core_concepts/authentication.md)
   - [Permissions](./05_core_concepts/permissions.md)
   - [Realtime events](./05_core_concepts/realtime_events.md)
   - [Dashboards](./05_core_concepts/dashboards.md)
6. [Database schema](./06_database/schema.md)
7. [Screenshots](./07_screenshots.md): a tour of the web app
8. [Brand & logos](./08_brand.md): logo files, colors and the welcoming ducks

## Design records

The `refactor/` folder holds the engineering records of the Django → Go rewrite. They are kept for reference, and the documentation above describes the system as built.

- [GO_REDPANDA_TSDB_PLAN.md](./refactor/GO_REDPANDA_TSDB_PLAN.md): the plan, implementation status and the legacy bug list (B1–B11).
- [MESSAGE_FORMATS.md](./refactor/MESSAGE_FORMATS.md): why CloudEvents, JSON Schema, OpenAPI and RFC 9457.
- [baseline.md](./refactor/baseline.md): Django vs Go load-test results.
- [Contract suite README](https://github.com/taha2samy/node_red_-_django-quack_quack-/blob/master/server/contracttest/README.md): the black-box WebSocket tests that define protocol behavior.
