# Screenshots

A tour of the web app, from the demo stack (`task demo` + `task simulate`). The pictures are captured by the
Playwright suite (`web/e2e/screenshots.spec.ts`), so they always show the real UI.

## Sign in

| Light | Dark |
|---|---|
| ![Login page, light theme](./imgs/screenshots/login-light.webp) | ![Login page, dark theme](./imgs/screenshots/login-dark.webp) |

## Live dashboard

Gauges, charts, switches, a slider and a device status widget, all updating in real time. The header shows the live
connection state.

![A live dashboard in the light theme](./imgs/screenshots/dashboard-view-light.webp)

![The same dashboard in the dark theme](./imgs/screenshots/dashboard-view-dark.webp)

## Building a dashboard

Drag widgets from the palette onto the grid, then move and resize them. Undo/redo, unsaved-changes protection and
responsive layouts are built in.

![Dashboard editor with the widget palette, light theme](./imgs/screenshots/dashboard-editor-light.webp)

![Dashboard editor, dark theme](./imgs/screenshots/dashboard-editor-dark.webp)

Every widget has a configuration sheet: title, units, ranges, colored thresholds, decimals and the chart time range.

![Widget configuration sheet](./imgs/screenshots/dashboard-config-light.webp)

## Device transports

One dashboard fed by all three device transports plus Node-RED: *Greenhouse A* over the WebSocket, *Boiler room* over
REST, *Weather station* over gRPC, and the *Packing line* flow in Node-RED. Every switch was flipped in the browser,
reached its device over that device's transport, and was confirmed back. Captured by `web/e2e/protocols.spec.ts`.

![Dashboard fed by WebSocket, REST, gRPC and Node-RED devices, light theme](./imgs/screenshots/transports-dashboard-light.webp)

![The same dashboard, dark theme](./imgs/screenshots/transports-dashboard-dark.webp)

| All four devices online | The Node-RED flow behind *Packing line* |
|---|---|
| ![Devices page with four online devices](./imgs/screenshots/transports-devices-light.webp) | ![Node-RED flow with dashboard commands in the debug sidebar](./imgs/screenshots/transports-nodered-flow.webp) |

| REST with curl | gRPC with buf curl |
|---|---|
| ![curl calls against the REST device API](./imgs/screenshots/transcript-rest-curl.webp) | ![buf curl calls against the gRPC device API](./imgs/screenshots/transcript-grpc-buf-curl.webp) |

## Rate limits per element

| Element list | Element form |
|---|---|
| ![Elements with their rate limits](./imgs/screenshots/admin-element-limits-light.webp) | ![The rate-limit section of the element form](./imgs/screenshots/admin-element-limit-dialog-light.webp) |

## Element pipelines

In **Admin → Elements**, each element has an in-memory transformation pipeline (Station 2) with steps like `scale`, `round`, `clamp`, `deadband`, `map`, and `script`. The pipeline sheet includes an interactive step editor with invertible badges, a live before-and-after preview table and chart over the last 500 history points, a test console with command inversion verification, and version rollback.

| Light | Dark |
|---|---|
| ![Element pipeline sheet with preview and chart, light theme](./imgs/screenshots/admin-pipeline-light.webp) | ![Element pipeline sheet with preview and chart, dark theme](./imgs/screenshots/admin-pipeline-dark.webp) |

## Element alert rules

In **Admin → Elements**, each element row provides a dedicated **Alerts** button. The Alert Rules sheet allows administrators to configure threshold conditions (`above`, `below`, `outside_range`, `equals`), severity levels (`info`, `warning`, `critical`), and anti-flapping hysteresis bands.

| Light | Dark |
|---|---|
| ![Element alert rules sheet with configured rules and condition form, light theme](./imgs/screenshots/admin-alerts-light.webp) | ![Element alert rules sheet, dark theme](./imgs/screenshots/admin-alerts-dark.webp) |

## Webhooks

Manage outbound notification destinations across Slack, Discord, Microsoft Teams, Telegram, and standard HMAC-SHA256 webhooks in **Admin → Webhooks**. Deliveries track status, HTTP latency, retries with backoff and jitter, and manual test pings.

| Webhook destinations | Endpoint configuration modal |
|---|---|
| ![Webhooks management page with active channels](./imgs/screenshots/admin-webhooks-light.webp) | ![New Webhook endpoint dialog](./imgs/screenshots/admin-webhook-dialog-light.webp) |

## Dashboards list and tablets

| Your dashboards | Tablet layout |
|---|---|
| ![Dashboards list with shared dashboards](./imgs/screenshots/dashboards-light.webp) | ![Dashboard on a tablet: widgets packed without gaps](./imgs/screenshots/dashboard-tablet-light.webp) |

## Devices

The devices behind the elements you can see, with live online status:

![Devices page](./imgs/screenshots/devices-light.webp)

## Administration

Users, groups, device keys, devices, elements, permissions, connections, live presence and an audit log replace the
old Django admin.

![Users administration](./imgs/screenshots/admin-users-light.webp)

![Permissions administration, dark theme](./imgs/screenshots/admin-permissions-dark.webp)

## MQTT

Devices that publish to an MQTT broker feed dashboards through [MQTT connections](./10_mqtt.md). The *Cold room* demo device speaks only MQTT: temperature and humidity in one JSON message, its battery in a binary frame decoded by JavaScript, and a compressor switch sent back as a downlink.

| Light | Dark |
|---|---|
| ![A dashboard fed only by MQTT, light theme](./imgs/screenshots/mqtt-dashboard-light.webp) | ![The same dashboard, dark theme](./imgs/screenshots/mqtt-dashboard-dark.webp) |

Connections show which gateway owns each slot; a connection's page lists its granted devices, uplink rules and downlinks:

![MQTT connections](./imgs/screenshots/mqtt-connections-light.webp)

![One MQTT connection](./imgs/screenshots/mqtt-connection-light.webp)

*Test & capture* runs a sample through a rule's decoder and field map, and shows the raw messages arriving:

![Testing a rule and capturing live messages](./imgs/screenshots/mqtt-test-capture-light.webp)

| Decoders | Rejected messages |
|---|---|
| ![Decoders](./imgs/screenshots/mqtt-decoders-light.webp) | ![Rejected messages](./imgs/screenshots/mqtt-rejected-light.webp) |

![Editing a decoder](./imgs/screenshots/mqtt-decoder-edit-light.webp)

## Friendly empty and error states

Empty and error states show a duck that fits the situation. Here is the lost duck on the "page not found" screen:

![Page not found, with the lost duck](./imgs/screenshots/not-found-light.webp)

## Node-RED integration

The [Node-RED nodes](./09_node_red.md) feeding a dashboard. The switch was flipped in the browser, applied in Node-RED, and confirmed back.

![Node-RED and the live dashboard side by side](./imgs/screenshots/nodered-with-dashboard.webp)

| Device settings with a connection test | Elements loaded from the server |
|---|---|
| ![quack-device configuration](./imgs/screenshots/nodered-device-config.webp) | ![quack in node with its element picker](./imgs/screenshots/nodered-in-node.webp) |
