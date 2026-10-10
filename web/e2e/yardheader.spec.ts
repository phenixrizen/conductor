import { expect, test, type Session } from './fixtures'

// The Yard's focused header at phone width (round 14: its status, transport
// and viewer badges and its Open page and Stop ran under one another and the
// title was gone at 390): the title shows, the transport and viewer badges
// hide, Open page and Stop fold into a menu; nothing overlaps and nothing
// runs off the side. At 1440 the buttons stand in the header as before.
test("the Yard's focused header keeps its title at 390, its actions in a menu", async ({ page, api }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'yard phone header' })
  try {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto(`/yard?focus=${s.id}`)
    const header = page.locator('[data-yard-header]')
    await expect(header).toBeVisible({ timeout: 30_000 })
    await expect(header).toContainText('yard phone header')
    // Every visible piece of the header: inside the screen, and clear of every other.
    const boxes = await header.evaluate((root) => {
      const out: Array<{ what: string; left: number; right: number; top: number; bottom: number }> = []
      const pick = Array.from(root.querySelectorAll('h1, [class*="badge"], .inline-flex, button, a'))
      for (const el of pick) {
        const r = el.getBoundingClientRect()
        const style = getComputedStyle(el)
        if (!r.width || !r.height || style.visibility === 'hidden' || style.display === 'none') continue
        if (pick.some((o) => o !== el && o.contains(el))) continue // the outermost piece only
        out.push({ what: (el.textContent || el.getAttribute('aria-label') || el.tagName).trim().slice(0, 30), left: r.left, right: r.right, top: r.top, bottom: r.bottom })
      }
      return { pieces: out, width: window.innerWidth }
    })
    expect(boxes.pieces.length).toBeGreaterThan(2)
    for (const a of boxes.pieces) {
      expect(a.left, `${a.what} starts on screen`).toBeGreaterThanOrEqual(0)
      expect(a.right, `${a.what} ends on screen`).toBeLessThanOrEqual(boxes.width + 0.5)
      for (const b of boxes.pieces) {
        if (a === b) continue
        const overlap = Math.min(a.right, b.right) - Math.max(a.left, b.left) > 1 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 1
        expect(overlap, `${a.what} overlaps ${b.what}`).toBe(false)
      }
    }
    // Open page and Stop are in a menu; Stop stops the session.
    await expect(page.locator('[data-yard-focus-actions]')).toBeHidden()
    const menu = page.locator('[data-yard-focus-menu]')
    await expect(menu).toBeVisible()
    await menu.click()
    await expect(page.getByRole('menuitem', { name: 'Open page' })).toBeVisible()
    await page.getByRole('menuitem', { name: 'Stop' }).click()
    await expect.poll(async () => (await api.ok<{ session: { status: string } }>('GET', `/api/sessions/${s.id}`)).session.status, { timeout: 15_000 }).toMatch(/stopped|exited/)
    // At 1440 the actions stand in the header and the menu is gone.
    await page.setViewportSize({ width: 1440, height: 900 })
    await expect(page.locator('[data-yard-focus-actions]')).toBeVisible()
    await expect(menu).toBeHidden()
  } finally {
    await api.stopSession(s.id)
  }
})
