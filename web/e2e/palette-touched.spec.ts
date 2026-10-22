import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The palette's Touched group across sessions (#304): a file a session
// touched before this tab opened is not in the tab's feed (the feed holds
// what arrived while the tab was open); the group finds it from the
// server's index, and its row on another session's page opens that
// session's page with the file in its editor (?open=). On the file's own
// session page the row opens the file in this page's editor.
test('the Touched group finds a file touched before the tab opened, and its row opens the other session with the file', async ({ page, api, state }) => {
  const cwdA = join(state.root, 'touched-a')
  const cwdB = join(state.root, 'touched-b')
  mkdirSync(cwdA, { recursive: true })
  mkdirSync(cwdB, { recursive: true })
  writeFileSync(join(cwdA, 'alpha-index.go'), 'package alpha\n\nfunc A() {}\n')
  const a = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e touched alpha', cwd: cwdA })
  const b = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e touched beta', cwd: cwdB })
  try {
    // Reported before any page of this test opens: only the server's index knows it.
    await api.ok('POST', `/api/sessions/${a.id}/events`, { type: 'file', op: 'edit', path: 'alpha-index.go', tool: 'Edit' })
    await page.goto(`/sessions/${b.id}`)
    await expect(page.locator('.terminal-host .xterm-helper-textarea').first()).toBeFocused({ timeout: 30_000 })
    await page.keyboard.press('Alt+/')
    const palette = page.locator('[data-palette]')
    await expect(palette).toBeVisible()
    await palette.locator('input[data-palette-input]').fill(':alpha-index')
    const row = palette.locator('[data-palette-group="touched"] [data-palette-row]').first()
    await expect(row).toContainText('alpha-index.go', { timeout: 15_000 })
    await expect(row).toContainText('e2e touched alpha')
    await expect(row).toContainText('edited with Edit')
    await row.click()
    // The other session's page, with the file open in its editor.
    await expect(page).toHaveURL(new RegExp(`/sessions/${a.id}\\?open=`), { timeout: 15_000 })
    const area = page.locator('[data-editor-area]')
    await expect(area).toHaveAttribute('data-editor-area', 'open', { timeout: 30_000 })
    await expect(area.locator('[data-editor-crumbs]')).toContainText('alpha-index.go')
    // On the file's own page the row opens the file here: the palette closes and the editor stays on it.
    await expect(page.locator('.terminal-host .xterm-helper-textarea').first()).toBeFocused({ timeout: 30_000 })
    await page.keyboard.press('Alt+/')
    await palette.locator('input[data-palette-input]').fill(':alpha-index')
    const own = palette.locator('[data-palette-group="touched"] [data-palette-row]').first()
    await expect(own).toContainText('alpha-index.go', { timeout: 15_000 })
    await own.click()
    await expect(palette).toBeHidden()
    await expect(page).toHaveURL(new RegExp(`/sessions/${a.id}`))
    await expect(area.locator('[data-editor-crumbs]')).toContainText('alpha-index.go')
  } finally {
    await api.stopSession(a.id)
    await api.stopSession(b.id)
  }
})
