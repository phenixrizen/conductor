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

// The table reads as columns (round 14: each row sized its own key columns,
// so nothing lined up and the owner could not tell them apart): at 1440 the
// modal is wide, every row's two key columns start where the first row's do,
// and the keys never run into the action; at 390 a row stacks and nothing
// runs off the side.
test('the shortcuts table lines its columns up at 1440 and fits a phone at 390', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await page.goto('/agents')
  await expect(page.locator('[data-session-list]')).toBeVisible({ timeout: 30_000 })
  await page.keyboard.press('?')
  const modal = page.locator('[data-shortcuts]')
  await expect(modal).toBeVisible()
  expect((await modal.boundingBox())!.width).toBeGreaterThan(650)
  // Every group past the first names the columns again.
  await expect(modal.locator('[data-shortcuts-group-columns]')).toHaveCount((await modal.locator('[data-shortcuts-group]').count()) - 1)
  const cells = await modal.locator('[data-shortcut]').evaluateAll((rows) =>
    rows.map((r) => {
      const label = r.children[0]!.getBoundingClientRect()
      const term = r.children[1]!.getBoundingClientRect()
      const out = r.children[2]!.getBoundingClientRect()
      return { labelRight: label.right, term: Math.round(term.left), termWidth: Math.round(term.width), out: Math.round(out.left) }
    }),
  )
  expect(cells.length).toBeGreaterThan(10)
  for (const c of cells) {
    expect(Math.abs(c.term - cells[0]!.term)).toBeLessThanOrEqual(1)
    expect(Math.abs(c.out - cells[0]!.out)).toBeLessThanOrEqual(1)
    expect(Math.abs(c.termWidth - cells[0]!.termWidth)).toBeLessThanOrEqual(1)
    expect(c.labelRight).toBeLessThanOrEqual(c.term + 1)
  }
  await page.keyboard.press('Escape')
  await page.setViewportSize({ width: 390, height: 844 })
  await page.keyboard.press('?')
  await expect(modal).toBeVisible()
  const overflow = await modal.evaluate((el) => {
    const scroller = el.closest('[role="dialog"]') ?? el
    return { scroll: scroller.scrollWidth, client: scroller.clientWidth }
  })
  expect(overflow.scroll).toBeLessThanOrEqual(overflow.client + 1)
  await expect(modal.locator('[data-shortcut="Go to the Yard"] [data-shortcut-outside]')).toBeVisible()
})
