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

## MQTT Administration

Manage external MQTT 5 client connections, uplink parsing rules, downlinks, and test Javascript decoders in a live sandbox. (Screenshots pending `e2e/mqtt.spec.ts` generation).

## Friendly empty and error states

Empty and error states show a duck that fits the situation. Here is the lost duck on the "page not found" screen:

![Page not found, with the lost duck](./imgs/screenshots/not-found-light.webp)

## Node-RED integration

The [Node-RED nodes](./09_node_red.md) feeding a dashboard. The switch was flipped in the browser, applied in Node-RED, and confirmed back.

![Node-RED and the live dashboard side by side](./imgs/screenshots/nodered-with-dashboard.webp)

| Device settings with a connection test | Elements loaded from the server |
|---|---|
| ![quack-device configuration](./imgs/screenshots/nodered-device-config.webp) | ![quack in node with its element picker](./imgs/screenshots/nodered-in-node.webp) |
