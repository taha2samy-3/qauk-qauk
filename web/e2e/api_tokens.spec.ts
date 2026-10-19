import { expect, request, test, type Page } from '@playwright/test'
import { ADMIN, login, useTheme } from './helpers'

/**
 * Machine users and API tokens: create both in the admin UI, use the token
 * from outside the browser session, revoke it. Screenshots for the docs.
 */
const DIR = 'e2e/screenshots'
const MACHINE = 'e2e-wallboard'
const ELEMENT = 'Soil moisture'

async function snap(page: Page, name: string, wait = 800) {
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${DIR}/${name}.png` })
}

/** Deletes the machine user this spec creates (and with it its tokens). */
async function cleanup(baseURL: string) {
  const api = await request.newContext({ baseURL })
  try {
    await api.post('/api/v1/auth/login', { data: ADMIN })
    const users = (await (await api.get('/api/v1/admin/users')).json()) as { id: number; username: string }[]
    for (const u of users) if (u.username === MACHINE) await api.delete(`/api/v1/admin/users/${u.id}`)
  } finally {
    await api.dispose()
  }
}

test.describe.configure({ mode: 'serial' })
test.beforeAll(async ({ baseURL }) => cleanup(baseURL!))
test.afterAll(async ({ baseURL }) => cleanup(baseURL!))

let token = ''

test('create a machine user and a token, then use it from outside', async ({ page, baseURL }) => {
  await useTheme(page, 'light')
  await login(page, ADMIN)
  await page.goto('/admin/api-tokens')
  await expect(page.getByRole('heading', { name: 'API tokens' })).toBeVisible()

  await page.getByRole('button', { name: 'New machine user' }).first().click()
  await page.locator('#mu-name').fill(MACHINE)
  await page.locator('#mu-email').fill('ops@example.com')
  await page.getByRole('dialog').getByRole('button', { name: 'Create' }).click()
  const card = page.getByRole('region', { name: `Machine user ${MACHINE}` })
  await expect(card).toBeVisible()

  // grant it the element (read-only), as an admin would in Permissions
  const users = (await (await page.request.get('/api/v1/admin/users')).json()) as {
    id: number
    username: string
  }[]
  const mu = users.find((u) => u.username === MACHINE)!
  const els = (await (await page.request.get('/api/v1/admin/elements')).json()) as {
    id: string
    name: string
  }[]
  const el = els.find((e) => e.name === ELEMENT)!
  expect(
    (
      await page.request.put('/api/v1/admin/permissions', {
        data: { element_id: el.id, user_id: mu.id, permission: 'R' },
      })
    ).ok(),
  ).toBeTruthy()

  await card.getByRole('button', { name: 'New token' }).click()
  await page.locator('#tok-name').fill('wallboard')
  await page.getByRole('checkbox', { name: /Alerts/ }).check()
  await snap(page, 'admin-api-token-dialog-light')
  await page.getByRole('dialog').getByRole('button', { name: 'Create token' }).click()

  token = ((await page.getByTestId('token').textContent()) ?? '').trim()
  expect(token).toMatch(/^qk_[A-Za-z0-9_-]{43}$/)
  await snap(page, 'admin-api-token-created-light')
  await page.getByRole('button', { name: 'Done' }).click()
  await expect(card.getByRole('table', { name: `Tokens of ${MACHINE}` })).toContainText('wallboard')

  // from outside the browser session: only the token
  const outside = await request.newContext({
    baseURL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  })
  try {
    const r = await outside.get('/api/v1/me/elements')
    expect(r.status()).toBe(200)
    expect(((await r.json()) as { name: string }[]).map((e) => e.name)).toEqual([ELEMENT])
    expect((await outside.get(`/api/v1/elements/${el.id}/history`)).status()).toBe(200)
    expect((await outside.get('/api/v1/admin/users')).status()).toBe(403) // never admin
  } finally {
    await outside.dispose()
  }
  await page.reload()
  // its last use shows (and its 90-day default expiry)
  await expect(card.getByRole('table', { name: `Tokens of ${MACHINE}` })).toContainText('just now')
  await expect(card.getByRole('table', { name: `Tokens of ${MACHINE}` })).toContainText(/in 3 months|in \d+ days/)
})

for (const theme of ['light', 'dark'] as const) {
  test(`API tokens page (${theme})`, async ({ page }) => {
    await useTheme(page, theme)
    await login(page, ADMIN)
    await page.goto('/admin/api-tokens')
    await expect(page.getByRole('region', { name: `Machine user ${MACHINE}` })).toBeVisible()
    await snap(page, `admin-api-tokens-${theme}`)
  })
}

test('a revoked token stops working at once', async ({ page, baseURL }) => {
  await login(page, ADMIN)
  await page.goto('/admin/api-tokens')
  const card = page.getByRole('region', { name: `Machine user ${MACHINE}` })
  await card.getByRole('button', { name: 'Revoke wallboard' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Revoke' }).click()
  await expect(card.getByText('No tokens')).toBeVisible()
  const outside = await request.newContext({
    baseURL,
    extraHTTPHeaders: { Authorization: `Bearer ${token}` },
  })
  try {
    expect((await outside.get('/api/v1/me/elements')).status()).toBe(401)
  } finally {
    await outside.dispose()
  }
})
