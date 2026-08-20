import { expect, test } from '@playwright/test'

import { signUp } from './helpers'

test('signup lands on a live dashboard', async ({ page }) => {
  await signUp(page)

  // The account cards render with the fresh paper balance.
  await expect(page.getByText('Equity', { exact: true })).toBeVisible()
  await expect(page.getByText('Cash available')).toBeVisible()

  // The quote header prices the default selection off the live feed.
  await expect(page.getByText(/₹[\d,]+/).first()).toBeVisible()

  // The feed badge must say live — an E2E run against simulated prices
  // would validate exactly the failure mode the platform refuses.
  await expect(page.getByText(/Live · /)).toBeVisible()
})
