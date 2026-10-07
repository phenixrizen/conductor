import { expect, test } from './fixtures'

// The page chords go by each page's initial: G then Y the Yard, G then R the
// Roundhouse, G then C Crews (A Agents and E Events as before). The shortcuts
// modal lists every action in three columns: the action, its keys in a
// terminal, its keys outside one.
test('G then Y, R and C go to the Yard, the Roundhouse and Crews; the modal shows three columns', async ({ page }) => {
  await page.goto('/agents')
  await expect(page.locator('[data-session-list]')).toBeVisible({ timeout: 30_000 })
  await page.keyboard.press('g')
  await page.keyboard.press('y')
  await expect(page).toHaveURL(/\/yard/)
  await page.keyboard.press('g')
  await page.keyboard.press('r')
  await expect(page).toHaveURL(/\/roundhouse/)
  await page.keyboard.press('g')
  await page.keyboard.press('c')
  await expect(page).toHaveURL(/\/crews/)

  await page.keyboard.press('?')
  const modal = page.locator('[data-shortcuts]')
  await expect(modal).toBeVisible()
  await expect(modal.locator('[data-shortcuts-columns]')).toHaveText(/Action\s*In a terminal\s*Outside/)
  const yard = modal.locator('[data-shortcut="Go to the Yard"]')
  await expect(yard.locator('[data-shortcut-terminal]')).toHaveText(/Alt.*Y/i)
  await expect(yard.locator('[data-shortcut-outside]')).toHaveText(/G\s*then\s*Y/)
  // What needs the list focused has no key in a terminal.
  await expect(modal.locator('[data-shortcut="Next row (or ↓)"] [data-shortcut-terminal]')).toHaveText('·')
})
