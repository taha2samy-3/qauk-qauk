import { createHmac } from 'node:crypto'
import { createServer, type IncomingHttpHeaders } from 'node:http'
import type { AddressInfo } from 'node:net'
import { expect, request, test, type APIRequestContext, type Page } from '@playwright/test'
import { ADMIN, login, useTheme } from './helpers'

/**
 * Alert rules and webhooks. The example Slack/Discord endpoints are disabled
 * (nothing is ever sent to a real third party); the delivery test uses a
 * receiver in this process, which needs the server's
 * QUACK_WEBHOOK_ALLOW_PRIVATE (set by `task dev`).
 */
const DIR = 'e2e/screenshots'
const ELEMENT = 'Soil moisture'
const EXAMPLES = [
  {
    name: 'Ops Slack Channel',
    url: 'https://hooks.slack.com/services/T00/B00/X00',
    format: 'slack',
    severities: ['warning', 'critical'],
    enabled: false,
  },
  {
    name: 'Discord Incident Room',
    url: 'https://discord.com/api/webhooks/123/abc',
    format: 'discord',
    severities: ['critical'],
    enabled: false,
  },
]
const RECEIVER = 'E2E receiver'
const RULES = [
  {
    name: 'High threshold warning',
    condition: 'above',
    threshold: 80,
    hysteresis: 2,
    severity: 'warning',
    message: 'Approaching the maximum operating limit',
    enabled: true,
  },
  {
    name: 'Out of range',
    condition: 'outside_range',
    threshold: 5,
    threshold_max: 95,
    hysteresis: 1,
    severity: 'critical',
    message: 'Check the sensor',
    enabled: false,
  },
]
const E2E_RULE = 'E2E rule'
/** Rules an earlier version of this spec seeded on every element. */
const LEGACY_RULES = ['High Threshold Warning', 'Critical Over-Limit Shutdown']

