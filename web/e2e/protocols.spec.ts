import { readFileSync } from 'node:fs'
import { expect, test, type Page } from '@playwright/test'
import { ADMIN, deleteDashboardsNamed, login, useTheme } from './helpers'

/**
 * Screenshots for the device-transport docs, against `task start` (the
 * simulator in its default "mixed" mode: Greenhouse A over WebSocket, Boiler
 * room over REST, Weather station over gRPC) plus a Node-RED flow feeding the
 * "Packing line (Node-RED)" device (see docs/09_node_red.md).
 *
 * Optional: NODE_RED_URL (default http://127.0.0.1:1880) and TRANSCRIPTS, a
 * directory of *.txt terminal transcripts rendered as terminal screenshots.
 */
const NAME = 'Device transports'
const DIR = 'e2e/screenshots'
const NODE_RED = process.env.NODE_RED_URL ?? 'http://127.0.0.1:1880'
let dashboardId = ''

test.describe.configure({ mode: 'serial' })
test.beforeAll(async ({ baseURL }) => deleteDashboardsNamed(baseURL!, [NAME]))

const widget = (page: Page, title: string) => page.locator('[data-testid="widget"]').filter({ hasText: title })

type W = { type: string; el: string; title: string; options?: Record<string, unknown>; at: [number, number, number, number] }
const WIDGETS: W[] = [
  { type: 'gauge', el: 'Temperature', title: 'Greenhouse A · WebSocket', at: [0, 0, 3, 6], options: { unit: '°C', min: -10, max: 50 } },
  { type: 'gauge', el: 'Water temperature', title: 'Boiler room · REST', at: [3, 0, 3, 6], options: { unit: '°C', min: 0, max: 120, baseColor: 'green', thresholds: [{ value: 80, color: 'orange' }] } },
  { type: 'stat', el: 'Climate', title: 'Weather station · gRPC', at: [6, 0, 3, 6], options: { field: 'temperature', unit: '°C', sparkline: true, decimals: 1, color: 'purple' } },
  { type: 'stat', el: 'Line speed', title: 'Packing line · Node-RED', at: [9, 0, 3, 6], options: { unit: 'm/min', sparkline: true, decimals: 1, color: 'orange' } },
  { type: 'switch', el: 'Irrigation pump', title: 'Pump · WebSocket', at: [0, 6, 3, 4] },
  { type: 'switch', el: 'Burner', title: 'Burner · REST sync', at: [3, 6, 3, 4] },
  { type: 'switch', el: 'Gate relay', title: 'Gate · gRPC stream', at: [6, 6, 3, 4], options: { field: 'relay', onValue: 'ON', offValue: 'OFF', onLabel: 'Open', offLabel: 'Closed' } },
  { type: 'switch', el: 'Conveyor', title: 'Conveyor · Node-RED', at: [9, 6, 3, 4] },
  { type: 'line', el: 'Pressure', title: 'Boiler pressure (REST)', at: [0, 10, 6, 6], options: { unit: 'bar', range: '15m', color: 'green', area: true } },
  { type: 'line', el: 'Line speed', title: 'Line speed (Node-RED, limit 5/s keep latest)', at: [6, 10, 6, 6], options: { unit: 'm/min', range: '15m', color: 'orange' } },
]

