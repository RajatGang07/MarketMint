import { defineConfig, devices } from '@playwright/test'

/**
 * E2E drives the real app: the Go server with the embedded UI (API and
 * dashboard on one origin, exactly what production runs). Build and start it
 * first — `backend/scripts/build.sh`, then `backend/bin/server` — or point
 * E2E_BASE_URL at any running instance.
 *
 * Tests ride the live NSE data feed on purpose: they exercise the same
 * provider chain the user does, so a dead feed fails the run instead of
 * hiding behind fixtures.
 */
export default defineConfig({
  testDir: './e2e',
  timeout: 240_000,
  expect: { timeout: 15_000 },
  // Two workers and a retry: the suite shares one live Yahoo feed, and a
  // burst of parallel sessions can trip its rate limiter into the chain's
  // two-minute cool-down. Gentler concurrency plus one retry rides it out.
  workers: 2,
  retries: 1,
  reporter: [['list']],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:8000',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  projects: [
    // channel: 'chrome' drives the installed Google Chrome, so the ~130MB
    // `npx playwright install` download is never needed.
    { name: 'chrome', use: { ...devices['Desktop Chrome'], channel: 'chrome' } },
  ],
})
