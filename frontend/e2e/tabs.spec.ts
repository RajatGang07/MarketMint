import { expect, test } from '@playwright/test'

import { openTab, signUp } from './helpers'

test('forecast analyses the selected share across horizons', async ({ page }) => {
  await signUp(page)
  await openTab(page, 'Forecast')

  // The tab inherits the Trade tab's selection and analyses it immediately.
  // A feed cool-down surfaces an error with a Retry button — press it the
  // way a person would until the analysis lands.
  await expect(async () => {
    const retry = page.getByRole('button', { name: 'Retry' })
    if (await retry.isVisible()) await retry.click()
    await expect(page.getByText(/as of/)).toBeVisible({ timeout: 20_000 })
  }).toPass({ timeout: 180_000 })
  await expect(page.getByText(/Market (open|closed)/).first()).toBeVisible()

  // Picking a different share re-runs the analysis. Enter submits the typed
  // symbol directly, so this doesn't depend on how the instrument master
  // spells the company name.
  await page.getByLabel('Search instruments').fill('TCS')
  await page.getByLabel('Search instruments').press('Enter')
  await expect(async () => {
    const retry = page.getByRole('button', { name: 'Retry' })
    if (await retry.isVisible()) await retry.click()
    await expect(page.getByText('TCS', { exact: true }).first()).toBeVisible({ timeout: 20_000 })
  }).toPass({ timeout: 180_000 })
})

test('autopilot shows its controls and decision log without enabling it', async ({ page }) => {
  await signUp(page)
  await openTab(page, 'Autopilot')

  await expect(page.getByText('Decision log')).toBeVisible()
  await expect(page.locator('button[aria-pressed]').first()).toBeVisible()
  await expect(page.getByText(/One trade per share per day/)).toBeVisible()
})

test('charges explains all three product types', async ({ page }) => {
  await signUp(page)
  await openTab(page, 'Charges')

  await expect(page.getByText('Delivery (CNC)')).toBeVisible()
  await expect(page.getByText('Intraday (MIS)')).toBeVisible()
  await expect(page.getByText('MTF / Pay Later')).toBeVisible()
  await expect(page.getByLabel('Search instruments')).toBeVisible()
})

test('how it works renders its guide', async ({ page }) => {
  await signUp(page)
  await openTab(page, 'How it works')
  expect(await page.locator('h2').count()).toBeGreaterThanOrEqual(3)
})
