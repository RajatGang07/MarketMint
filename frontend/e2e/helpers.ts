import { expect, type Page } from '@playwright/test'

/**
 * Creates a throwaway paper account and lands on the dashboard. Signup is the
 * cheapest honest login: no fixtures, no shared state between runs, and the
 * account starts with the default ₹10L of paper money.
 */
export async function signUp(page: Page): Promise<string> {
  const user = `e2e${Date.now()}${Math.floor(Math.random() * 1000)}`
  await page.goto('/')
  await page.getByRole('button', { name: 'Create account', exact: true }).click()
  await page.getByPlaceholder('e.g. rajat').fill(user)
  await page.locator('input[type="password"]').fill('e2e-password-1')
  await page.getByRole('button', { name: /Create account & start trading/ }).click()
  await expect(page.getByRole('button', { name: 'History' })).toBeVisible()
  return user
}

/** Switches to a main navigation tab by its label. */
export async function openTab(page: Page, name: string) {
  await page.getByRole('button', { name, exact: true }).click()
}

/**
 * Waits until the order ticket prices the symbol off the live feed. During a
 * feed cool-down the chip reads "SYM · —" and the engine would rightly refuse
 * to trade, so tests wait for a real price the same way a person would. The
 * timeout comfortably covers the chain's two-minute provider cool-down.
 */
export async function waitForLiveTicket(page: Page, symbol: string) {
  await expect(page.getByText(new RegExp(`${symbol} · ₹`))).toBeVisible({ timeout: 180_000 })
}