async function snap(page: Page, name: string, wait = 800) {
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${DIR}/${name}.png` })
}

async function adminApi(baseURL: string) {
  const api = await request.newContext({ baseURL })
  expect((await api.post('/api/v1/auth/login', { data: ADMIN })).ok()).toBeTruthy()
  return api
}

async function elementId(api: APIRequestContext): Promise<string> {
  const elements = (await (await api.get('/api/v1/admin/elements')).json()) as { id: string; name: string }[]
  const el = elements.find((e) => e.name === ELEMENT)
  expect(el, `element ${ELEMENT} (task demo)`).toBeDefined()
  return el!.id
}

/** Removes what this spec creates, by name. */
async function cleanup(baseURL: string) {
  const api = await adminApi(baseURL)
  try {
    const hooks = (await (await api.get('/api/v1/admin/webhooks')).json()) as { id: string; name: string }[]
    for (const h of hooks) {
      if (h.name === RECEIVER || EXAMPLES.some((e) => e.name === h.name)) await api.delete(`/api/v1/admin/webhooks/${h.id}`)
    }
    const id = await elementId(api)
    const elements = (await (await api.get('/api/v1/admin/elements')).json()) as { id: string }[]
    for (const el of elements) {
      const rules = (await (await api.get(`/api/v1/admin/elements/${el.id}/alerts`)).json()) as { id: string; name: string }[]
      for (const r of rules) {
        const ours = el.id === id && (r.name === E2E_RULE || RULES.some((x) => x.name === r.name))
        if (ours || LEGACY_RULES.includes(r.name)) await api.delete(`/api/v1/admin/elements/${el.id}/alerts/${r.id}`)
      }
    }
  } finally {
    await api.dispose()
  }
}

test.describe.configure({ mode: 'serial' })

test.beforeAll(async ({ baseURL }) => {
  await cleanup(baseURL!)
  const api = await adminApi(baseURL!)
  try {
    for (const e of EXAMPLES) expect((await api.post('/api/v1/admin/webhooks', { data: e })).status()).toBe(201)
    const id = await elementId(api)
    for (const r of RULES) expect((await api.post(`/api/v1/admin/elements/${id}/alerts`, { data: r })).ok()).toBeTruthy()
  } finally {
    await api.dispose()
  }
})
test.afterAll(async ({ baseURL }) => cleanup(baseURL!))

async function openAlerts(page: Page) {
  await page.goto('/admin/elements')
  await page.getByPlaceholder('Search elements…').fill(ELEMENT)
  const row = page.getByRole('row').filter({ has: page.getByText(ELEMENT, { exact: true }) })
  await row.getByRole('button', { name: /Alerts/i }).click()
  const sheet = page.getByRole('dialog')
  await expect(sheet.getByText(`Alert rules · ${ELEMENT}`)).toBeVisible()
  return sheet
}

for (const theme of ['light', 'dark'] as const) {
  test(`element alert rules sheet (${theme})`, async ({ page }) => {
    await useTheme(page, theme)
    await login(page, ADMIN)
    const sheet = await openAlerts(page)
    await expect(sheet.getByText('High threshold warning', { exact: true })).toBeVisible()
    await expect(sheet.getByText('resolves at ≤ 78')).toBeVisible()
    await expect(sheet.getByRole('switch', { name: 'Out of range enabled' })).not.toBeChecked()
    await expect(sheet.locator('#alert-name')).toBeVisible()
    await snap(page, `admin-alerts-${theme}`, 1000)
  })

  test(`webhooks admin page (${theme})`, async ({ page }) => {
    await useTheme(page, theme)
    await login(page, ADMIN)
    await page.goto('/admin/webhooks')
    await expect(page.getByRole('heading', { name: 'Webhooks' })).toBeVisible()
    // the URLs are shown redacted
    await expect(page.getByText('https://hooks.slack.com/…/X00')).toBeVisible()
    await snap(page, `admin-webhooks-${theme}`, 1000)

    await page.getByRole('button', { name: /New webhook/i }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: 'New webhook' })).toBeVisible()
    await expect(dialog.locator('#wh-name')).toBeVisible()
    await expect(dialog.locator('#wh-url')).toBeVisible()
    await snap(page, `admin-webhook-dialog-${theme}`, 1000)
  })
}

test('alert rules: add, edit, disable and delete', async ({ page, baseURL }) => {
  await login(page, ADMIN)
  const sheet = await openAlerts(page)
  await sheet.locator('#alert-name').fill(E2E_RULE)
  await sheet.locator('#alert-thresh').fill('50')
  await sheet.locator('#alert-hys').fill('1')
  await sheet.getByRole('button', { name: 'Add rule' }).click()
  await expect(sheet.getByText('value > 50')).toBeVisible()

  await sheet.getByRole('button', { name: `Edit ${E2E_RULE}` }).click()
  await expect(sheet.locator('#alert-thresh')).toHaveValue('50')
  await sheet.locator('#alert-thresh').fill('60')
  await sheet.getByRole('button', { name: 'Save rule' }).click()
  await expect(sheet.getByText('value > 60')).toBeVisible()

  await sheet.getByRole('switch', { name: `${E2E_RULE} enabled` }).click()
  await expect(sheet.getByRole('switch', { name: `${E2E_RULE} enabled` })).not.toBeChecked()

  const api = await adminApi(baseURL!)
  try {
    const id = await elementId(api)
    const rules = (await (await api.get(`/api/v1/admin/elements/${id}/alerts`)).json()) as {
      name: string
      threshold: number
      enabled: boolean
    }[]
    const saved = rules.filter((r) => r.name === E2E_RULE)
    expect(saved).toEqual([expect.objectContaining({ threshold: 60, enabled: false })]) // edited, not duplicated
  } finally {
    await api.dispose()
  }

  await sheet.getByRole('button', { name: `Delete ${E2E_RULE}` }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: /Delete|Confirm/ }).click()
  await expect(sheet.getByText('value > 60')).toHaveCount(0)
})

test('webhooks: a signed test delivery reaches the receiver', async ({ page }) => {
  const received: { headers: IncomingHttpHeaders; body: Buffer }[] = []
  const server = createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (c: Buffer) => chunks.push(c))
    req.on('end', () => {
      received.push({ headers: req.headers, body: Buffer.concat(chunks) })
      res.writeHead(200, { 'Content-Type': 'text/plain' }).end('ok')
    })
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const port = (server.address() as AddressInfo).port
  try {
    await login(page, ADMIN)
    await page.goto('/admin/webhooks')
    await page.getByRole('button', { name: /New webhook/i }).click()
    const dialog = page.getByRole('dialog')
    await dialog.locator('#wh-name').fill(RECEIVER)
    await dialog.locator('#wh-url').fill(`http://127.0.0.1:${port}/hook`)
    // only info: alert rules elsewhere don't reach it
    await dialog.getByRole('checkbox', { name: 'warning' }).uncheck()
    await dialog.getByRole('checkbox', { name: 'critical' }).uncheck()
    await dialog.getByRole('button', { name: 'Create webhook' }).click()

    // the generated secret is shown once
    const reveal = page.getByRole('dialog').filter({ hasText: `Signing secret for ${RECEIVER}` })
    const secret = (await reveal.locator('code').textContent())!.trim()
    expect(secret).toMatch(/^whsec_[A-Za-z0-9+/=]{40,}$/)
    await reveal.getByRole('button', { name: 'Done' }).click()

    const row = page.getByRole('row').filter({ hasText: RECEIVER })
    await expect(row.getByText(`http://127.0.0.1:${port}/…hook`)).toBeVisible()
    await row.getByRole('button', { name: `Send test notification to ${RECEIVER}` }).click()
    await expect(page.getByText(`Test delivered to ${RECEIVER}`)).toBeVisible()
    await expect.poll(() => received.length).toBe(1)

    // Standard Webhooks: v1,base64(HMAC-SHA256(key, id.timestamp.body))
    const { headers, body } = received[0]!
    const id = String(headers['webhook-id'])
    const ts = String(headers['webhook-timestamp'])
    const key = Buffer.from(secret.slice('whsec_'.length), 'base64')
    const want = 'v1,' + createHmac('sha256', key).update(`${id}.${ts}.`).update(body).digest('base64')
    expect(String(headers['webhook-signature']).split(' ')).toContain(want)
    expect(Math.abs(Date.now() / 1000 - Number(ts))).toBeLessThan(300)
    expect(JSON.parse(body.toString())).toMatchObject({ specversion: '1.0', id })

    // editing never shows the URL or the secret again
    await row.getByRole('button', { name: `Edit ${RECEIVER}` }).click()
    const edit = page.getByRole('dialog')
    await expect(edit.locator('#wh-url')).toHaveValue('')
    await expect(edit.locator('#wh-secret')).toHaveValue('')
    await page.keyboard.press('Escape')
  } finally {
    server.close()
  }
})
