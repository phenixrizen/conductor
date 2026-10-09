import { execFileSync, spawn } from 'node:child_process'
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
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
    writeFileSync(join(cwd, 'notes.txt'), 'café ok\n')
    await page.reload()
    await openNotes()
    await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'nvim')
    await expect(area.locator('[data-nvim-status]')).toHaveAttribute('data-nvim-mode', /^(n|normal)$/, { timeout: 30_000 })
    // Neovim counts a line's bytes, the editor its UTF-16 units: past the é the cursor is where Neovim says (o is byte 7, column 6).
    await lines.click()
    await page.keyboard.type('0')
    await page.keyboard.type('f')
    await page.keyboard.type('o')
    await expect(area.locator('[data-editor-pos]')).toHaveText(/^Ln 1, Col 6$/, { timeout: 15_000 })
    await area.locator('[data-editor-keymap]').click()
    await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'default')
    await expect(area.locator('[data-nvim-status]')).toHaveCount(0)
  } finally {
    await api.stopSession(s.id)
  }
})

// Round 13, G3: a file another Vim left a swap file for opens read-only
// under the Neovim keymap, with a banner naming the swap file's writer and
// what applies. A Vim that died with unsaved text left this one: Recover
// reads that text in, :w keeps it, and Delete the swap file ends the banner.
test('a swap file left by a Vim that died opens read-only with a banner; Recover, write, Delete the swap file', async ({ page, api, state }) => {
  const cwd = join(state.root, 'files-swap')
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'notes.txt'), 'alpha\n')
  // A Vim with the server's home edits the file, writes its swap file, and dies.
  const swapDir = join(state.home, '.local', 'state', 'nvim', 'swap')
  const vim = spawn('nvim', ['--headless', '-c', 'normal! Ifrom the swap ', '-c', 'preserve', 'notes.txt'], {
    cwd,
    env: { PATH: process.env.PATH ?? '/usr/bin:/bin', HOME: state.home, LANG: 'C.UTF-8' },
    stdio: 'ignore',
  })
  const swapFile = () => (existsSync(swapDir) ? readdirSync(swapDir).find((f) => f.includes('files-swap') && f.endsWith('.swp')) : undefined)
  await expect.poll(swapFile, { timeout: 15_000, message: 'the dying Vim wrote its swap file' }).toBeTruthy()
  await page.waitForTimeout(500)
  vim.kill('SIGKILL')
  await new Promise((r) => vim.once('exit', r))
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'swap', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    await expect(pane.locator('[data-files-tree] [data-file-node]').first()).toBeVisible({ timeout: 30_000 })
    await pane.locator(`[data-file-node="${cwd}/notes.txt"]`).click()
    const area = page.locator('[data-editor-area]')
    await expect(area.locator('.monaco-editor')).toBeVisible({ timeout: 30_000 })
    await area.locator('[data-editor-keymap]').click()
    const banner = area.locator('[data-editor-swap]')
    await expect(banner).toBeVisible({ timeout: 30_000 })
    await expect(banner).toHaveAttribute('data-swap-running', 'false')
    await expect(banner).toContainText('A swap file from a Vim that ended')
    await expect(banner).toContainText('with changes not written')
    await expect(banner.locator('[data-swap-choice]')).toHaveCount(3)
    const lines = area.locator('.monaco-editor .view-lines')
    await expect(lines).toContainText('alpha')
    // Recover reads the dead Vim's text in; the banner says to write, then delete.
    await banner.locator('[data-swap-choice="recover"]').click()
    await expect(lines).toContainText('from the swap alpha', { timeout: 15_000 })
    await expect(banner).toContainText('write the file (:w)')
    await lines.click()
    await page.keyboard.type(':w')
    await page.keyboard.press('Enter')
    await expect.poll(() => readFileSync(join(cwd, 'notes.txt'), 'utf8'), { timeout: 15_000 }).toBe('from the swap alpha\n')
    await banner.locator('[data-swap-choice="delete"]').click()
    await expect(banner).toHaveCount(0)
    await expect.poll(swapFile, { timeout: 15_000, message: 'the swap file is gone' }).toBeFalsy()
    // Neovim's own question shows as buttons: :confirm q over a change not written; Cancel keeps the tab.
    await lines.click()
    await page.keyboard.type('Ax')
    await page.keyboard.press('Escape')
    await page.keyboard.type(':confirm q')
    await page.keyboard.press('Enter')
    const confirm = area.locator('[data-nvim-confirm]')
    await expect(confirm).toContainText('Save changes', { timeout: 15_000 })
    await expect(confirm.locator('[data-nvim-confirm-choice]')).toHaveCount(3)
    await confirm.locator('[data-nvim-confirm-choice="c"]').click()
    await expect(confirm).toHaveCount(0)
    await expect(area.locator('[data-nvim-status]')).toBeVisible()
    await expect(lines).toContainText('from the swap alphax')
  } finally {
    await api.stopSession(s.id)
  }
})

