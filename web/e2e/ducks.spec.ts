import { expect, test } from '@playwright/test'
import { ADMIN, VIEWER, login } from './helpers'

/** Brand illustrations show up in empty and error states. */
test('404 shows the lost duck', async ({ page }) => {
  await login(page, ADMIN)
  await page.goto('/this-page-does-not-exist')
  await expect(page.getByText('Page not found')).toBeVisible()
  await expect(page.getByTestId('duck-lost')).toBeVisible()
  await page.screenshot({ path: 'e2e/screenshots/not-found-light.png' })
})

test('viewer gets the guardian duck on admin pages', async ({ page }) => {
  await login(page, VIEWER)
  await page.goto('/admin/users')
  await expect(page.getByText('Administrators only')).toBeVisible()
  await expect(page.getByTestId('duck-guardian')).toBeVisible()
  await page.screenshot({ path: 'e2e/screenshots/admin-only-light.png' })
})
