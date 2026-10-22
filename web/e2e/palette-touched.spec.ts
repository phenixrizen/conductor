import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The palette's Touched group across sessions (#304): a file a session
// touched before this tab opened is not in the tab's feed (the feed holds
// what arrived while the tab was open); the group finds it from the
// server's index, and its row on another session's page opens that
// session's page with the file in its editor (?open=, the path taken whole:
// a name ending in `:digits` is a name, the whole-batch round). On the
// file's own session page the row opens the file in this page's editor.
test('the Touched group finds a file touched before the tab opened, and its row opens the other session with the file', async ({ page, api, state }) => {
  const cwdA = join(state.root, 'touched-a')
  const cwdB = join(state.root, 'touched-b')
  mkdirSync(cwdA, { recursive: true })
  mkdirSync(cwdB, { recursive: true })
  writeFileSync(join(cwdA, 'alpha-index.go'), 'package alpha\n\nfunc A() {}\n')
  writeFileSync(join(cwdA, 'report:2026'), 'a colon in the name\n')
  const a = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e touched alpha', cwd: cwdA })
  const b = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e touched beta', cwd: cwdB })
  try {
    // Reported before any page of this test opens: only the server's index knows them.
    await api.ok('POST', `/api/sessions/${a.id}/events`, { type: 'file', op: 'edit', path: 'alpha-index.go', tool: 'Edit' })
    await api.ok('POST', `/api/sessions/${a.id}/events`, { type: 'file', op: 'write', path: 'report:2026', tool: 'Write' })
    await page.goto(`/sessions/${b.id}`)
    await expect(page.locator('.terminal-host .xterm-helper-textarea').first()).toBeFocused({ timeout: 30_000 })
    await page.keyboard.press('Alt+/')
    const palette = page.locator('[data-palette]')
    await expect(palette).toBeVisible()
    await palette.locator('input[data-palette-input]').fill(':report')
    const row = palette.locator('[data-palette-group="touched"] [data-palette-row]').first()
    await expect(row).toContainText('report:2026', { timeout: 15_000 })
    await expect(row).toContainText('e2e touched alpha')
    await expect(row).toContainText('wrote with Write')
    await row.click()
    // The other session's page, with the file open in its editor: the whole name, nothing read as a line number.
    await expect(page).toHaveURL(new RegExp(`/sessions/${a.id}\\?open=`), { timeout: 15_000 })
    const area = page.locator('[data-editor-area]')
    await expect(area).toHaveAttribute('data-editor-area', 'open', { timeout: 30_000 })
    await expect(area.locator('[data-editor-crumbs]')).toContainText('report:2026')
    // The file itself, read once the terminal connected (opened before that, the read failed: Codex's round).
    await expect(area.locator('.monaco-editor .view-lines')).toContainText('a colon in the name', { timeout: 30_000 })
    // On the file's own page the row opens the file here: the palette closes and the editor shows it. The chord is pressed
    // from wherever the focus is (the editor just opened has it; CI's run waited on the terminal's focus in vain).
    await page.keyboard.press('Alt+/')
    await palette.locator('input[data-palette-input]').fill(':alpha-index')
    const own = palette.locator('[data-palette-group="touched"] [data-palette-row]').first()
    await expect(own).toContainText('alpha-index.go', { timeout: 15_000 })
    await expect(own).toContainText('edited with Edit')
    await own.click()
    await expect(palette).toBeHidden()
    await expect(page).toHaveURL(new RegExp(`/sessions/${a.id}`))
    await expect(area.locator('[data-editor-crumbs]')).toContainText('alpha-index.go')
    await expect(area.locator('.monaco-editor .view-lines')).toContainText('package alpha', { timeout: 30_000 })
  } finally {
    await api.stopSession(a.id)
    await api.stopSession(b.id)
  }
})