// Round 13, G4: text that comes with no key press reaches Neovim too. A
// dead key's character or dictation arrives as inserted text (Playwright's
// insertText); an input method composes a word and commits it (Chromium's
// Input.imeSetComposition, then the commit): only the committed word is
// typed, not the composition on the way.
test('text with no key press, a dead key\'s character and an input method\'s word, reaches Neovim', async ({ page, api, state }) => {
  const cwd = join(state.root, 'files-ime')
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'notes.txt'), 'end\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'ime', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    await expect(pane.locator('[data-files-tree] [data-file-node]').first()).toBeVisible({ timeout: 30_000 })
    await pane.locator(`[data-file-node="${cwd}/notes.txt"]`).click()
    const area = page.locator('[data-editor-area]')
    await expect(area.locator('.monaco-editor')).toBeVisible({ timeout: 30_000 })
    await area.locator('[data-editor-keymap]').click()
    const status = area.locator('[data-nvim-status]')
    await expect(status).toHaveAttribute('data-nvim-mode', /^(n|normal)$/, { timeout: 30_000 })
    const lines = area.locator('.monaco-editor .view-lines')
    await lines.click()
    await page.keyboard.type('I')
    await expect(status.locator('[data-nvim-mode-words]')).toHaveText('-- INSERT --', { timeout: 15_000 })
    // Typed as fast as a machine sends keys, far past the 20 a quick burst once lost (the chat's bound, before Neovim's own).
    await page.keyboard.type('the quick brown fox jumps over the lazy dog ')
    await expect(lines).toContainText('the quick brown fox jumps over the lazy dog end', { timeout: 15_000 })
    // A dead key's é, or dictation: text with no key press.
    await page.keyboard.insertText('café ')
    await expect(lines).toContainText('dog café end', { timeout: 15_000 })
    // An input method: the composition is shown on the way and only the committed word is typed.
    const cdp = await page.context().newCDPSession(page)
    await cdp.send('Input.imeSetComposition', { text: 'に', selectionStart: 1, selectionEnd: 1 })
    await cdp.send('Input.imeSetComposition', { text: 'にほん', selectionStart: 3, selectionEnd: 3 })
    await cdp.send('Input.insertText', { text: '日本 ' })
    await expect(lines).toContainText('dog café 日本 end', { timeout: 15_000 })
    await expect(lines).not.toContainText('にほん')
    // No "Cannot edit in read-only editor" from Monaco: Neovim took it all.
    await expect(page.getByText('Cannot edit in read-only editor')).toHaveCount(0)
    await page.keyboard.press('Escape')
    await page.keyboard.type(':w')
    await page.keyboard.press('Enter')
    await expect.poll(() => readFileSync(join(cwd, 'notes.txt'), 'utf8'), { timeout: 15_000 }).toBe('the quick brown fox jumps over the lazy dog café 日本 end\n')
  } finally {
    await api.stopSession(s.id)
  }
})

