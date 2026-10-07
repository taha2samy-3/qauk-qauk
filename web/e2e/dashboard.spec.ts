import { expect, test, type Page } from '@playwright/test'
import { ADMIN, VIEWER, deleteDashboardsNamed, login, uniqueName } from './helpers'

/**
 * The main user journey against the live backend + simulator:
 * create a dashboard, add widgets, persist, watch live data, control a
 * device, check read-only access, delete.
 */
test.describe.configure({ mode: 'serial' })

const NAME = uniqueName('E2E dashboard')
let dashboardUrl = ''

// leave nothing behind, even when a test fails half-way
test.afterAll(async ({ baseURL }) => deleteDashboardsNamed(baseURL!, [NAME]))

async function addWidget(page: Page, element: RegExp, search: string, type: string) {
  await page.getByTestId('add-widget').click()
  const dialog = page.getByRole('dialog', { name: 'Add widget' })
  await dialog.getByRole('combobox', { name: 'Search elements' }).fill(search)
  await dialog.getByRole('option', { name: element }).click()
  await page.getByRole('dialog', { name: 'Choose a widget' }).getByTestId(`widget-type-${type}`).click()
}

const widget = (page: Page, type: string) => page.locator(`[data-testid="widget"][data-widget-type="${type}"]`)

test('admin builds a dashboard and the layout persists', async ({ page }) => {
  await login(page, ADMIN)

  await page.getByTestId('new-dashboard').click()
  await page.getByLabel('Name').fill(NAME)
  await page.getByTestId('dashboard-name-submit').click()
  await expect(page.getByTestId('dashboard-title')).toHaveText(NAME)
  dashboardUrl = page.url()

  // empty state → editor
  await page.getByTestId('empty-add-widget').click()
  const dialog = page.getByRole('dialog', { name: 'Add widget' })
  await dialog.getByRole('combobox', { name: 'Search elements' }).fill('temp')
  await dialog.getByRole('option', { name: /^Temperature/ }).click()
  // the style hint ("sensor") makes the gauge the suggested widget
  const gaugeChoice = page.getByTestId('widget-type-gauge')
  await expect(gaugeChoice).toContainText('Suggested')
  await gaugeChoice.click()

  await addWidget(page, /^Irrigation pump/, 'irrigation', 'switch')

  await expect(widget(page, 'gauge')).toHaveCount(1)
  await expect(widget(page, 'switch')).toHaveCount(1)
  await expect(page.getByText('Unsaved changes')).toBeVisible()

  // undo/redo
  await page.getByRole('button', { name: 'Undo' }).click()
  await expect(widget(page, 'switch')).toHaveCount(0)
  await page.getByRole('button', { name: 'Redo' }).click()
  await expect(widget(page, 'switch')).toHaveCount(1)

  // drag a stat widget from the palette onto the grid, then bind it
  const grid = page.locator('.dashboard-grid')
  const box = (await grid.boundingBox())!
  await page.getByTestId('palette-stat').dragTo(grid, { targetPosition: { x: box.width - 120, y: 60 } })
  const sheet = page.getByRole('dialog', { name: /Configure stat/ })
  await expect(sheet).toBeVisible()
  await page.getByRole('combobox', { name: 'Search elements' }).fill('humid')
  await page.getByRole('option', { name: /^Humidity/ }).click()
  await sheet.getByLabel('Title').fill('Air humidity')
  await sheet.getByTestId('apply-widget-config').click()
  await expect(widget(page, 'stat')).toContainText('Air humidity')

  await page.getByTestId('save-dashboard').click()
  await expect(page.getByText('Dashboard saved')).toBeVisible()
  await expect(page.getByTestId('edit-dashboard')).toBeVisible()

  await page.reload()
  await expect(page.getByTestId('dashboard-title')).toHaveText(NAME)
  await expect(widget(page, 'gauge')).toHaveCount(1)
  await expect(widget(page, 'switch')).toHaveCount(1)
  await expect(widget(page, 'stat')).toContainText('Air humidity')
  await expect(widget(page, 'gauge')).toContainText('Temperature')
})

test('live values stream into the gauge', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto(dashboardUrl)
  const gauge = page.getByTestId('gauge')
  await expect(gauge).not.toHaveAttribute('data-value', '')
  const first = await gauge.getAttribute('data-value')
  await expect.poll(() => gauge.getAttribute('data-value'), { timeout: 15_000 }).not.toBe(first)
  await expect(widget(page, 'gauge').getByTestId('last-updated')).toBeVisible()
})

test('toggling the switch waits for the device echo', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto(dashboardUrl)
  const sw = page.getByTestId('switch-widget')
  await expect(sw).not.toHaveAttribute('data-state', 'unknown')
  const before = await sw.getAttribute('data-state')
  const after = before === 'on' ? 'off' : 'on'

  const toggle = sw.getByRole('switch')
  await expect(toggle).toBeEnabled()
  await toggle.click()
  // the device simulator echoes the new state back
  await expect(sw).toHaveAttribute('data-state', after)
  await expect(sw).toHaveAttribute('data-pending', 'false')
  await expect(toggle).toHaveAttribute('aria-checked', after === 'on' ? 'true' : 'false')

  // restore the demo state
  await toggle.click()
  await expect(sw).toHaveAttribute('data-state', before!)
  await expect(sw).toHaveAttribute('data-pending', 'false')
})

test('viewer sees a shared dashboard read-only', async ({ page, browser }) => {
  await login(page, ADMIN)
  await page.goto('/dashboards')
  const card = page.locator('[data-testid="dashboard-card"]', { hasText: NAME })
  await card.getByTestId('dashboard-actions').click()
  await page.getByRole('menuitemcheckbox', { name: 'Shared with everyone' }).click()
  await expect(page.getByText('Dashboard shared with everyone')).toBeVisible()

  const ctx = await browser.newContext()
  const viewer = await ctx.newPage()
  await login(viewer, VIEWER)
  await expect(viewer.locator('[data-testid="dashboard-card"]', { hasText: NAME })).toContainText('Owned by admin')
  await viewer.goto(dashboardUrl)
  await expect(viewer.getByTestId('readonly-badge')).toContainText('owned by admin')
  await expect(viewer.getByTestId('edit-dashboard')).toHaveCount(0)
  const sw = viewer.getByTestId('switch-widget')
  await expect(sw).not.toHaveAttribute('data-state', 'unknown')
  await expect(sw.getByRole('switch')).toBeDisabled()
  await expect(widget(viewer, 'switch').getByTestId('badge-readonly')).toBeVisible()
  // reading still works
  await expect(viewer.getByTestId('gauge')).not.toHaveAttribute('data-value', '')
  // admin pages are hidden and guarded
  await expect(viewer.getByRole('link', { name: 'Users' })).toHaveCount(0)
  await viewer.goto('/admin/users')
  await expect(viewer.getByText('Administrators only')).toBeVisible()
  await ctx.close()
})

test('admin deletes the dashboard', async ({ page }) => {
  await login(page, ADMIN)
  const card = page.locator('[data-testid="dashboard-card"]', { hasText: NAME })
  await card.getByTestId('dashboard-actions').click()
  await page.getByTestId('delete-dashboard').click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByText('Dashboard deleted')).toBeVisible()
  await expect(card).toHaveCount(0)
  await page.goto(dashboardUrl)
  await expect(page.getByText('Dashboard not found')).toBeVisible()
})
