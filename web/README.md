# Quack Quack web app

This is the dashboard and admin UI for Quack Quack. It is a React 19 + TypeScript app built with Vite. The Go server
(`quack serve`) serves the production build from `web/dist`.

## Run it

```sh
task infra:up && task demo   # once: database, bus, demo data
task dev                     # Go API + realtime gateway on :8080
task simulate                # demo devices streaming values
task web:dev                 # this app on http://127.0.0.1:5173
```

Log in as `admin` / `admin12345`, or as `viewer` / `viewer12345` (read-only).

Vite proxies `/api`, `/browser` and `/device` (WebSockets included) to `QUACK_BACKEND` (default
`http://127.0.0.1:8080`). The browser therefore sees a single origin, so the session cookie, the CSRF checks and the
WebSocket Origin checks all work without CORS.

| Task | What it does |
|---|---|
| `task web:dev` | Dev server with HMR |
| `task web:build` | Type check + production build into `dist/` |
| `task web:lint` | ESLint + `tsc --noEmit` |
| `task web:test` | Vitest unit tests (realtime client, editor state, layout packing) |
| `task web:e2e` | Playwright against the live stack. It needs `task dev`, `task demo` and `task simulate` running; screenshots are saved in `e2e/screenshots/` |
| `task web:gen-api` | Regenerate `src/api/schema.d.ts` from the running backend's OpenAPI spec |

## Architecture

```
src/
  api/          typed REST client (openapi-fetch + generated schema), Problem Details errors, TanStack Query hooks
  realtime/     ONE WebSocket per tab: ref-counted subscriptions, reconnect with backoff, useElement() hook
  auth/         login page, route guards (session, admin)
  features/
    dashboards/ list, view/editor (react-grid-layout), widget palette, config sheet, undo/redo, layout schema
    devices/    devices the user can see, with live online status
    admin/      users, groups, keys, devices, elements, permissions, connections, presence, audit log
  widgets/      widget registry + one folder per widget type
  components/   app shell, shadcn-style UI primitives (Radix), empty/error states with duck illustrations
```

**Server state:**
- REST data lives in TanStack Query.
- Live data comes from `useElement(elementId)`. It returns `{ value, history, permission, deviceConnected,
  lastEditBy, lastEditAt, status, send }`.
- Several widgets bound to the same element share a single subscription.

**Realtime client** (`src/realtime/client.ts`): it speaks the legacy-compatible protocol described in
`docs/04_api_reference/browser_api.md`.
- On reconnect it re-subscribes to every element with a live widget.
- A forced `unsubscribe` (permission revoked, element deleted) puts the widget into a "revoked" state.
- Commands from switches and sliders stay "pending" until the device echoes the new state back.

## Dashboard layout document

The backend stores `layout` verbatim (`PUT /api/v1/dashboards/{id}`). This app owns its schema
(`src/features/dashboards/layout.ts`):

```jsonc
{
  "version": 2,
  "widgets": [
    {
      "id": "w_8f2k",
      "type": "gauge",                 // a key of the widget registry
      "element_id": "0192…",           // or null (unbound)
      "title": "Boiler",               // optional override
      "options": { "unit": "°C", "min": 0, "max": 120 },
      "layouts": {
        "lg": { "x": 0, "y": 0, "w": 3, "h": 7 },   // 12 columns, always present
        "md": { "x": 0, "y": 0, "w": 4, "h": 7 }    // 8 columns: only if the user arranged it at that size
      }
    }
  ]
}
```

`md` (8 columns) and `sm` (4 columns) are **automatic** unless the user drags widgets at that size.
`positionsFor()` packs the widgets in reading order into the first free slot, so tablets and phones never get holes.

**Migrations:** bump `LAYOUT_VERSION` and add a step in `migrateLayout()`. v1 → v2 dropped the old mechanically
scaled md/sm positions. Widgets that fail validation are dropped and counted, and the editor reports them.

## Adding a widget type

1. Create `src/widgets/<name>/<Name>Widget.tsx` exporting a `WidgetDefinition` (`src/widgets/types.ts`). It provides:
   - `type`, `label`, `description`, `icon`
   - `defaultSize` / `minSize` in lg grid units
   - `fit(element)`: `'suggested' | 'ok' | 'no'`. This decides where the type shows up in the "Choose a widget" step.
   - `defaultOptions(element)` and `fields` (text, number, boolean, select, thresholds, color), which render in the
     config sheet automatically
   - `Component` (gets `{ widget, element, options, rt, editing, local, setLocal }`), plus optional header `Actions`
   - `control: true` if the widget sends commands; it is then disabled for read-only users
2. Add it to `WIDGETS` in `src/widgets/registry.ts`.
3. If an element style hint should default to it, map the hint in `defaultWidgetType()`.

## Brand assets

`public/favicon.svg`, `public/ducks/*.svg` (empty/error-state illustrations) and the touch icons are copies of the
files in `/branding`, which is the source of truth (see `branding/README.md`). Use them via `<EmptyState duck="…">` /
`<Duck pose="…">` from `src/components/states.tsx`.
