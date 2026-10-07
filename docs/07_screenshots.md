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

## Friendly empty and error states

Empty and error states show a duck that fits the situation. Here is the lost duck on the "page not found" screen:

![Page not found, with the lost duck](./imgs/screenshots/not-found-light.webp)
