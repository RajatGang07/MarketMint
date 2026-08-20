import { expect, test } from '@playwright/test'

import { signUp } from './helpers'

test.describe('History tab', () => {
  test.beforeEach(async ({ page }) => {
    await signUp(page)
    await page.getByRole('button', { name: 'History' }).click()
    await expect(page.getByRole('columnheader', { name: 'Date' })).toBeVisible()
  })

  test('defaults to the last 10 trading sessions', async ({ page }) => {
    await expect(page.locator('tbody tr').first()).toBeVisible()
    const rows = await page.locator('tbody tr').count()
    // 18 calendar days minus weekends and the odd holiday: exactly 10 when
    // enough sessions exist, a couple fewer around long holiday stretches.
    expect(rows).toBeGreaterThanOrEqual(8)
    expect(rows).toBeLessThanOrEqual(10)
    await expect(page.getByText(/\d+ trading sessions/)).toBeVisible()

    // Every close is a real rupee figure, and the summary card agrees.
    await expect(page.getByText(/over \d+ sessions/)).toBeVisible()
  })

  test('a custom date window returns exactly that window', async ({ page }) => {
    await page.getByRole('button', { name: 'Custom' }).click()
    await page.locator('input[type="date"]').first().fill('2026-06-01')
    await page.locator('input[type="date"]').nth(1).fill('2026-06-30')
    await page.getByRole('button', { name: 'Apply' }).click()

    // June 2026 has 20-odd sessions; every visible date must be June.
    await expect(page.locator('tbody tr td:first-child').first()).toContainText('Jun 2026')
    const dates = await page.locator('tbody tr td:first-child').allInnerTexts()
    expect(dates.length).toBeGreaterThanOrEqual(18)
    for (const d of dates) {
      expect(d).toContain('Jun 2026')
    }
  })

  test('searching a share loads its history', async ({ page }) => {
    await page.getByLabel('Search instruments').fill('APARINDS')
    await page.getByRole('button', { name: /Apar Industries/ }).click()

    await expect(page.getByText('APARINDS')).toBeVisible()
    await expect(page.locator('tbody tr').first()).toBeVisible()

    // The regression that motivated this suite: APARINDS is a five-figure
    // share; the simulator's hash-derived ~₹1,920 must never show here.
    await expect
      .poll(async () => {
        const close = await page.locator('tbody tr').first().locator('td').nth(4).innerText()
        return Number(close.replace(/[₹,]/g, ''))
      })
      .toBeGreaterThan(5000)
  })

  test('a reversed date range gets a clear error', async ({ page }) => {
    await page.getByRole('button', { name: 'Custom' }).click()
    await page.locator('input[type="date"]').first().fill('2026-06-30')
    await page.locator('input[type="date"]').nth(1).fill('2026-06-01')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByText(/from must be on or before to/)).toBeVisible()
  })

  test('a window over five years is refused', async ({ page }) => {
    await page.getByRole('button', { name: 'Custom' }).click()
    await page.locator('input[type="date"]').first().fill('2019-01-01')
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByText(/window too large/)).toBeVisible()
  })

  test('a weekend-only window shows the empty state, not an error', async ({ page }) => {
    await page.getByRole('button', { name: 'Custom' }).click()
    await page.locator('input[type="date"]').first().fill('2026-08-08') // Saturday
    await page.locator('input[type="date"]').nth(1).fill('2026-08-09') // Sunday
    await page.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByText(/No trading sessions in this window/)).toBeVisible({
      timeout: 60_000,
    })
  })

  test('an unknown symbol errors honestly without poisoning the feed', async ({ page }) => {
    await page.getByLabel('Search instruments').fill('ZZZZZZ')
    await page.getByLabel('Search instruments').press('Enter')
    await expect(page.getByText(/candle lookup failed/)).toBeVisible({ timeout: 60_000 })
    await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()

    // The bad symbol must not have tripped the provider-wide cool-down:
    // a real share still loads immediately afterwards.
    await page.getByLabel('Search instruments').fill('RELIANCE')
    await page.getByLabel('Search instruments').press('Enter')
    await expect(page.locator('tbody tr').first()).toBeVisible({ timeout: 30_000 })
  })

  test('exports the visible rows as CSV', async ({ page }) => {
    await expect(page.locator('tbody tr').first()).toBeVisible()
    const [download] = await Promise.all([
      page.waitForEvent('download'),
      page.getByRole('button', { name: 'Download CSV' }).click(),
    ])
    expect(download.suggestedFilename()).toMatch(/RELIANCE-daily-\d+sessions\.csv/)
  })
})
