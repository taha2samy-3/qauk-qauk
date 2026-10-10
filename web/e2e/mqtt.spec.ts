import { execFileSync } from 'node:child_process'
import { expect, test, type Page } from '@playwright/test'
import { ADMIN, deleteDashboardsNamed, login, useTheme } from './helpers'

/**
 * Screenshots for docs/10_mqtt.md, against `task start MQTT=1`: the demo
 * creates the "Demo broker" connection and the simulator plays the Cold room,
 * a device that speaks only MQTT (JSON telemetry, a binary frame decoded by
 * JavaScript, and compressor commands it echoes back).
 *
 * Rejected messages are injected with mosquitto_pub inside the broker
 * container (MOSQUITTO_CONTAINER, default quack-mosquitto-1).
 */
const NAME = 'Cold room (MQTT)'
const DIR = 'e2e/screenshots'
const MOSQUITTO = process.env.MOSQUITTO_CONTAINER ?? 'quack-mosquitto-1'
let dashboardId = ''
let connectionId = ''

test.describe.configure({ mode: 'serial' })
test.beforeAll(async ({ baseURL }) => deleteDashboardsNamed(baseURL!, [NAME]))

const widget = (page: Page, title: string) =>
  page.locator('[data-testid="widget"]').filter({ hasText: title })

type W = {
  type: string
  el: string
  title: string
  options?: Record<string, unknown>
  at: [number, number, number, number]
}
const WIDGETS: W[] = [
  {
    type: 'gauge',
    el: 'Cold room temperature',
    title: 'Temperature',
    at: [0, 0, 3, 6],
    options: { unit: '°C', min: -10, max: 15, baseColor: 'blue', thresholds: [{ value: 8, color: 'red' }] },
  },
  {
    type: 'gauge',
    el: 'Cold room humidity',
    title: 'Humidity',
    at: [3, 0, 3, 6],
    options: { unit: '%', min: 0, max: 100, baseColor: 'green' },
  },
  {
    type: 'stat',
    el: 'Battery',
    title: 'Battery · binary frame, JS decoder',
    at: [6, 0, 3, 6],
    options: { unit: 'V', decimals: 1, sparkline: true, color: 'orange' },
  },
  { type: 'switch', el: 'Compressor', title: 'Compressor · MQTT downlink', at: [9, 0, 3, 6] },
  {
    type: 'line',
    el: 'Cold room temperature',
    title: 'Temperature (quack/demo/+/up)',
    at: [0, 6, 6, 7],
    options: { unit: '°C', range: '15m', color: 'blue', area: true },
  },
  {
    type: 'line',
    el: 'Cold room humidity',
    title: 'Humidity (same message, second element)',
    at: [6, 6, 6, 7],
    options: { unit: '%', range: '15m', color: 'green' },
  },
]

