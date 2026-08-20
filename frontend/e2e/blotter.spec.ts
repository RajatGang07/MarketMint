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

test('a fresh account shows honest empty states everywhere', async ({ page }) => {
  await signUp(page)

  await expect(page.getByText(/No open positions/)).toBeVisible()
  await page.getByRole('tab', { name: /Open orders/ }).click()
  await expect(page.getByText(/No working orders/)).toBeVisible()
  await page.getByRole('tab', { name: /Order history/ }).click()
  await expect(page.getByText('No orders yet.')).toBeVisible()
  await page.getByRole('tab', { name: /Trades/ }).click()
  await expect(page.getByText('No trades yet.')).toBeVisible()
})

test('the pager clamps instead of stranding you when the last page empties', async ({ page, baseURL }) => {
  test.setTimeout(300_000)
  await signUp(page)
  await waitForLiveTicket(page, 'RELIANCE')

  // Eleven ₹1 limit buys can only rest, giving Open orders two pages.
  const token = await page.evaluate(() => localStorage.getItem('paper-trading.session'))
  for (const symbol of [
    'SBIN', 'ITC', 'WIPRO', 'TATASTEEL', 'ONGC', 'NTPC',
    'COALINDIA', 'GAIL', 'BPCL', 'IOC', 'HINDALCO',
  ]) {
    await expect(async () => {
      const res = await page.request.post(`${baseURL}/orders`, {
        headers: { Authorization: `Bearer ${token}` },
        data: {
          trading_symbol: symbol,
          transaction_type: 'BUY',
          order_type: 'LIMIT',
          quantity: 1,
          limit_price: 1,
        },
      })
      expect(res.ok()).toBeTruthy()
    }).toPass({ timeout: 180_000, intervals: [2_000, 5_000, 10_000] })
  }

  await page.reload()
  await page.getByRole('tab', { name: /Open orders/ }).click()
  await expect(page.getByText('1–10 of 11')).toBeVisible()

  // Cancel the lone row on page two; the pager must clamp back to page one
  // (ten rows, single page, pager hidden) rather than showing an empty page.
  await page.getByRole('button', { name: '2', exact: true }).click()
  await expect(page.getByText('11–11 of 11')).toBeVisible()
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()

  await expect(page.locator('tbody tr')).toHaveCount(10)
  await expect(page.getByText(/of 11/)).toHaveCount(0)
})
