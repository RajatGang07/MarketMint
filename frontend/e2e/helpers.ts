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
