import { expect, test } from './fixtures'

// Dark is the theme whatever the OS prefers; light is a choice the sidebar's
// theme button makes and the browser remembers.
test('the workbench opens dark on a light OS, and remembers a switch to light', async ({ browser, state }) => {
  const ctx = await browser.newContext({ colorScheme: 'light' })
  const page = await ctx.newPage()
  await page.addInitScript((token) => localStorage.setItem('conductor.workbenchToken', token), state.token)
  await page.goto('/')
  await expect(page.locator('html')).toHaveClass(/\bdark\b/)
  await page.getByRole('button', { name: 'Toggle theme' }).click()
  await expect(page.locator('html')).not.toHaveClass(/\bdark\b/)
  await page.reload()
  await expect(page.locator('html')).not.toHaveClass(/\bdark\b/)
  await ctx.close()
})
