import { expect, test, type APIRequestContext, type Page } from '@playwright/test'
import { ADMIN, deleteDashboardsNamed, login, uniqueName } from './helpers'

/**
 * Data bindings against the demo "Weather station": its messages are not
 * {"value": N} but {temperature, humidity, battery, ts, gps{lat,lng}}, and its
 * relay reports/accepts "ON"/"OFF". Widgets bind to attributes, charts plot
 * several series with an X attribute, and value mappings rename states.
 */
test.describe.configure({ mode: 'serial' })

const NAME = uniqueName('Bindings')
let dashboardId = ''

test.afterAll(async ({ baseURL }) => deleteDashboardsNamed(baseURL!, [NAME]))

async function adminApi(page: Page): Promise<APIRequestContext> {
  const res = await page.request.post('/api/v1/auth/login', { data: ADMIN })
  expect(res.ok()).toBeTruthy()
  return page.request
}

const widget = (page: Page, title: string) => page.locator('[data-testid="widget"]').filter({ hasText: title })

test('a dashboard bound to attributes renders live values, multi-series and mappings', async ({ page }) => {
  const api = await adminApi(page)
  const elements = (await (await api.get('/api/v1/me/elements')).json()) as { id: string; name: string }[]
  const climate = elements.find((e) => e.name === 'Climate')?.id
  const relay = elements.find((e) => e.name === 'Gate relay')?.id
  test.skip(!climate || !relay, 'demo Weather station not seeded (run `task demo`)')

  const at = (x: number, y: number, w: number, h: number) => ({ lg: { x, y, w, h } })
  const layout = {
    version: 2,
    widgets: [
      { id: 'hum', type: 'stat', element_id: climate, title: 'Humidity', options: { field: 'humidity', unit: '%', sparkline: true }, layouts: at(0, 0, 3, 5) },
      { id: 'bat', type: 'stat', element_id: climate, title: 'Battery', options: { field: 'battery', unit: 'V' }, layouts: at(3, 0, 3, 5) },
      {
        id: 'chart',
        type: 'line',
        element_id: climate,
        title: 'Climate',
        options: {
          field: 'temperature',
          xField: 'ts',
          label: 'Temperature',
          range: '15m',
          series: [{ element_id: climate, field: 'humidity', label: 'Humidity', color: 'green' }],
        },
        layouts: at(6, 0, 6, 7),
      },
      {
        id: 'sw',
        type: 'switch',
        element_id: relay,
        title: 'Gate',
        options: { field: 'relay', onValue: 'ON', offValue: 'OFF', onLabel: 'Open', offLabel: 'Closed' },
        layouts: at(0, 5, 3, 4),
      },
      {
        id: 'st',
        type: 'status',
        element_id: relay,
        title: 'Gate state',
        options: {
          source: 'attribute',
          field: 'relay',
          mappings: [
            { match: 'ON', label: 'Gate open', color: 'green' },
            { match: 'OFF', label: 'Gate closed', color: 'red' },
          ],
        },
        layouts: at(3, 5, 3, 4),
      },
    ],
  }
  const created = await api.post('/api/v1/dashboards', { data: { name: NAME, layout } })
  expect(created.status()).toBe(201)
  dashboardId = ((await created.json()) as { id: string }).id

  // the API login above already put the session cookie in this browser context
  await page.goto(`/dashboards/${dashboardId}`)
  await expect(page.getByTestId('connection-status')).toHaveAttribute('data-status', 'open')

  // single values read from named attributes
  const hum = widget(page, 'Humidity').getByTestId('stat')
  await expect(hum).not.toHaveAttribute('data-value', '')
  expect(Number(await hum.getAttribute('data-value'))).toBeGreaterThan(20)
  const bat = widget(page, 'Battery').getByTestId('stat')
  await expect(bat).not.toHaveAttribute('data-value', '')
  expect(Number(await bat.getAttribute('data-value'))).toBeLessThan(5)

  // one chart, two series (temperature + humidity), X from the message's ts
  const chart = widget(page, 'Climate').getByTestId('line-chart')
  await expect(chart).toHaveAttribute('data-series', '2')
  await expect.poll(async () => Number(await chart.getAttribute('data-points'))).toBeGreaterThan(4)

  // value mapping: the relay's "OFF"/"ON" shown under other names
  const status = widget(page, 'Gate state').getByTestId('status-label')
  const sw = widget(page, 'Gate').getByTestId('switch-widget')
  await expect(sw).not.toHaveAttribute('data-state', 'unknown')
  const startOn = (await sw.getAttribute('data-state')) === 'on'
  await expect(status).toHaveText(startOn ? 'Gate open' : 'Gate closed')

  // commands go out in the device's own format ({"relay": "ON"}) and the echo flips both widgets
  await widget(page, 'Gate').getByRole('switch').click()
  await expect(sw).toHaveAttribute('data-state', startOn ? 'off' : 'on', { timeout: 10_000 })
  await expect(status).toHaveText(startOn ? 'Gate closed' : 'Gate open')
  await widget(page, 'Gate').getByRole('switch').click()
  await expect(sw).toHaveAttribute('data-state', startOn ? 'on' : 'off', { timeout: 10_000 })
  await expect(status).toHaveText(startOn ? 'Gate open' : 'Gate closed')

  await page.screenshot({ path: 'e2e/screenshots/bindings-light.png' })
})

test('the settings sheet discovers attributes and rebinds a widget', async ({ page }) => {
  test.skip(!dashboardId, 'needs the dashboard from the previous test')
  await login(page, ADMIN)
  await page.goto(`/dashboards/${dashboardId}`)
  await page.getByTestId('edit-dashboard').click()
  await widget(page, 'Battery').getByRole('button', { name: 'Configure Battery' }).click()
  const sheet = page.getByRole('dialog', { name: /Configure stat/ })
  await expect(sheet.getByTestId('section-data')).toBeVisible()

  // discovered from live messages, with sample values; nested and time attributes included
  await expect(sheet.getByTestId('attr-chip-temperature')).toBeVisible()
  await expect(sheet.getByTestId('attr-chip-gps.lat')).toBeVisible()
  await sheet.getByTestId('attr-chip-temperature').click()
  await sheet.getByLabel('Unit').fill('°C')
  await sheet.getByTestId('apply-widget-config').click()

  const stat = widget(page, 'Battery').getByTestId('stat')
  await expect.poll(async () => Number(await stat.getAttribute('data-value'))).toBeGreaterThan(10)

  // invalid paths are rejected before saving
  await widget(page, 'Battery').getByRole('button', { name: 'Configure Battery' }).click()
  await sheet.locator('#opt-field').fill('bad path!')
  await sheet.getByTestId('apply-widget-config').click()
  await expect(sheet.getByText(/Use a path like/)).toBeVisible()
  await page.keyboard.press('Escape')
})
