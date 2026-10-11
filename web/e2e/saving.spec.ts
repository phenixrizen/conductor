import { execFileSync } from 'node:child_process'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { expect, test, type Session } from './fixtures'

function git(dir: string, ...args: string[]) {
  execFileSync('git', ['-c', 'user.name=e2e', '-c', 'user.email=e2e@conductor.invalid', '-c', 'commit.gpgsign=false', ...args], { cwd: dir, stdio: 'ignore' })
}

const hasNvim = (() => {
  try {
    execFileSync('sh', ['-c', 'command -v nvim'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})()

// Editing (design round 12, F6): a controller edits a file in Monaco, the
// tab wears a dot, Ctrl+S writes it on the session's machine and lands in
// Touched; a file changed on disk since it was read refuses the save with
// what changed it, and Compare, Reload and Save anyway; a tab with unsaved
// changes asks before it closes; a view-only guest gets Read only.
test('a controller edits and saves; a file changed on disk is caught; an unsaved tab asks before closing', async ({ page, api, state, browser }) => {
  const cwd = join(state.root, 'files-saving')
  mkdirSync(cwd, { recursive: true })
  const file = join(cwd, 'notes.txt')
  writeFileSync(file, 'alpha\nbeta\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'saving', cwd })
  const area = page.locator('[data-editor-area]')
  const lines = area.locator('.monaco-editor .view-lines')
  const openNotes = async () => {
    const pane = page.locator('[data-files-mode]')
    await pane.locator('[data-files-section="explorer"]').click()
    await pane.locator(`[data-file-node="${file}"]`).click()
    await expect(lines).toBeVisible({ timeout: 30_000 })
    return pane
  }
  const typeAtEnd = async (text: string) => {
    await lines.click()
    await page.keyboard.press('Control+End')
    await page.keyboard.type(text)
  }
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    await expect(page.locator('[data-files-tree] [data-file-node]').first()).toBeVisible({ timeout: 30_000 })
    const pane = await openNotes()
    // Editable: Save shows, disabled until something changes; no Read only.
    const saveButton = area.locator('[data-editor-save]')
    await expect(saveButton).toBeDisabled()
    await expect(area.locator('[data-editor-readonly]')).toHaveCount(0)
    await typeAtEnd('gamma\n')
    await expect(area.locator('[data-editor-dirty]')).toHaveCount(1)
    await expect(saveButton).toBeEnabled()
    // Ctrl+S writes it; the dot goes; Touched has the person's write.
    await page.keyboard.press('Control+s')
    await expect(area.locator('[data-editor-saved]')).toBeVisible({ timeout: 15_000 })
    await expect(area.locator('[data-editor-dirty]')).toHaveCount(0)
    expect(readFileSync(file, 'utf8')).toBe('alpha\nbeta\ngamma\n')
    await pane.locator('[data-files-section="touched"]').click()
    await expect(pane.locator('[data-files-touched] [data-touched-op="write"]').first()).toContainText('editor', { timeout: 15_000 })
    // The agent changes the file; a save from the old read is refused with the words, then Compare, Reload.
    writeFileSync(file, 'alpha\nbeta\ngamma\nfrom the agent\n')
    await api.ok('POST', `/api/sessions/${s.id}/events`, { type: 'file', op: 'edit', path: 'notes.txt', tool: 'Edit' })
    await typeAtEnd('mine\n')
    await page.keyboard.press('Control+s')
    const conflict = area.locator('[data-editor-conflict]')
    await expect(conflict).toContainText('Changed on disk since you opened it', { timeout: 15_000 })
    await expect(conflict).toContainText('(Edit)')
    await expect(conflict).toContainText('Saving would overwrite that.')
    expect(readFileSync(file, 'utf8')).toBe('alpha\nbeta\ngamma\nfrom the agent\n')
    await conflict.locator('[data-editor-compare]').click()
    await expect(area.locator('[data-editor-comparing]')).toBeVisible()
    await expect(area.locator('[data-diff-editor]')).toContainText('from the agent', { timeout: 30_000 })
    await expect(area.locator('[data-diff-editor]')).toContainText('mine')
    await conflict.locator('[data-editor-reload]').click()
    await expect(conflict).toHaveCount(0)
    await expect(lines).toContainText('from the agent', { timeout: 15_000 })
    await expect(lines).not.toContainText('mine')
    await expect(area.locator('[data-editor-dirty]')).toHaveCount(0)
    // Save anyway keeps the person's version over the agent's.
    writeFileSync(file, 'the agent again\n')
    await typeAtEnd('mine again\n')
    await page.keyboard.press('Control+s')
    await expect(conflict).toBeVisible({ timeout: 15_000 })
    await conflict.locator('[data-editor-save-anyway]').click()
    await expect(area.locator('[data-editor-saved]')).toBeVisible({ timeout: 15_000 })
    expect(readFileSync(file, 'utf8')).toContain('mine again')
    // An unsaved tab asks before it closes; Don't save drops the edits, and the file opens again as it is on disk.
    await typeAtEnd('never saved\n')
    await area.locator('[data-editor-tab-active] [data-editor-close]').click()
    const prompt = page.locator('[role="dialog"]', { hasText: 'Save notes.txt?' })
    await expect(prompt).toBeVisible()
    await prompt.locator('[data-editor-discard]').click()
    await expect(area.locator('[data-editor-tab]')).toHaveCount(0)
    expect(readFileSync(file, 'utf8')).not.toContain('never saved')
    await openNotes()
    await expect(lines).not.toContainText('never saved')
    // A view-only guest gets the editor read-only: Read only, no Save.
    const link = await api.ok<{ token: string }>('POST', `/api/sessions/${s.id}/links`, { role: 'view', ttlSeconds: 3600 })
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    await ctx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Jane'))
    const guest = await ctx.newPage()
    await guest.goto(`/join/${link.token}`)
    await guest.getByRole('button', { name: /^Join/ }).first().click()
    const aside = guest.locator('[data-files-aside]')
    await expect(aside.locator(`[data-file-node="${file}"]`)).toBeVisible({ timeout: 30_000 })
    await aside.locator(`[data-file-node="${file}"]`).click()
    const garea = guest.locator('[data-editor-area]')
    await expect(garea.locator('.monaco-editor .view-lines')).toContainText('mine again', { timeout: 30_000 })
    await expect(garea.locator('[data-editor-readonly]')).toBeVisible()
    await expect(garea.locator('[data-editor-save]')).toHaveCount(0)
    await ctx.close()
  } finally {
    await api.stopSession(s.id)
  }
})

// A file in a repository's .git opens read only: Read only (saying why),
// no Save, keys change nothing, and the Neovim keymap says why it keeps
// Monaco's keys; a file of the working tree beside it is edited as before.
test("a file in a repository's .git opens read only; the working tree beside it saves", async ({ page, api, state }) => {
  const cwd = join(state.root, 'files-gitdir')
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'notes.txt'), 'alpha\n')
  git(cwd, 'init', '-q')
  git(cwd, 'add', '-A')
  git(cwd, 'commit', '-q', '-m', 'init')
  const config = join(cwd, '.git', 'config')
  const before = readFileSync(config, 'utf8')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'gitdir', cwd })
  const area = page.locator('[data-editor-area]')
  const lines = area.locator('.monaco-editor .view-lines')
  try {
    // The agent touched both: Touched lists them, and a click opens each.
    await api.ok('POST', `/api/sessions/${s.id}/events`, { type: 'file', op: 'read', path: '.git/config', tool: 'Read' })
    await api.ok('POST', `/api/sessions/${s.id}/events`, { type: 'file', op: 'edit', path: 'notes.txt', tool: 'Edit' })
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    await expect(pane.locator('[data-files-tree] [data-file-node]').first()).toBeVisible({ timeout: 30_000 })
    await pane.locator('[data-files-section="touched"]').click()
    await pane.locator(`[data-files-touched] [data-touched="${config}"]`).click()
    await expect(lines).toContainText('[core]', { timeout: 30_000 })
    const readOnly = area.locator('[data-editor-readonly]')
    await expect(readOnly).toBeVisible()
    await expect(readOnly).toHaveAttribute('title', /repository's \.git/)
    await expect(area.locator('[data-editor-save]')).toHaveCount(0)
    await lines.click()
    await page.keyboard.press('Control+End')
    await page.keyboard.type('[core]\n\tfsmonitor = true\n')
    await page.keyboard.press('Control+s')
    await expect(area.locator('[data-editor-dirty]')).toHaveCount(0)
    await expect(lines).not.toContainText('fsmonitor')
    expect(readFileSync(config, 'utf8')).toBe(before)
    if (hasNvim) {
      // The Neovim keymap does not open it: the note says why, Monaco's keys meanwhile.
      await area.locator('[data-editor-keymap]').click()
      await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'nvim')
      await expect(area.locator('[data-nvim-status]')).toHaveAttribute('data-nvim-mode', 'off')
      await expect(area.locator('[data-editor-keymap-note]')).toContainText("a repository's .git")
      await expect(readOnly).toBeVisible()
      await area.locator('[data-editor-keymap]').click()
      await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'default')
    }
    // The working tree's file beside it: Save, no Read only, and it saves.
    await pane.locator(`[data-files-touched] [data-touched="${join(cwd, 'notes.txt')}"]`).click()
    await expect(lines).toContainText('alpha', { timeout: 30_000 })
    await expect(area.locator('[data-editor-readonly]')).toHaveCount(0)
    await lines.click()
    await page.keyboard.press('Control+End')
    await page.keyboard.type('beta\n')
    await page.keyboard.press('Control+s')
    await expect(area.locator('[data-editor-saved]')).toBeVisible({ timeout: 15_000 })
    expect(readFileSync(join(cwd, 'notes.txt'), 'utf8')).toBe('alpha\nbeta\n')
  } finally {
    await api.stopSession(s.id)
  }
})

