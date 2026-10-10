import { expect, test, type Page } from '@playwright/test'
import { ADMIN, login, useTheme } from './helpers'

const DIR = 'e2e/screenshots'

async function snap(page: Page, name: string, wait = 1200) {
  await page.waitForTimeout(wait)
  await page.screenshot({ path: `${DIR}/${name}.png` })
}

test.describe.configure({ mode: 'serial' })

for (const theme of ['light', 'dark'] as const) {
  test(`pipeline sheet and preview (${theme})`, async ({ page }) => {
    await useTheme(page, theme)
    await login(page, ADMIN)
    await page.goto('/admin/elements')
    await expect(page.getByRole('table', { name: 'Elements' })).toBeVisible()

    // Filter to Soil moisture
    await page.getByPlaceholder('Search elements…').fill('Soil moisture')
    const row = page.getByRole('row').filter({ hasText: 'Soil moisture' })
    await expect(row).toBeVisible()
    await expect(row.getByText(/steps · v/)).toBeVisible()
    await row.getByRole('button', { name: 'Pipeline' }).click()

    // The PipelineSheet is now open
    await expect(page.getByRole('dialog')).toBeVisible()
    await expect(page.getByText('Pipeline · Soil moisture')).toBeVisible()
    await expect(page.getByText('scale')).toBeVisible()

    // Switch to Preview tab
    await page.getByRole('tab', { name: /Preview/ }).click()
    const runBtn = page.getByRole('button', { name: /Run preview/i })
    await expect(runBtn).toBeVisible()
    await runBtn.click()

    await snap(page, `admin-pipeline-${theme}`, 1800)
  })
}
