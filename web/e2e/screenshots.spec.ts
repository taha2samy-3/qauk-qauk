import { expect, test, type Page } from '@playwright/test'
import { ADMIN, deleteDashboardsNamed, login, uniqueName, useTheme } from './helpers'

/**
 * Visual snapshots for review (not pixel assertions): login, dashboard view,
 * editor, devices and admin users, in light and dark. Saved to e2e/screenshots/.
 */
const NAME = uniqueName('Plant overview')
const DIR = 'e2e/screenshots'
let dashboardId = ''

test.describe.configure({ mode: 'serial' })
test.afterAll(async ({ baseURL }) => deleteDashboardsNamed(baseURL!, [NAME]))

type W = { type: string; el: string; title?: string; options?: Record<string, unknown>; at: [number, number, number, number] }
const WIDGETS: W[] = [
  { type: 'gauge', el: 'Temperature', at: [0, 0, 3, 6], options: { unit: '°C', min: -10, max: 50, thresholds: [{ value: 28, color: 'yellow' }, { value: 35, color: 'red' }] } },
  { type: 'gauge', el: 'Water temperature', at: [3, 0, 3, 6], options: { unit: '°C', min: 0, max: 120, baseColor: 'green', thresholds: [{ value: 80, color: 'orange' }, { value: 100, color: 'red' }] } },
  { type: 'line', el: 'Soil moisture', at: [6, 0, 6, 6], options: { unit: '%', range: '15m', color: 'green', area: true } },
  { type: 'stat', el: 'Humidity', at: [0, 6, 3, 4], options: { unit: '%', sparkline: true, color: 'blue', decimals: 1 } },
  { type: 'switch', el: 'Irrigation pump', at: [3, 6, 2, 4] },
  { type: 'switch', el: 'Burner', at: [5, 6, 2, 4] },
  { type: 'slider', el: 'Fan speed', at: [7, 6, 3, 4], options: { min: 0, max: 100, step: 5, unit: '%' } },
  { type: 'status', el: 'Pressure', title: 'Boiler room', at: [10, 6, 2, 4] },
  { type: 'line', el: 'Pressure', at: [0, 10, 12, 6], options: { unit: 'bar', range: '15m', color: 'purple', thresholds: [{ value: 2.5, color: 'red' }] } },
]

async function snap(page: Page, name: string) {
  await page.waitForTimeout(1200) // let charts animate in
  await page.screenshot({ path: `${DIR}/${name}.png` })
}

test('create the showcase dashboard', async ({ page }) => {
  await login(page, ADMIN)
  const elements = (await (await page.request.get('/api/v1/me/elements')).json()) as { id: string; name: string }[]
  const id = (n: string) => elements.find((e) => e.name === n)!.id
  const layout = {
    version: 1,
    widgets: WIDGETS.map((w, i) => ({
      id: `w_shot${i}`,
      type: w.type,
      element_id: id(w.el),
      ...(w.title ? { title: w.title } : {}),
      options: w.options ?? {},
      layouts: { lg: { x: w.at[0], y: w.at[1], w: w.at[2], h: w.at[3] } },
    })),
  }
  const res = await page.request.post('/api/v1/dashboards', { data: { name: NAME, shared: false, layout } })
  expect(res.status()).toBe(201)
  dashboardId = ((await res.json()) as { id: string }).id
})

for (const theme of ['light', 'dark'] as const) {
  test(`screenshots (${theme})`, async ({ page, browser }) => {
    // login page (signed out)
    const anon = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    const anonPage = await anon.newPage()
    await useTheme(anonPage, theme)
    await anonPage.goto('/login')
    await expect(anonPage.getByRole('button', { name: 'Sign in' })).toBeVisible()
    await snap(anonPage, `login-${theme}`)
    await anon.close()

    await useTheme(page, theme)
    await login(page, ADMIN)
    await snap(page, `dashboards-${theme}`)

    await page.goto(`/dashboards/${dashboardId}`)
    await expect(page.getByTestId('gauge').first()).not.toHaveAttribute('data-value', '')
    await expect(page.getByTestId('line-chart').first()).not.toHaveAttribute('data-points', '0')
    await snap(page, `dashboard-view-${theme}`)

    await page.getByTestId('edit-dashboard').click()
    await page.locator('[data-widget-type="gauge"]').first().getByTestId('widget-configure').click()
    await expect(page.getByRole('dialog', { name: /Configure gauge/ })).toBeVisible()
    await snap(page, `dashboard-config-${theme}`)
    await page.keyboard.press('Escape')
    await snap(page, `dashboard-editor-${theme}`)
    await page.getByTestId('discard').click()

    await page.goto('/devices')
    await expect(page.getByTestId('device-status').first()).toHaveAttribute('data-status', 'online')
    await snap(page, `devices-${theme}`)

    await page.goto('/admin/users')
    await expect(page.getByRole('table', { name: 'Users' })).toBeVisible()
    await snap(page, `admin-users-${theme}`)

    await page.goto('/admin/permissions')
    await expect(page.getByRole('table', { name: 'Permissions' })).toBeVisible()
    await snap(page, `admin-permissions-${theme}`)
  })
}

test('tablet layout', async ({ page }) => {
  await page.setViewportSize({ width: 820, height: 1180 })
  await useTheme(page, 'light')
  await login(page, ADMIN)
  await page.goto(`/dashboards/${dashboardId}`)
  await expect(page.getByTestId('gauge').first()).not.toHaveAttribute('data-value', '')
  await snap(page, 'dashboard-tablet-light')
})
