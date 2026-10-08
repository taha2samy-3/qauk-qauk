---
layout: home
title: Quack Quack
titleTemplate: Real-time IoT platform

hero:
  name: Quack Quack
  text: Real-time IoT dashboards, device control and history
  tagline: Devices and Node-RED stream over WebSockets to a Go gateway. Users build live drag-and-drop dashboards. Every value lands in a pluggable history store: TimescaleDB or ClickHouse.
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: Architecture
      link: /guide/architecture
    - theme: alt
      text: API reference
      link: /api/

features:
  - icon: ⚡
    title: Realtime gateway in Go
    details: One binary holds tens of thousands of sockets. In our load test it fanned out 20,000 deliveries/s with 0% loss and a 22 ms p99.
    link: /design/performance-baseline
  - icon: 🧩
    title: Drag-and-drop dashboards
    details: Gauges, charts, switches, sliders and status widgets bound to live elements, with responsive layouts and sharing.
    link: /concepts/dashboards
  - icon: 🔐
    title: Secure by default
    details: Device JWTs (RS256/ES256, exp required), argon2id sessions, CSRF and Origin checks, and live permission re-evaluation.
    link: /concepts/authentication
  - icon: 📈
    title: History built in
    details: Every message is stored in TimescaleDB or ClickHouse (one setting), with per-attribute 1-minute rollups, retention and compression; dashboards backfill charts from it.
    link: /concepts/history
  - icon: 🛰️
    title: Event bus with standards
    details: Redpanda topics carrying CloudEvents 1.0, JSON Schema and AsyncAPI, plus a transactional outbox, so no change is ever lost.
    link: /concepts/realtime_events
  - icon: 🧩
    title: Node-RED nodes
    details: "quack out sends readings, quack in receives dashboard commands. Install: npm install the tarball from the GitHub release in ~/.node-red."
    link: /guide/node-red
  - icon: 🔁
    title: Drop-in for existing devices
    details: The legacy WebSocket protocol is byte-compatible, so Node-RED flows and device keys keep working after migration.
    link: /api/device_api
---

<div class="home-hi">
  <WavingDuck variant="hi" :width="360" alt="A duck waving and saying Hi there" />
  <div>
    <h2>New here? Start in five minutes</h2>
    <p>Run the whole stack locally with demo devices that stream live values:</p>

```sh
mise install && task infra:up && task demo
task dev        # API + gateway on :8080
task simulate   # demo devices streaming values
task web:dev    # dashboard on http://127.0.0.1:5173 (admin / admin12345)
```

  </div>
</div>

<style>
.home-hi { display: grid; grid-template-columns: minmax(0, 360px) 1fr; gap: 32px; align-items: center; max-width: 1152px; margin: 48px auto 0; padding: 0 24px; }
.home-hi h2 { margin-top: 0; border-top: none; }
@media (max-width: 768px) { .home-hi { grid-template-columns: 1fr; } }
</style>
