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
