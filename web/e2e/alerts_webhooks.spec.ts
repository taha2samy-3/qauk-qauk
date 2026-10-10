import { expect, test, type Page } from '@playwright/test'
import { ADMIN, login, useTheme } from './helpers'

const DIR = 'e2e/screenshots'

async function snap(page: Page, name: string, wait = 800) {
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${DIR}/${name}.png` })
}

test.describe.configure({ mode: 'serial' })

test.beforeAll(async ({ playwright, baseURL }) => {
  const api = await playwright.request.newContext({ baseURL })
  try {
    const loginRes = await api.post('/api/v1/auth/login', { data: ADMIN })
    if (!loginRes.ok()) return

    // Seed sample webhooks if empty
    const whRes = await api.get('/api/v1/admin/webhooks')
    if (whRes.ok()) {
      const existingWh = (await whRes.json()) as { id: string; name: string }[] | null
      if (!existingWh || existingWh.length === 0) {
        await api.post('/api/v1/admin/webhooks', {
          data: {
            name: 'Ops Slack Channel',
            url: 'https://hooks.slack.com/services/T00/B00/X00',
            format: 'slack',
            secret: 'whsec_sample123',
            severities: ['warning', 'critical'],
            enabled: true,
          },
        })
        await api.post('/api/v1/admin/webhooks', {
          data: {
            name: 'Discord Incident Room',
            url: 'https://discord.com/api/webhooks/123/abc',
            format: 'discord',
            secret: 'whsec_sample456',
            severities: ['critical'],
            enabled: true,
          },
        })
      }
    }

    // Seed sample alert rules on elements
    const elemRes = await api.get('/api/v1/admin/elements')
    if (elemRes.ok()) {
      const elements = (await elemRes.json()) as { id: string; name: string }[] | null
      if (elements && elements.length > 0) {
        for (const el of elements) {
          const rulesRes = await api.get(`/api/v1/admin/elements/${el.id}/alerts`)
          if (rulesRes.ok()) {
            const existingRules = (await rulesRes.json()) as { id: string }[] | null
            if (!existingRules || existingRules.length === 0) {
              await api.post(`/api/v1/admin/elements/${el.id}/alerts`, {
                data: {
                  name: 'High Threshold Warning',
                  condition: 'above',
                  threshold: 80,
                  hysteresis: 2,
                  severity: 'warning',
                  message: 'Approaching maximum operating limit',
                  enabled: true,
                },
              })
              await api.post(`/api/v1/admin/elements/${el.id}/alerts`, {
                data: {
                  name: 'Critical Over-Limit Shutdown',
                  condition: 'above',
                  threshold: 95,
                  hysteresis: 1,
                  severity: 'critical',
                  message: 'Immediate safety shutoff required',
                  enabled: true,
                },
              })
            }
          }
        }
      }
    }
  } finally {
    await api.dispose()
  }
})

for (const theme of ['light', 'dark'] as const) {
  test(`element alert rules sheet (${theme})`, async ({ page }) => {
    await useTheme(page, theme)
    await login(page, ADMIN)
    await page.goto('/admin/elements')
    await expect(page.getByRole('table', { name: 'Elements' })).toBeVisible()

    // Find first element row and click Alerts button
    const firstRow = page.getByRole('row').nth(1)
    await expect(firstRow).toBeVisible()
    const alertsBtn = firstRow.getByRole('button', { name: /Alerts/i })
    await expect(alertsBtn).toBeVisible()
    await alertsBtn.click()

    // Alert rules sheet is now open
    await expect(page.getByRole('dialog')).toBeVisible()
    await expect(page.getByText(/Alert Rules ·/i)).toBeVisible()
    await expect(page.getByText('Add New Alert Condition')).toBeVisible()
    await expect(page.getByLabel(/Rule Name/i)).toBeVisible()
    await expect(page.getByLabel(/Threshold Value/i)).toBeVisible()
    await expect(page.getByLabel(/Hysteresis Band/i)).toBeVisible()

    // Capture screenshot of the open Alert Rules Sheet
    await snap(page, `admin-alerts-${theme}`, 1000)
  })

  test(`webhooks admin page (${theme})`, async ({ page }) => {
    await useTheme(page, theme)
    await login(page, ADMIN)
    await page.goto('/admin/webhooks')
    await expect(page.getByRole('heading', { name: 'Webhooks' })).toBeVisible()

    // Capture screenshot of the Webhooks page with table and endpoints
    await snap(page, `admin-webhooks-${theme}`, 1000)

    // Click New webhook
    const newBtn = page.getByRole('button', { name: /New webhook/i })
    await expect(newBtn).toBeVisible()
    await newBtn.click()

    // Webhook modal is open
    await expect(page.getByRole('dialog')).toBeVisible()
    await expect(page.getByText('New Webhook Endpoint')).toBeVisible()
    await expect(page.getByLabel('Name')).toBeVisible()
    await expect(page.getByLabel('Format')).toBeVisible()
    await expect(page.getByLabel('Endpoint URL')).toBeVisible()

    // Capture screenshot of the Webhook Configuration Dialog
    await snap(page, `admin-webhook-dialog-${theme}`, 1000)
  })
}