async function snap(page: Page, name: string, wait = 1200) {
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${DIR}/${name}.png` })
}

function mqttPub(topic: string, ...payload: string[]) {
  execFileSync('docker', [
    'exec',
    MOSQUITTO,
    'mosquitto_pub',
    '-V',
    'mqttv5',
    '-q',
    '1',
    '-t',
    topic,
    ...payload,
  ])
}

test('create the Cold room dashboard', async ({ page }) => {
  await login(page, ADMIN)
  const elements = (await (await page.request.get('/api/v1/me/elements')).json()) as {
    id: string
    name: string
  }[]
  const id = (n: string) => {
    const e = elements.find((x) => x.name === n)
    if (!e) throw new Error(`element ${n} not visible to admin (run task demo MQTT=1)`)
    return e.id
  }
  const layout = {
    version: 1,
    widgets: WIDGETS.map((w, i) => ({
      id: `w_mqtt${i}`,
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
  const conns = (await (await page.request.get('/api/v1/admin/mqtt/connections')).json()) as {
    id: string
    name: string
  }[]
  connectionId = conns.find((c) => c.name === 'Demo broker')?.id ?? ''
  expect(connectionId, 'Demo broker connection (task demo MQTT=1)').not.toBe('')
})

test('a dashboard command reaches the MQTT device and comes back', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto(`/dashboards/${dashboardId}`)
  const sw = widget(page, 'Compressor').getByTestId('switch-widget')
  const toggle = widget(page, 'Compressor').getByRole('switch')
  const start = await sw.getAttribute('data-state')
  for (const want of start === 'on' ? ['off', 'on'] : ['on']) {
    await toggle.click()
    await expect(sw, `compressor -> ${want}`).toHaveAttribute('data-state', want, { timeout: 15_000 })
    await expect(sw).toHaveAttribute('data-pending', 'false')
  }
})

for (const theme of ['light', 'dark'] as const) {
  test(`Cold room dashboard (${theme})`, async ({ page }) => {
    await useTheme(page, theme)
    await login(page, ADMIN)
    await page.goto(`/dashboards/${dashboardId}`)
    await expect(page.getByTestId('gauge').first()).not.toHaveAttribute('data-value', '')
    await expect(widget(page, 'Battery').getByTestId('stat')).not.toHaveAttribute('data-value', '', {
      timeout: 20_000,
    })
    await snap(page, `mqtt-dashboard-${theme}`, 2500)
  })
}

test('MQTT admin: connections and the connection page', async ({ page }) => {
  await useTheme(page, 'light')
  await login(page, ADMIN)
  await page.goto('/admin/mqtt')
  await expect(page.getByRole('table', { name: 'MQTT connections' })).toBeVisible()
  await expect(page.getByText('1/1 connected')).toBeVisible()
  await snap(page, 'mqtt-connections-light')

  await page.goto(`/admin/mqtt/${connectionId}`)
  await expect(page.getByTestId('mqtt-slots')).toContainText('connected')
  await expect(page.getByRole('table', { name: 'Uplink rules' })).toContainText('quack/demo/+/bin')
  await snap(page, 'mqtt-connection-light')
})

test('MQTT admin: test a rule and capture live messages', async ({ page }) => {
  await useTheme(page, 'light')
  await login(page, ADMIN)
  await page.goto(`/admin/mqtt/${connectionId}`)
  const row = page.getByRole('table', { name: 'Uplink rules' }).getByRole('row').filter({ hasText: '/bin' })
  await row.getByRole('button', { name: 'Test & capture' }).click()
  const sheet = page.getByRole('dialog')
  await sheet.getByLabel('Topic').fill('quack/demo/cold-room-1/bin')
  await sheet.getByLabel('Payload').fill('hex:2701')
  await sheet.getByRole('button', { name: 'Run the pipeline' }).click()
  await expect(sheet.getByTestId('mqtt-test')).toContainText('Battery')
  await sheet.getByRole('button', { name: 'Capture for 5 min' }).click()
  const captured = sheet.getByTestId('captured').locator('li').first()
  await expect(captured).toContainText('quack/demo/cold-room-1/bin', { timeout: 20_000 })
  await expect(captured.locator('pre')).toHaveText(/^[0-9a-f]{2} [0-9a-f]{2}$/) // binary shown as hex
  await snap(page, 'mqtt-test-capture-light')
})

test('MQTT admin: decoders', async ({ page }) => {
  await useTheme(page, 'light')
  await login(page, ADMIN)
  await page.goto('/admin/mqtt/decoders')
  await expect(page.getByRole('table', { name: 'Decoders' })).toContainText('decodeUplink')
  await snap(page, 'mqtt-decoders-light')
  await page
    .getByRole('button', { name: /^Edit / })
    .first()
    .click()
  await expect(page.getByLabel('Source')).toHaveValue(/decodeUplink/)
  await snap(page, 'mqtt-decoder-edit-light', 600)
})

test('MQTT admin: rejected messages', async ({ page }) => {
  // an ungranted device, a frame too short for the decoder, and a payload that is not JSON
  mqttPub('quack/demo/intruder-7/up', '-m', '{"t": 99}')
  mqttPub('quack/demo/cold-room-1/bin', '-m', 'x')
  mqttPub('quack/demo/cold-room-1/up', '-m', 'not json')
  await useTheme(page, 'light')
  await login(page, ADMIN)
  await page.goto('/admin/mqtt/rejected')
  const table = page.getByRole('table', { name: 'Rejected messages' })
  await expect(table).toContainText('intruder-7', { timeout: 20_000 })
  await expect(table).toContainText('frame too short')
  await snap(page, 'mqtt-rejected-light')
})