// Round 13, G5: the local echo. Neovim here is slow on purpose (its config,
// in the server's home for this test alone, busy-waits 300 ms on each typed
// character), so what shows at once is the page's guess: plain characters
// in insert mode, marked while Neovim has not handled them, then settled
// with nothing doubled. An autopair (the config maps ( to ()<Left>) is not
// the guess: Neovim's text wins once, and guessing stops in that insert.
test('typing in insert mode shows at once and settles as Neovim has it; an autopair corrects once', async ({ page, api, state }) => {
  const config = join(state.home, '.config', 'nvim')
  mkdirSync(config, { recursive: true })
  writeFileSync(
    join(config, 'init.lua'),
    // A busy wait, as a slow link is: Neovim answers nothing meanwhile (:sleep would let it answer requests mid-key).
    "local uv = vim.uv or vim.loop\nvim.api.nvim_create_autocmd('InsertCharPre', { callback = function() local t = uv.hrtime() while uv.hrtime() - t < 3e8 do end end })\nvim.keymap.set('i', '(', '()<Left>')\n",
  )
  const cwd = join(state.root, 'files-echo')
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'notes.txt'), 'end\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'echo', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    await expect(pane.locator('[data-files-tree] [data-file-node]').first()).toBeVisible({ timeout: 30_000 })
    await pane.locator(`[data-file-node="${cwd}/notes.txt"]`).click()
    const area = page.locator('[data-editor-area]')
    await expect(area.locator('.monaco-editor')).toBeVisible({ timeout: 30_000 })
    await area.locator('[data-editor-keymap]').click()
    const status = area.locator('[data-nvim-status]')
    await expect(status).toHaveAttribute('data-nvim-mode', /^(n|normal)$/, { timeout: 30_000 })
    const lines = area.locator('.monaco-editor .view-lines')
    await lines.click()
    await page.keyboard.type('I')
    await expect(status.locator('[data-nvim-mode-words]')).toHaveText('-- INSERT --', { timeout: 15_000 })
    // Four characters, a space among them: Neovim needs 1.2 s for them; the page shows them at once, marked. The space is guessed
    // like a letter (it comes as <Space>, which held every word after it until Neovim's round trip).
    await page.keyboard.type('ab c')
    await expect(lines).toContainText('ab cend', { timeout: 250 })
    await expect(area.locator('.nvim-guess')).not.toHaveCount(0)
    // Settled: the marks go, nothing is doubled.
    await expect(area.locator('.nvim-guess')).toHaveCount(0, { timeout: 10_000 })
    await expect(lines).toContainText('ab cend')
    await expect(lines).not.toContainText('ab cab c')
    // The autopair: the guess "(" becomes Neovim's "()" once; the next character is Neovim's, between the two.
    await page.keyboard.type('(')
    await expect(lines).toContainText('ab c()end', { timeout: 10_000 })
    await page.keyboard.type('x')
    await expect(lines).toContainText('ab c(x)end', { timeout: 10_000 })
    await expect(area.locator('.nvim-guess')).toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(status.locator('[data-nvim-mode-words]')).toHaveText('', { timeout: 10_000 })
    await page.keyboard.type(':w')
    await page.keyboard.press('Enter')
    await expect.poll(() => readFileSync(join(cwd, 'notes.txt'), 'utf8'), { timeout: 15_000 }).toBe('ab c(x)end\n')
  } finally {
    rmSync(join(config, 'init.lua'), { force: true })
    await api.stopSession(s.id)
  }
})