async function snap(page: Page, name: string, wait = 1200) {
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${DIR}/${name}.png` })
}

test('create the transports dashboard', async ({ page }) => {
  await login(page, ADMIN)
  const elements = (await (await page.request.get('/api/v1/me/elements')).json()) as { id: string; name: string }[]
  const id = (n: string) => {
    const e = elements.find((x) => x.name === n)
    if (!e) throw new Error(`element ${n} not visible to admin`)
    return e.id
  }
  const layout = {
    version: 1,
    widgets: WIDGETS.map((w, i) => ({
      id: `w_proto${i}`,
      type: w.type,
      element_id: id(w.el),
      title: w.title,
      options: w.options ?? {},
      layouts: { lg: { x: w.at[0], y: w.at[1], w: w.at[2], h: w.at[3] } },
    })),
  }
  const res = await page.request.post('/api/v1/dashboards', { data: { name: NAME, shared: false, layout } })
  expect(res.status()).toBe(201)
  dashboardId = ((await res.json()) as { id: string }).id
})

test('commands reach every transport and come back', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto(`/dashboards/${dashboardId}`)
  // every switch is driven by a different transport; each device echoes the new state
  // (a round trip each way; the screenshots then show them all on)
  for (const title of ['Pump · WebSocket', 'Burner · REST sync', 'Gate · gRPC stream', 'Conveyor · Node-RED']) {
    const sw = widget(page, title).getByTestId('switch-widget')
    const toggle = widget(page, title).getByRole('switch')
    const start = await sw.getAttribute('data-state')
    const steps = start === 'on' ? ['off', 'on'] : start === 'off' ? ['on'] : ['on'] // unknown: never reported yet
    for (const want of steps) {
      await toggle.click()
      await expect(sw, `${title} -> ${want}`).toHaveAttribute('data-state', want, { timeout: 15_000 })
      await expect(sw).toHaveAttribute('data-pending', 'false')
    }
  }
})

for (const theme of ['light', 'dark'] as const) {
  test(`transports dashboard (${theme})`, async ({ page }) => {
    await useTheme(page, theme)
    await login(page, ADMIN)
    await page.goto(`/dashboards/${dashboardId}`)
    for (const t of ['Greenhouse A', 'Boiler room', 'Weather station', 'Packing line']) {
      await expect(widget(page, t).first()).toBeVisible()
    }
    await expect(page.getByTestId('gauge').first()).not.toHaveAttribute('data-value', '')
    await expect(widget(page, 'Weather station').getByTestId('stat')).not.toHaveAttribute('data-value', '')
    await expect(widget(page, 'Packing line').getByTestId('stat')).not.toHaveAttribute('data-value', '')
    await snap(page, `transports-dashboard-${theme}`, 2500)

    await page.goto('/devices')
    await expect(page.getByTestId('device-status').first()).toHaveAttribute('data-status', 'online')
    await snap(page, `transports-devices-${theme}`)
  })
}

test('element rate limits in the admin', async ({ page }) => {
  await useTheme(page, 'light')
  await login(page, ADMIN)
  await page.goto('/admin/elements')
  await expect(page.getByRole('table', { name: 'Elements' })).toBeVisible()
  await expect(page.getByText('5/s').first()).toBeVisible()
  await snap(page, 'admin-element-limits-light')
  await page.getByRole('button', { name: 'Edit Line speed' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(page.getByLabel('Messages / second')).toHaveValue('5')
  await snap(page, 'admin-element-limit-dialog-light', 600)
})

test('Node-RED flow feeding the platform', async ({ page, browser }) => {
  const up = await page.request.get(`${NODE_RED}/flows`).then((r) => r.ok()).catch(() => false)
  test.skip(!up, `Node-RED not running on ${NODE_RED}`)
  await page.goto(NODE_RED)
  await expect(page.locator('#red-ui-workspace')).toBeVisible()
  const nag = page.getByRole('button', { name: 'No, do not enable notifications' })
  if (await nag.isVisible({ timeout: 3000 }).catch(() => false)) await nag.click()
  await expect(page.locator('.red-ui-flow-node-status-label').filter({ hasText: /^connected$/ }).first()).toBeVisible({
    timeout: 20_000,
  })
  // a dashboard command, so the debug sidebar shows it arriving in Node-RED
  const dash = await browser.newPage()
  await login(dash, ADMIN)
  await dash.goto(`/dashboards/${dashboardId}`)
  const sw = widget(dash, 'Conveyor · Node-RED').getByTestId('switch-widget')
  for (const want of ['off', 'on']) {
    await widget(dash, 'Conveyor · Node-RED').getByRole('switch').click()
    await expect(sw).toHaveAttribute('data-state', want, { timeout: 15_000 })
  }
  await dash.close()
  await expect(page.locator('.red-ui-debug-msg').first()).toBeVisible({ timeout: 10_000 })
  await snap(page, 'transports-nodered-flow', 800)
})

test('terminal transcripts (REST, gRPC)', async ({ page }) => {
  const dir = process.env.TRANSCRIPTS
  test.skip(!dir, 'set TRANSCRIPTS to a directory of *.txt transcripts')
  for (const name of ['rest-curl', 'grpc-buf-curl']) {
    const text = readFileSync(`${dir}/${name}.txt`, 'utf8')
    const esc = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    const body = text
      .split('\n')
      .map((l) =>
        l.startsWith('$ ')
          ? `<span class="p">$</span> <span class="c">${esc(l.slice(2))}</span>`
          : l.startsWith('# ')
            ? `<span class="m">${esc(l)}</span>`
            : esc(l),
      )
      .join('\n')
    await page.setViewportSize({ width: 1200, height: 900 })
    await page.setContent(`<!doctype html><html><head><style>
      body{margin:0;background:#0d1117;font:13.5px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;color:#c9d1d9}
      .bar{background:#161b22;padding:9px 14px;color:#8b949e;font:12px system-ui;border-bottom:1px solid #30363d}
      .dot{display:inline-block;width:11px;height:11px;border-radius:50%;margin-right:6px}
      pre{margin:0;padding:16px 18px;white-space:pre-wrap;word-break:break-all}
      .p{color:#3fb950}.c{color:#e6edf3;font-weight:600}.m{color:#8b949e}
      </style></head><body><div class="bar"><span class="dot" style="background:#ff5f56"></span><span class="dot" style="background:#ffbd2e"></span><span class="dot" style="background:#27c93f"></span>&nbsp;${name}</div><pre>${body}</pre></body></html>`)
    const h = await page.evaluate(() => document.body.scrollHeight)
    await page.setViewportSize({ width: 1200, height: Math.min(h, 2000) })
    await page.screenshot({ path: `${DIR}/transcript-${name}.png` })
  }
})
