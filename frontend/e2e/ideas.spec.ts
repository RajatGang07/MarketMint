import { expect, test } from '@playwright/test'

import { openTab, signUp } from './helpers'

/**
 * The three analytics views scan the ~210-stock F&O universe server-side.
 * The server caches the scans (and the autopilot keeps them warm), so these
 * are usually instant — but a cold cache legitimately takes a while, hence
 * the generous timeouts.
 */
test('the three ideas views all load', async ({ page }) => {
  test.setTimeout(300_000)
  await signUp(page)
  await openTab(page, 'Ideas & Signals')

  // Signals board loads itself on first open and stamps when it was built.
  await expect(page.getByText(/updated/).filter({ visible: true }).first()).toBeVisible({
    timeout: 180_000,
  })

  await page.getByRole('button', { name: /Intraday scanner/ }).click()
  await expect(
    page.getByText(/updated|No breakout signals/).filter({ visible: true }).first(),
  ).toBeVisible({ timeout: 120_000 })

  await page.getByRole('button', { name: /Positional ideas/ }).click()
  await expect(page.getByText(/updated/).filter({ visible: true }).first()).toBeVisible({
    timeout: 120_000,
  })
})