/** While window.__holdWrittenMs is set, a FILE frame answering a save as written reaches the page that much later, as through a slow relay. */
async function holdWrittenAnswers(page: Page) {
  await page.addInitScript(() => {
    const Native = window.WebSocket
    class Holding extends Native {
      override set onmessage(fn: ((ev: MessageEvent) => void) | null) {
        super.onmessage = fn && ((ev: MessageEvent) => {
          const hold = (window as unknown as { __holdWrittenMs?: number }).__holdWrittenMs
          if (hold && ev.data instanceof ArrayBuffer) {
            const b = new Uint8Array(ev.data)
            if (b[0] === 0x06 && b.length > 13) {
              const len = new DataView(ev.data).getUint32(9)
              const header = JSON.parse(new TextDecoder().decode(b.subarray(13, 13 + len))) as { kind?: string }
              if (header.kind === 'written') {
                setTimeout(() => fn.call(this, ev), hold)
                return
              }
            }
          }
          fn.call(this, ev)
        })
      }
      override get onmessage(): ((ev: MessageEvent) => void) | null {
        return super.onmessage
      }
    }
    window.WebSocket = Holding
  })
}

// A save whose answer is slow (a relayed one slowed on its way) is not given
// up on: the file is written, the bar says the save goes on, and when the
// answer lands what was sent is saved, on the new hash, so the next save
// goes through. What was typed meanwhile stays, unsaved, and a tab in
// front when the answer lands keeps its own changes. The page holds the
// answer back here, as a slow relay would.
test('a save that answers late is still on its way, then saved', async ({ page, api, state }) => {
  const cwd = join(state.root, 'files-saving-late')
  mkdirSync(cwd, { recursive: true })
  const file = join(cwd, 'notes.txt')
  writeFileSync(file, 'alpha\n')
  const other = join(cwd, 'other.txt')
  writeFileSync(other, 'one\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'saving late', cwd })
  await holdWrittenAnswers(page)
  const area = page.locator('[data-editor-area]')
  const lines = area.locator('.monaco-editor .view-lines')
  const typeAtEnd = async (text: string) => {
    await lines.click()
    await page.keyboard.press('Control+End')
    await page.keyboard.type(text)
  }
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    await pane.locator('[data-files-section="explorer"]').click()
    await pane.locator(`[data-file-node="${file}"]`).click()
    await expect(lines).toBeVisible({ timeout: 30_000 })
    await page.evaluate(() => ((window as unknown as { __holdWrittenMs?: number }).__holdWrittenMs = 22_000))
    await typeAtEnd('beta\n')
    await page.keyboard.press('Control+s')
    // Written at once on the session's machine; the page hears nothing for 22 s.
    await expect.poll(() => readFileSync(file, 'utf8'), { timeout: 15_000 }).toBe('alpha\nbeta\n')
    // After 20 s the bar says the save goes on, and nothing says it failed.
    const slow = area.locator('[data-editor-save-slow]')
    await expect(slow).toBeVisible({ timeout: 25_000 })
    await expect(slow).toContainText('Still saving')
    await expect(area.locator('[data-editor-save-error]')).toHaveCount(0)
    await expect(area.locator('[data-editor-dirty]')).toHaveCount(1)
    // Typed while the answer is on its way.
    await typeAtEnd('more\n')
    // The answer lands: saved, the bar gone; what was typed meanwhile is
    // still there, and still unsaved.
    await expect(area.locator('[data-editor-saved]')).toBeVisible({ timeout: 15_000 })
    await expect(slow).toHaveCount(0)
    await expect(area.locator('[data-editor-save-error]')).toHaveCount(0)
    await page.waitForTimeout(500)
    await expect(lines).toContainText('more')
    await expect(area.locator('[data-editor-dirty]')).toHaveCount(1)
    // The tab took the new hash: the next save is no conflict.
    await page.evaluate(() => ((window as unknown as { __holdWrittenMs?: number }).__holdWrittenMs = 0))
    await page.keyboard.press('Control+s')
    await expect(area.locator('[data-editor-dirty]')).toHaveCount(0, { timeout: 15_000 })
    await expect(area.locator('[data-editor-conflict]')).toHaveCount(0)
    expect(readFileSync(file, 'utf8')).toBe('alpha\nbeta\nmore\n')
    // Another tab with changes of its own is in front when a save's answer
    // lands: its changes stay, unsaved, and the saved tab is saved.
    await pane.locator(`[data-file-node="${other}"]`).click()
    await expect(lines).toContainText('one', { timeout: 30_000 })
    await typeAtEnd('two\n')
    const tabOf = (path: string) => area.locator(`[data-editor-tab]:has(button[title="${path}"])`)
    await expect(tabOf(other).locator('[data-editor-dirty]')).toHaveCount(1)
    await tabOf(file).locator('button').first().click()
    await expect(lines).toContainText('more', { timeout: 15_000 })
    await page.evaluate(() => ((window as unknown as { __holdWrittenMs?: number }).__holdWrittenMs = 3_000))
    await typeAtEnd('gamma\n')
    await page.keyboard.press('Control+s')
    await expect.poll(() => readFileSync(file, 'utf8'), { timeout: 15_000 }).toBe('alpha\nbeta\nmore\ngamma\n')
    await tabOf(other).locator('button').first().click()
    await expect(lines).toContainText('two', { timeout: 15_000 })
    await expect(tabOf(file).locator('[data-editor-dirty]')).toHaveCount(0, { timeout: 15_000 })
    await page.waitForTimeout(500)
    await expect(tabOf(other).locator('[data-editor-dirty]')).toHaveCount(1)
    await expect(lines).toContainText('two')
    expect(readFileSync(other, 'utf8')).toBe('one\n')
    // Saved anyway from the compare view, then back to editing and typing
    // before the answer lands: what was typed stays, unsaved.
    await tabOf(file).locator('button').first().click()
    await expect(lines).toContainText('gamma', { timeout: 15_000 })
    writeFileSync(file, 'outside\n')
    await page.evaluate(() => ((window as unknown as { __holdWrittenMs?: number }).__holdWrittenMs = 0))
    await typeAtEnd('delta\n')
    await page.keyboard.press('Control+s')
    await expect(area.locator('[data-editor-conflict]')).toBeVisible({ timeout: 15_000 })
    await area.locator('[data-editor-compare]').click()
    await expect(area.locator('[data-editor-comparing]')).toBeVisible({ timeout: 15_000 })
    await page.evaluate(() => ((window as unknown as { __holdWrittenMs?: number }).__holdWrittenMs = 3_000))
    await area.locator('[data-editor-save-anyway]').click()
    await area.getByRole('button', { name: 'Back to editing' }).click()
    await typeAtEnd('epsilon\n')
    await expect.poll(() => readFileSync(file, 'utf8'), { timeout: 15_000 }).toBe('alpha\nbeta\nmore\ngamma\ndelta\n')
    await expect(area.locator('[data-editor-saved]')).toBeVisible({ timeout: 15_000 })
    await page.waitForTimeout(500)
    await expect(lines).toContainText('epsilon')
    await expect(tabOf(file).locator('[data-editor-dirty]')).toHaveCount(1)
    // Saved, typed on, and closed with Don't save before the answer lands:
    // what was typed is gone, and the file opens again as it is on disk.
    await typeAtEnd('zeta\n')
    await page.keyboard.press('Control+s')
    await expect.poll(() => readFileSync(file, 'utf8'), { timeout: 15_000 }).toContain('zeta')
    await typeAtEnd('eta\n')
    await tabOf(file).locator('[data-editor-close]').click()
    const prompt = page.locator('[role="dialog"]', { hasText: 'Save notes.txt?' })
    await expect(prompt).toBeVisible()
    await prompt.locator('[data-editor-discard]').click()
    await expect(tabOf(file)).toHaveCount(0)
    await page.waitForTimeout(4_000)
    await pane.locator(`[data-file-node="${file}"]`).click()
    await expect(lines).toContainText('zeta', { timeout: 30_000 })
    await page.waitForTimeout(500)
    await expect(lines).not.toContainText('zetaeta')
    await expect(tabOf(file).locator('[data-editor-dirty]')).toHaveCount(0)
    expect(readFileSync(file, 'utf8')).toBe('alpha\nbeta\nmore\ngamma\ndelta\nepsilon\nzeta\n')
  } finally {
    await api.stopSession(s.id)
  }
})

