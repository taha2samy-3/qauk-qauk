import { defineConfig, devices } from '@playwright/test'

const baseURL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:5173'

/**
 * E2E tests run against the live stack: the Vite dev server (proxying to the
 * Go backend on :8080, started separately) with the demo data + simulator.
 */
export default defineConfig({
  testDir: 'e2e',
  timeout: 90_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    // no GPU process: screenshots need none, and a wedged GPU driver stalls
    // requestAnimationFrame (every click then waits forever for "stable")
    launchOptions: { args: ['--disable-gpu', '--disable-software-rasterizer'] },
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } } }],
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : { command: 'pnpm dev', url: baseURL, reuseExistingServer: true, timeout: 60_000 },
})
