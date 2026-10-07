import { expect, type Page } from '@playwright/test'

export const ADMIN = { username: 'admin', password: 'admin12345' }
export const VIEWER = { username: 'viewer', password: 'viewer12345' }

export async function login(page: Page, user: { username: string; password: string }) {
  await page.goto('/login')
  await page.getByLabel('Username').fill(user.username)
  await page.getByLabel('Password').fill(user.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page).toHaveURL(/\/dashboards$/)
  await expect(page.getByTestId('connection-status')).toHaveAttribute('data-status', 'open')
}

/** Theme is read from localStorage before first paint. */
export async function useTheme(page: Page, theme: 'light' | 'dark') {
  await page.addInitScript((t) => localStorage.setItem('quack-theme', t), theme)
}

export const uniqueName = (prefix: string) => `${prefix} ${new Date().toISOString().slice(11, 19)}-${Math.random().toString(36).slice(2, 6)}`

/** Delete dashboards by name through the API (test cleanup). */
export async function deleteDashboardsNamed(baseURL: string, names: string[]) {
  const { request } = await import('@playwright/test')
  const api = await request.newContext({ baseURL })
  try {
    const res = await api.post('/api/v1/auth/login', { data: ADMIN })
    if (!res.ok()) return
    const list = (await (await api.get('/api/v1/dashboards')).json()) as { id: string; name: string }[] | null
    for (const d of list ?? []) if (names.includes(d.name)) await api.delete(`/api/v1/dashboards/${d.id}`)
  } finally {
    await api.dispose()
  }
}