// Ctrl+S while a save is on its way is not dropped without a word: the bar
// says a save is still on its way, nothing more is sent, and once that save
// is answered the note goes and the next Ctrl+S saves what was typed since.
test('a save asked for while another is on its way says so, and the next one saves', async ({ page, api, state }) => {
  const cwd = join(state.root, 'files-saving-again')
  mkdirSync(cwd, { recursive: true })
  const file = join(cwd, 'again.txt')
  writeFileSync(file, 'one\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'saving again', cwd })
  await holdWrittenAnswers(page)
  const area = page.locator('[data-editor-area]')
  const lines = area.locator('.monaco-editor .view-lines')
  const typeAtEnd = async (text: string) => {
    await lines.click()
    await page.keyboard.press('Control+End')
    await page.keyboard.type(text)
  }
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    await pane.locator('[data-files-section="explorer"]').click()
    await pane.locator(`[data-file-node="${file}"]`).click()
    await expect(lines).toBeVisible({ timeout: 30_000 })
    await page.evaluate(() => ((window as unknown as { __holdWrittenMs?: number }).__holdWrittenMs = 4_000))
    await typeAtEnd('two\n')
    await page.keyboard.press('Control+s')
    await expect.poll(() => readFileSync(file, 'utf8'), { timeout: 15_000 }).toBe('one\ntwo\n')
    // The first save's answer is still held.
    await typeAtEnd('three\n')
    await page.keyboard.press('Control+s')
    const busy = area.locator('[data-editor-save-busy]')
    await expect(busy).toBeVisible()
    await expect(busy).toContainText('still on its way')
    expect(readFileSync(file, 'utf8')).toBe('one\ntwo\n')
    // Answered: the note goes, what was typed since is still unsaved, and the next save saves it.
    await expect(area.locator('[data-editor-saved]')).toBeVisible({ timeout: 15_000 })
    await expect(busy).toHaveCount(0)
    await expect(area.locator('[data-editor-dirty]')).toHaveCount(1)
    await page.evaluate(() => ((window as unknown as { __holdWrittenMs?: number }).__holdWrittenMs = 0))
    await page.keyboard.press('Control+s')
    await expect.poll(() => readFileSync(file, 'utf8'), { timeout: 15_000 }).toBe('one\ntwo\nthree\n')
    await expect(area.locator('[data-editor-dirty]')).toHaveCount(0, { timeout: 15_000 })
  } finally {
    await api.stopSession(s.id)
  }
})
