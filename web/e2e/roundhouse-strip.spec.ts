import { expect, test, type Session } from './fixtures'

// The Roundhouse's strip (#310): the chips rearranged by drag and by
// keyboard, the order kept per browser and surviving a reload; stepping
// follows it; the thumbnails view shows a live view of the current slide
// and its neighbours and the idle mark for the rest.
test('the strip is rearranged by drag and keyboard, the order outlives a reload, and thumbnails show the neighbours live', async ({ page, api }) => {
  const made: Session[] = []
  for (const name of ['e2e strip one', 'e2e strip two', 'e2e strip three', 'e2e strip four']) made.push(await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name }))
  const [one, two, , four] = made as [Session, Session, Session, Session]
  try {
    // Rotation and follow off for the check; the order and the view the page keeps are left to it across the reloads below.
    await page.addInitScript(() => {
      if (!localStorage.getItem('conductor.carousel.settings')) localStorage.setItem('conductor.carousel.settings', JSON.stringify({ intervalMs: 60000, autoplay: false, follow: false }))
    })
    await page.goto('/roundhouse')
    const strip = page.locator('[data-carousel-strip]')
    const chip = (s: Session) => strip.locator(`[data-strip-chip="${s.id}"]`)
    const order = async () => strip.locator('[data-strip-chip]').evaluateAll((els) => els.map((e) => e.getAttribute('data-strip-chip')))
    await expect(chip(four)).toBeVisible({ timeout: 30_000 })
    // The sessions as the page lists them, newest first; the suite's other sessions may sit among them, so the four are compared by rank.
    const rank = async (s: Session) => (await order()).indexOf(s.id)
    expect(await rank(four)).toBeLessThan(await rank(one))
    // A drag of the last of the four before the first of them.
    await chip(one).dragTo(chip(four), { targetPosition: { x: 4, y: 10 } })
    await expect.poll(async () => (await rank(one)) < (await rank(four))).toBe(true)
    // The order outlives a reload, and the keyboard moves a chip: Space picks it up, ArrowRight moves it one place, Space drops it.
    await page.reload()
    await expect(chip(four)).toBeVisible({ timeout: 30_000 })
    expect(await rank(one)).toBeLessThan(await rank(four))
    const before = await rank(one)
    await chip(one).focus()
    await page.keyboard.press('Space')
    await expect(chip(one)).toHaveAttribute('aria-grabbed', 'true')
    await page.keyboard.press('ArrowRight')
    await page.keyboard.press('Space')
    await expect(chip(one)).toHaveAttribute('aria-grabbed', 'false')
    expect(await rank(one)).toBe(before + 1)
    // Escape puts a picked chip back where it was, and keeps the focus on it (Codex's round on #310).
    const kept = await order()
    await chip(two).focus()
    await page.keyboard.press('Space')
    await page.keyboard.press('ArrowLeft')
    await expect(chip(two)).toBeFocused()
    await page.keyboard.press('Escape')
    expect(await order()).toEqual(kept)
    await expect(chip(two)).toBeFocused()
    await expect(chip(two)).toHaveAttribute('aria-grabbed', 'false')
    // Stepping goes in the kept order: → from the first chip shows the second.
    await strip.locator(`[data-strip-chip="${kept[0]}"]`).click()
    await page.keyboard.press('ArrowRight')
    await expect(strip.locator('[data-strip-chip][aria-current="true"]')).toHaveAttribute('data-strip-chip', kept[1]!)
    // Thumbnails: a live view for the current slide and its neighbours, the idle mark for the rest; the choice outlives a reload.
    await page.locator('[data-strip-view]').click()
    await expect(strip.locator('[data-strip-thumb]').first()).toBeVisible()
    await expect(strip.locator('[data-strip-thumb] .terminal-host')).toHaveCount(3, { timeout: 30_000 })
    await expect(strip.locator('[data-strip-idle]').first()).toBeVisible()
    // A thumbnail is inert: nothing in it takes the focus or a key, so Tab from a chip never lands in a neighbour's terminal.
    await expect(strip.locator('[data-strip-thumb]').first()).toHaveAttribute('inert', '')
    await strip.locator(`[data-strip-chip="${kept[0]}"]`).focus()
    await page.keyboard.press('Tab')
    expect(await page.evaluate(() => !!document.activeElement?.closest('[data-strip-thumb]'))).toBe(false)
    await page.reload()
    await expect(page.locator('[data-carousel-strip] [data-strip-thumb]').first()).toBeVisible({ timeout: 30_000 })
    await page.locator('[data-strip-view]').click()
    await expect(page.locator('[data-carousel-strip] [data-strip-thumb]')).toHaveCount(0)
  } finally {
    for (const s of made) await api.stopSession(s.id)
  }
})
