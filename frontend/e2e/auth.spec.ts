import { expect, test } from '@playwright/test'

import { signUp } from './helpers'

test('rejects a wrong password with a readable error', async ({ page }) => {
  await page.goto('/')
  await page.getByPlaceholder('e.g. rajat').fill(`nouser${Date.now()}`)
  await page.locator('input[type="password"]').fill('wrong-password-1')
  await page.getByRole('button', { name: 'Sign in', exact: true }).last().click()
  await expect(page.getByText('wrong username or password')).toBeVisible()
})

test('signs out and back in with the same account', async ({ page }) => {
  const user = await signUp(page)

  await page.getByRole('button', { name: 'Sign out' }).click()
  await expect(page.getByRole('button', { name: 'Create account', exact: true })).toBeVisible()

  await page.getByPlaceholder('e.g. rajat').fill(user)
  await page.locator('input[type="password"]').fill('e2e-password-1')
  await page.getByRole('button', { name: 'Sign in', exact: true }).last().click()
  await expect(page.getByText(`@${user}`)).toBeVisible()
})

test('the session survives a reload', async ({ page }) => {
  const user = await signUp(page)
  await page.reload()
  await expect(page.getByText(`@${user}`)).toBeVisible()
})

test('a taken username cannot be registered twice', async ({ page }) => {
  const user = await signUp(page)
  await page.getByRole('button', { name: 'Sign out' }).click()

  await page.getByRole('button', { name: 'Create account', exact: true }).click()
  await page.getByPlaceholder('e.g. rajat').fill(user)
  await page.locator('input[type="password"]').fill('another-pass-1')
  await page.getByRole('button', { name: /Create account & start trading/ }).click()
  await expect(page.getByText('that username is taken')).toBeVisible()
})

test('a five-character password is rejected', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'Create account', exact: true }).click()
  await page.getByPlaceholder('e.g. rajat').fill(`short${Date.now()}`)
  const password = page.locator('input[type="password"]')
  await password.fill('12345')
  await page.getByRole('button', { name: /Create account & start trading/ }).click()

  // The field's native minLength blocks the submit before the server sees
  // it: the form stays put with the input flagged too-short.
  expect(await password.evaluate((el) => (el as HTMLInputElement).validity.tooShort)).toBe(true)
  await expect(page.getByRole('button', { name: /Create account & start trading/ })).toBeVisible()
})

test('signup honours a custom starting equity', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'Create account', exact: true }).click()
  await page.getByPlaceholder('e.g. rajat').fill(`rich${Date.now()}`)
  await page.locator('input[type="password"]').fill('e2e-password-1')
  await page.locator('input[type="number"]').fill('2500000')
  await page.getByRole('button', { name: /Create account & start trading/ }).click()

  await expect(page.getByText('Started at ₹25,00,000.00')).toBeVisible()
})

test('a corrupted session token drops back to the sign-in screen', async ({ page }) => {
  await signUp(page)
  await page.evaluate(() => localStorage.setItem('paper-trading.session', 'garbage-token'))
  await page.reload()
  await expect(page.getByRole('button', { name: 'Create account', exact: true })).toBeVisible()
})
