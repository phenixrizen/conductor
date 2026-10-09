import { execFileSync } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The Neovim keymap (design round 12, F8): off by default; the Keys button
// turns it on and the choice is kept per browser; with it on, the real
// Neovim on the session's machine holds the file: keys go to it, its mode
// and command line show, :w writes the file there (a file event by the
// person, so Touched follows), :q closes the tab. Needs `nvim` on PATH where
// the e2e server runs (CI installs it).
const hasNvim = (() => {
  try {
    execFileSync('sh', ['-c', 'command -v nvim'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})()
test.skip(!hasNvim, 'nvim is not on PATH: the Neovim keymap needs the real Neovim (CI installs it)')

test('the Neovim keymap is off until its button, then keys reach the real Neovim, :w writes and :q closes', async ({ page, api, state }) => {
  const cwd = join(state.root, 'files-vim')
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'notes.txt'), 'alpha\nbeta\ngamma\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'vim', cwd })
  const openNotes = async () => {
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    await expect(pane.locator('[data-files-tree] [data-file-node]').first()).toBeVisible({ timeout: 30_000 })
    await pane.locator(`[data-file-node="${cwd}/notes.txt"]`).click()
    await expect(page.locator('[data-editor-area] .monaco-editor')).toBeVisible({ timeout: 30_000 })
    return pane
  }
  try {
    await page.goto(`/sessions/${s.id}`)
    const pane = await openNotes()
    const area = page.locator('[data-editor-area]')
    const lines = area.locator('.monaco-editor .view-lines')
    // Off by default: Monaco's keys, no status line, no key reaches Neovim.
    await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'default')
    await expect(area.locator('[data-nvim-status]')).toHaveCount(0)
    await expect(lines).toContainText('alpha')
    // The button turns it on; Neovim reports normal mode.
    await area.locator('[data-editor-keymap]').click()
    await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'nvim')
    const status = area.locator('[data-nvim-status]')
    await expect(status).toHaveAttribute('data-nvim-mode', /^(n|normal)$/, { timeout: 30_000 })
    // dd removes the first line in Neovim and the editor follows.
    await lines.click()
    await page.keyboard.type('dd')
    await expect(lines).not.toContainText('alpha', { timeout: 15_000 })
    await expect(lines).toContainText('beta')
    // Insert mode shows; the typed text lands; Escape leaves it.
    await page.keyboard.type('i')
    await expect(status.locator('[data-nvim-mode-words]')).toHaveText('-- INSERT --', { timeout: 15_000 })
    await page.keyboard.type('hello ')
    await page.keyboard.press('Escape')
    await expect(status.locator('[data-nvim-mode-words]')).toHaveText('', { timeout: 15_000 })
    await expect(lines).toContainText('hello beta')
    // :w shows on the command line, writes the file on the machine and says so; the write is a file event by the person.
    await page.keyboard.type(':w')
    await expect(status.locator('[data-nvim-cmdline]')).toHaveText(':w')
    await page.keyboard.press('Enter')
    await expect(status.locator('[data-nvim-message]')).toContainText('written', { timeout: 15_000 })
    expect(readFileSync(join(cwd, 'notes.txt'), 'utf8')).toBe('hello beta\ngamma\n')
    await pane.locator('[data-files-section="touched"]').click()
    await expect(pane.locator('[data-files-touched] [data-touched-op="write"]').first()).toContainText('nvim', { timeout: 15_000 })
    // :q closes the tab.
    await lines.click()
    await page.keyboard.type(':q')
    await page.keyboard.press('Enter')
    await expect(page.locator('[data-editor-tab]')).toHaveCount(0, { timeout: 15_000 })
    // The choice is kept across a reload; the button turns it off again.
    await page.reload()
    await openNotes()
    await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'nvim')
    await expect(area.locator('[data-nvim-status]')).toHaveAttribute('data-nvim-mode', /^(n|normal)$/, { timeout: 30_000 })
    await area.locator('[data-editor-keymap]').click()
    await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'default')
    await expect(area.locator('[data-nvim-status]')).toHaveCount(0)
  } finally {
    await api.stopSession(s.id)
  }
})
