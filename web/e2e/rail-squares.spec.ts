import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The collapsed sidebar (#314): the session open on the page carries the
// selection mark on its square (a primary bar down its outer edge), and two
// squares of one agent never look the same: in different folders they show
// their folders' letters, in the same folder an ordinal top left as well.
// The mark and the letters read in both colour modes.
test('the open session is marked on the rail, and two squares of one agent differ', async ({ page, api, state }) => {
  const docs = join(state.root, 'docs-work')
  mkdirSync(docs, { recursive: true })
  // Two Claude Code sessions in the default folder, one in another, and a Codex beside them.
  const a = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e rail a' })
  const b = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e rail b' })
  const c = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e rail docs', cwd: docs })
  const x = await api.ok<Session>('POST', '/api/sessions', { agentId: 'codex', name: 'e2e rail codex' })
  try {
    await page.goto(`/sessions/${a.id}`)
    await page.locator('[data-sidebar-collapse]').click()
    const rail = page.locator('[data-rail]')
    await expect(rail).toBeVisible()
    const square = (id: string) => rail.locator(`[data-rail-session="${id}"]`)
    // The selection mark on the open session alone; aria-current with it.
    await expect(square(a.id).locator('[data-rail-current]')).toBeVisible()
    await expect(square(a.id)).toHaveAttribute('aria-current', 'page')
    await expect(square(b.id).locator('[data-rail-current]')).toHaveCount(0)
    await expect(square(c.id).locator('[data-rail-current]')).toHaveCount(0)
    // Three Claude Codes: the two in the default folder share its letters and get ordinals; the one in docs-work shows DO; Codex keeps CX.
    const letters = (id: string) => square(id).locator('[data-avatar-label]')
    const root = ((await api.session(a.id)).cwd ?? '').split('/').pop()!.slice(0, 2).toUpperCase()
    expect(root).toMatch(/^[A-Z0-9_.-]{2}$/)
    await expect(letters(a.id)).toHaveText(root)
    await expect(letters(b.id)).toHaveText(root)
    await expect(letters(c.id)).toHaveText('DO')
    await expect(letters(x.id)).toHaveText('CX')
    await expect(square(a.id).locator('[data-rail-ordinal]')).toHaveText(/^[12]$/)
    await expect(square(b.id).locator('[data-rail-ordinal]')).toHaveText(/^[12]$/)
    expect(await square(a.id).locator('[data-rail-ordinal]').textContent()).not.toBe(await square(b.id).locator('[data-rail-ordinal]').textContent())
    await expect(square(c.id).locator('[data-rail-ordinal]')).toHaveCount(0)
    await expect(square(x.id).locator('[data-rail-ordinal]')).toHaveCount(0)
    // Moving to the other session moves the mark.
    await square(b.id).click()
    await expect(page).toHaveURL(new RegExp(`/sessions/${b.id}$`))
    await expect(square(b.id).locator('[data-rail-current]')).toBeVisible()
    await expect(square(a.id).locator('[data-rail-current]')).toHaveCount(0)
    // The mark is drawn in both colour modes (a colour, never transparent).
    for (const mode of ['dark', 'light']) {
      await page.evaluate((m) => localStorage.setItem('nuxt-color-mode', m), mode)
      await page.reload()
      await expect(rail).toBeVisible()
      const bg = await square(b.id).locator('[data-rail-current]').evaluate((el) => getComputedStyle(el).backgroundColor)
      expect(bg, mode).not.toMatch(/rgba\(\d+, \d+, \d+, 0\)|transparent/)
    }
  } finally {
    for (const s of [a, b, c, x]) await api.stopSession(s.id)
  }
})
