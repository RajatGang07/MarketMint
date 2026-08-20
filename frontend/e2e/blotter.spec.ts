import { expect, test } from '@playwright/test'

import { signUp, waitForLiveTicket } from './helpers'

test('the blotter paginates past ten rows', async ({ page, baseURL }) => {
  test.setTimeout(300_000)
  await signUp(page)
  await waitForLiveTicket(page, 'RELIANCE')

  // Seed twelve orders through the API — order history rows appear whether
  // or not the market is open to fill them, so this stays deterministic.
  // Each seed retries through any transient feed cool-down.
  const token = await page.evaluate(() => localStorage.getItem('paper-trading.session'))
  for (const symbol of [
    'SBIN', 'ITC', 'WIPRO', 'TATASTEEL', 'ONGC', 'NTPC',
    'COALINDIA', 'GAIL', 'BPCL', 'IOC', 'HINDALCO', 'VEDL',
  ]) {
    await expect(async () => {
      const res = await page.request.post(`${baseURL}/orders`, {
        headers: { Authorization: `Bearer ${token}` },
        data: { trading_symbol: symbol, transaction_type: 'BUY', order_type: 'MARKET', quantity: 1 },
      })
      expect(res.ok()).toBeTruthy()
    }).toPass({ timeout: 180_000, intervals: [2_000, 5_000, 10_000] })
  }

  await page.reload()
  await page.getByRole('tab', { name: /Order history/ }).click()

  // Page one: ten rows and the range label.
  await expect(page.getByText('1–10 of 12')).toBeVisible()
  await expect(page.locator('tbody tr')).toHaveCount(10)

  // Page two: the remaining rows, with Next disabled at the end.
  await page.getByRole('button', { name: '2', exact: true }).click()
  await expect(page.getByText('11–12 of 12')).toBeVisible()
  await expect(page.locator('tbody tr')).toHaveCount(2)
  await expect(page.getByRole('button', { name: 'Next ›' })).toBeDisabled()
})