// A tab whose Neovim buffer holds changes not written keeps its Neovim while
// another tab is in front: back in front, the changes are there and the
// editing goes on (it ended, before, and the next showing met its swap file
// in a banner). The tab wears the unsaved dot and the keymap waits for a save;
// closing it asks: Don't save drops the changes and the swap file with them,
// Save writes through Neovim's :w. Neovim's own :q leaves no swap file.
test('a Neovim tab keeps its changes not written while another is in front; closing asks; nothing leaves a swap file', async ({ page, api, state }) => {
  const cwd = join(state.root, 'files-tabs')
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'a.txt'), 'alpha\n')
  writeFileSync(join(cwd, 'b.txt'), 'beta\n')
  const swapDir = join(state.home, '.local', 'state', 'nvim', 'swap')
  const swaps = () => (existsSync(swapDir) ? readdirSync(swapDir).filter((f) => f.includes('files-tabs')) : [])
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'tabs', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    await expect(pane.locator('[data-files-tree] [data-file-node]').first()).toBeVisible({ timeout: 30_000 })
    const area = page.locator('[data-editor-area]')
    const lines = area.locator('.monaco-editor .view-lines')
    const status = area.locator('[data-nvim-status]')
    const tab = (name: string) => area.locator('[data-editor-tab]', { hasText: name })
    const open = async (name: string, text: string) => {
      await pane.locator(`[data-file-node="${cwd}/${name}"]`).click()
      await expect(lines).toContainText(text, { timeout: 30_000 })
      await expect(status).toHaveAttribute('data-nvim-mode', /^(n|normal)$/, { timeout: 30_000 })
    }
    await pane.locator(`[data-file-node="${cwd}/a.txt"]`).click()
    await expect(area.locator('.monaco-editor')).toBeVisible({ timeout: 30_000 })
    await area.locator('[data-editor-keymap]').click()
    await expect(status).toHaveAttribute('data-nvim-mode', /^(n|normal)$/, { timeout: 30_000 })
    await lines.click()
    await page.keyboard.type('Ixx ')
    await page.keyboard.press('Escape')
    await expect(lines).toContainText('xx alpha')
    // Neovim says the buffer holds a change: the dot, and the keymap waits.
    await expect(tab('a.txt').locator('[data-editor-dirty]')).toBeVisible({ timeout: 15_000 })
    await expect(area.locator('[data-editor-keymap]')).toBeDisabled()
    // Another tab in front, then back: the change is there, no swap file banner, and the editing goes on.
    await open('b.txt', 'beta')
    await expect(tab('a.txt').locator('[data-editor-dirty]')).toBeVisible()
    await tab('a.txt').click()
    await expect(lines).toContainText('xx alpha', { timeout: 15_000 })
    await expect(area.locator('[data-editor-swap]')).toHaveCount(0)
    await lines.click()
    await page.keyboard.type('A!')
    await page.keyboard.press('Escape')
    await expect(lines).toContainText('xx alpha!', { timeout: 15_000 })
    // Closing it asks; Don't save drops the changes, the file is as it was, and its swap file goes.
    const question = page.locator('[role="dialog"]', { hasText: 'Save a.txt?' })
    await area.locator('[data-editor-tab-active] [data-editor-close]').click()
    await expect(question).toBeVisible()
    await question.locator('[data-editor-discard]').click()
    await expect(tab('a.txt')).toHaveCount(0)
    expect(readFileSync(join(cwd, 'a.txt'), 'utf8')).toBe('alpha\n')
    await expect.poll(() => swaps().filter((f) => f.includes('a.txt')), { timeout: 15_000, message: "a.txt's swap file goes with its changes" }).toEqual([])
    await expect(area.locator('[data-editor-keymap]')).toBeEnabled()
    // Open again: no banner, the file as it is.
    await open('a.txt', 'alpha')
    await expect(area.locator('[data-editor-swap]')).toHaveCount(0)
    // Save from the question writes through Neovim and closes the tab.
    await lines.click()
    await page.keyboard.type('Ayy')
    await page.keyboard.press('Escape')
    await expect(tab('a.txt').locator('[data-editor-dirty]')).toBeVisible({ timeout: 15_000 })
    await area.locator('[data-editor-tab-active] [data-editor-close]').click()
    await question.locator('[data-editor-close-save]').click()
    await expect(tab('a.txt')).toHaveCount(0, { timeout: 15_000 })
    expect(readFileSync(join(cwd, 'a.txt'), 'utf8')).toBe('alphayy\n')
    // Neovim's own :q on b.txt: the tab closes and no swap file is left.
    await tab('b.txt').click()
    await expect(lines).toContainText('beta', { timeout: 15_000 })
    await expect(status).toHaveAttribute('data-nvim-mode', /^(n|normal)$/, { timeout: 30_000 })
    await lines.click()
    await page.keyboard.type(':q')
    await page.keyboard.press('Enter')
    await expect(area.locator('[data-editor-tab]')).toHaveCount(0, { timeout: 15_000 })
    await expect.poll(swaps, { timeout: 15_000, message: 'no swap file left behind' }).toEqual([])
  } finally {
    await api.stopSession(s.id)
  }
})
