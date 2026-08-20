import { expect, test } from '@playwright/test'

import { signUp, waitForLiveTicket } from './helpers'

test.describe('Trade tab', () => {
  test.beforeEach(async ({ page }) => {
    await signUp(page)
  })

  test('watchlist selection drives the header and the ticket', async ({ page }) => {
    await page.locator('li').filter({ hasText: 'TCS' }).getByRole('button').first().click()
    await expect(page.getByRole('heading', { name: 'TCS' })).toBeVisible()
    await expect(page.getByText(/TCS · /)).toBeVisible() // ticket reprices to the selection
  })

  test('search adds a share to the watchlist; remove drops it', async ({ page }) => {
    await page.getByLabel('Search instruments').fill('KOTAKBANK')
    await page.getByRole('button', { name: /Kotak Mahindra Bank/i }).first().click()

    await expect(page.getByRole('heading', { name: 'KOTAKBANK' })).toBeVisible()
    const row = page.locator('li').filter({ hasText: 'KOTAKBANK' })
    await expect(row).toBeVisible()

    await row.hover()
    await page.getByLabel('Remove KOTAKBANK from watchlist').click()
    await expect(page.getByLabel('Remove KOTAKBANK from watchlist')).toHaveCount(0)
  })

  test('the chart switches ranges', async ({ page }) => {
    // The first paint may sit out a feed cool-down; the chart polls its way
    // back to live data.
    await expect(page.locator('svg path').first()).toBeVisible({ timeout: 180_000 })
    await page.getByRole('button', { name: '1M', exact: true }).click()
    await expect(page.locator('svg path').first()).toBeVisible({ timeout: 60_000 })
    await page.getByRole('button', { name: '1Y', exact: true }).click()
    await expect(page.locator('svg path').first()).toBeVisible({ timeout: 60_000 })
  })

  test('a limit buy rests as an open order and cancels cleanly', async ({ page }) => {
    await waitForLiveTicket(page, 'RELIANCE')
    // ₹1 is far below any real price, so the order can only rest.
    await page.getByLabel('Order type').selectOption('LIMIT')
    await page.getByLabel('Limit price').fill('1')
    await page.getByRole('button', { name: /^Buy 1 / }).click()
    await expect(page.getByText(/^OPEN/)).toBeVisible()

    await page.getByRole('tab', { name: /Open orders/ }).click()
    await expect(page.locator('tbody tr')).toHaveCount(1)
    await page.getByRole('button', { name: 'Cancel', exact: true }).click()
    await expect(page.getByText('Order cancelled')).toBeVisible() // toast
    await expect(page.getByText(/No working orders/)).toBeVisible()
  })

  test('a market buy books a position; selling closes it', async ({ page }) => {
    await waitForLiveTicket(page, 'RELIANCE')
    await page.getByRole('button', { name: /^Buy 1 RELIANCE/ }).click()
    await expect(page.getByText(/^FILLED/)).toBeVisible()

    await page.getByRole('tab', { name: /Positions/ }).click()
    await expect(page.locator('tbody tr').filter({ hasText: 'RELIANCE' })).toBeVisible()

    await page.getByRole('button', { name: 'Sell', exact: true }).click()
    await page.getByRole('button', { name: /^Sell 1 RELIANCE/ }).click()
    await expect(page.getByText(/^FILLED/)).toBeVisible()
    await expect(page.locator('tbody tr').filter({ hasText: 'RELIANCE' })).toHaveCount(0)

    await page.getByRole('tab', { name: /Trades/ }).click()
    await expect(page.locator('tbody tr')).toHaveCount(2)
  })

  test('reset wipes the account back to its starting equity', async ({ page }) => {
    await waitForLiveTicket(page, 'RELIANCE')
    await page.getByRole('button', { name: /^Buy 1 RELIANCE/ }).click()
    await expect(page.getByText(/^FILLED/)).toBeVisible()

    await page.getByRole('button', { name: 'Reset', exact: true }).click()
    await expect(page.getByText('Reset the paper account?')).toBeVisible()
    await page.getByRole('button', { name: 'Reset everything' }).click()

    await expect(page.getByText(/Account reset/)).toBeVisible() // toast
    await expect(page.getByText(/No open positions/)).toBeVisible()
  })
})
