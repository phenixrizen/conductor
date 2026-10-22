import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The command palette (#302): Ctrl+K opens it over the page and Escape
// closes it; what needs you is the first row and ↓ then a digit answers it
// without leaving the page (a digit typed into the input is a search); the
// page's own group comes first on a session page and is absent on a page
// with nothing of its own; a verb typed reads as one row that asks before it
// stops; # lists the saved crews, Enter launches one and Launch named passes
// the label; :path:line opens the editor at the line; / opens the palette
// and the sidebar's filter box still filters the list.
test.describe.configure({ mode: 'serial' })

const sessions: string[] = []
const runs: string[] = []
let crewId = ''

test.afterAll(async ({ api }) => {
  for (const id of runs) await api.stopRun(id)
  for (const id of sessions) await api.stopSession(id)
  if (crewId) await api.call('DELETE', `/api/crews/${encodeURIComponent(crewId)}`)
})

function transcript(home: string, s: Session): string {
  const file = join(home, '.stub-sessions', `${s.agentSession?.id ?? ''}.txt`)
  return existsSync(file) ? readFileSync(file, 'utf8') : ''
}

test("Ctrl+K opens the palette and Escape closes it; the page's own group comes first, named after the page", async ({ page, api }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e palette page' })
  sessions.push(s.id)
  await page.goto('/agents')
  await expect(page.locator('[data-session-list]')).toBeVisible({ timeout: 30_000 })
  await page.keyboard.press('Control+k')
  const palette = page.locator('[data-palette]')
  await expect(palette).toBeVisible()
  await expect(palette.locator('input[data-palette-input]')).toBeFocused()
  await expect(palette.locator('[data-palette-group]').first()).toHaveAttribute('data-palette-group', 'page')
  await expect(palette.locator('[data-palette-group="page"] h3')).toContainText('Agents')
  await expect(palette.locator('[data-palette-group="goto"]')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(palette).toBeHidden()
  // On a session page the terminal has the focus: Alt+/ opens the palette from it, and the page's group leads with its name.
  await page.goto(`/sessions/${s.id}`)
  await expect(page.locator('.terminal-host .xterm-helper-textarea').first()).toBeFocused({ timeout: 30_000 })
  await page.keyboard.press('Alt+/')
  await expect(palette).toBeVisible()
  await expect(palette.locator('[data-palette-group]').first()).toHaveAttribute('data-palette-group', 'page')
  await expect(palette.locator('[data-palette-group="page"] h3')).toContainText('e2e palette page')
  await expect(palette.locator('[data-palette-row="page:stop"]')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(palette).toBeHidden()
})

test('a session that needs you is the first row; ↓ then 1 answers it from any page, a digit typed into the input does not', async ({ page, api, state }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'asker', name: 'e2e palette asks' })
  sessions.push(s.id)
  await expect.poll(async () => (await api.session(s.id)).attention?.state, { timeout: 30_000 }).toBe('needs_input')
  await page.goto('/agents')
  await expect(page.locator('[data-session-list]')).toBeVisible({ timeout: 30_000 })
  await page.keyboard.press('Control+k')
  const palette = page.locator('[data-palette]')
  const first = palette.locator('[data-palette-list] [data-palette-row]').first()
  await expect(first).toHaveAttribute('data-palette-row', `session:${s.id}`)
  await expect(first).toHaveAttribute('data-palette-state', 'needs')
  await expect(first.locator('[data-palette-detail]')).toHaveText('Which database?')
  await expect(first.locator('[data-palette-choice="3"]')).toContainText('Keep both')
  // A digit in the input is a search, never an answer.
  await page.keyboard.type('2')
  await expect(palette.locator('input[data-palette-input]')).toHaveValue('2')
  expect(transcript(state.home, s)).not.toContain('SQLite')
  await page.keyboard.press('Backspace')
  await page.keyboard.press('ArrowDown')
  await expect(first).toHaveAttribute('data-palette-focused', '')
  await page.keyboard.press('1')
  await expect.poll(() => transcript(state.home, s), { timeout: 15_000, message: 'the choice was typed' }).toContain('Postgres\n')
  await expect(palette.locator(`[data-palette-group="needs"] [data-palette-row="session:${s.id}"]`)).toHaveCount(0, { timeout: 15_000 })
  await expect(page).toHaveURL(/\/agents$/)
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await expect(palette).toBeHidden()
})

test('a verb typed is one row: stop asks in the row, Escape cancels, Enter stops', async ({ page, api }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e palette stops' })
  sessions.push(s.id)
  await page.goto('/agents')
  await expect(page.locator('[data-session-list]')).toBeVisible({ timeout: 30_000 })
  await page.keyboard.press('Control+k')
  const palette = page.locator('[data-palette]')
  await palette.locator('input[data-palette-input]').fill('> stop e2e palette stops')
  const row = palette.locator(`[data-palette-row="verb:stop:${s.id}"]`)
  await expect(row).toBeVisible()
  await expect(palette.locator('[data-palette-group]').first()).toHaveAttribute('data-palette-group', 'verb')
  await page.keyboard.press('ArrowDown')
  await expect(row).toHaveAttribute('data-palette-focused', '')
  await page.keyboard.press('Enter')
  await expect(row.locator('[data-palette-ask]')).toContainText('Stop e2e palette stops?')
  expect((await api.session(s.id)).status).not.toBe('stopped')
  await page.keyboard.press('Escape')
  await expect(row.locator('[data-palette-ask]')).toHaveCount(0)
  await expect(row).toHaveAttribute('data-palette-focused', '')
  await page.keyboard.press('Enter')
  await expect(row.locator('[data-palette-confirm]')).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(palette).toBeHidden()
  await expect.poll(async () => (await api.session(s.id)).status, { timeout: 15_000 }).toMatch(/stopped|exited/)
  // Exited folds to one row; Enter on it unfolds the exited sessions and keeps the palette open (Codex's round on #302).
  await page.keyboard.press('Control+k')
  const fold = palette.locator('[data-palette-row="exited:unfold"]')
  await expect(fold).toBeVisible()
  await fold.click()
  await expect(palette).toBeVisible()
  await expect(palette.locator(`[data-palette-group="exited"] [data-palette-row="session:${s.id}"]`)).toBeVisible()
  await page.keyboard.press('Escape')
  await page.keyboard.press('Escape')
  await expect(palette).toBeHidden()
})

test('# lists the saved crews: Enter launches one, Launch named passes the label', async ({ page, api }) => {
  crewId = (
    await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
      name: 'e2e palette crew',
      goal: 'launched from the palette',
      cwd: '',
      where: 'server',
      isolation: 'none',
      openAfterLaunch: false,
      yolo: false,
      members: [{ name: 'lead', agentId: 'claude', prompt: 'say hello', start: { when: 'immediately' } }],
    })
  ).crew.id
  await page.goto('/agents')
  await expect(page.locator('[data-session-list]')).toBeVisible({ timeout: 30_000 })
  await page.keyboard.press('Control+k')
  const palette = page.locator('[data-palette]')
  await palette.locator('input[data-palette-input]').fill('# e2e palette crew')
  await expect(palette.locator('[data-palette-chip]')).toHaveText('Runs and crews')
  const row = palette.locator(`[data-palette-row="crew:${crewId}"]`)
  await expect(row).toBeVisible({ timeout: 15_000 })
  await expect(row.locator('[data-palette-detail]')).toContainText('launched from the palette')
  await page.keyboard.press('ArrowDown')
  await expect(row).toHaveAttribute('data-palette-focused', '')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/runs\//, { timeout: 15_000 })
  runs.push(decodeURIComponent(new URL(page.url()).pathname.split('/').pop() ?? ''))
  // Launch named: under →, the field takes the name; Enter in it launches with the label.
  await page.goto('/agents')
  await expect(page.locator('[data-session-list]')).toBeVisible({ timeout: 30_000 })
  await page.keyboard.press('Control+k')
  await palette.locator('input[data-palette-input]').fill('# e2e palette crew')
  await expect(row).toBeVisible({ timeout: 15_000 })
  // The run just launched carries the crew's name and lists first under #: J steps down to the crew's row.
  await page.keyboard.press('ArrowDown')
  for (let i = 0; i < 6 && !(await row.getAttribute('data-palette-focused') === ''); i++) await page.keyboard.press('j')
  await expect(row).toHaveAttribute('data-palette-focused', '')
  await page.keyboard.press('ArrowRight')
  await expect(row.locator('[data-palette-actions] [data-palette-action="field"]')).toBeVisible()
  await page.keyboard.press('l')
  const field = row.locator('[data-palette-field] input')
  await expect(field).toBeFocused()
  await field.fill('e2e palette named')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/runs\//, { timeout: 15_000 })
  runs.push(decodeURIComponent(new URL(page.url()).pathname.split('/').pop() ?? ''))
  await expect(page.locator('[data-crew-run-header] [data-run-subtitle]')).toHaveText('e2e palette named', { timeout: 15_000 })
})

test(':path:line opens the file in the editor at the line; / opens the palette and the filter box still filters the sidebar', async ({ page, api, state }) => {
  const cwd = join(state.root, 'palette-files')
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'notes.txt'), 'alpha\nbeta\ngamma\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e palette files', cwd })
  sessions.push(s.id)
  await page.goto(`/sessions/${s.id}`)
  await expect(page.locator('.terminal-host .xterm-helper-textarea').first()).toBeFocused({ timeout: 30_000 })
  await page.keyboard.press('Alt+/')
  const palette = page.locator('[data-palette]')
  await palette.locator('input[data-palette-input]').fill(':notes.txt:2')
  await expect(palette.locator('[data-palette-chip]')).toHaveText('Files')
  const open = palette.locator('[data-palette-group="open"] [data-palette-row]').first()
  await expect(open).toContainText('Open notes.txt at line 2')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await expect(palette).toBeHidden()
  const area = page.locator('[data-editor-area]')
  await expect(area).toHaveAttribute('data-editor-area', 'open', { timeout: 30_000 })
  await expect(area.locator('[data-editor-crumbs]')).toContainText('notes.txt')
  await expect(area.locator('[data-editor-pos]')).toHaveText('Ln 2, Col 1', { timeout: 15_000 })
  // / from the page (not the terminal) opens the palette; the sidebar's filter box is a plain filter of the list.
  await page.goto('/agents')
  await expect(page.locator('[data-session-list]')).toBeVisible({ timeout: 30_000 })
  await page.keyboard.press('/')
  await expect(palette).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(palette).toBeHidden()
  const filter = page.getByPlaceholder('Filter sessions, runs, people')
  await filter.click()
  await filter.fill('e2e palette files')
  await expect(page.locator('[data-session-list="sidebar"] [data-sidebar-row^="s:"]')).toHaveCount(1)
  await expect(page.locator(`[data-session-list="sidebar"] [data-sidebar-row="s:${s.id}"]`)).toBeVisible()
  await expect(palette).toBeHidden()
})

test("the editor's own rows (#321): Save writes from the palette, Close file asks on a dirty tab, Keys → Neovim waits for the save", async ({ page, api, state }) => {
  const cwd = join(state.root, 'palette-editor')
  mkdirSync(cwd, { recursive: true })
  const file = join(cwd, 'notes.txt')
  writeFileSync(file, 'alpha\nbeta\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e palette editor', cwd })
  sessions.push(s.id)
  await page.goto(`/sessions/${s.id}`)
  await expect(page.locator('.terminal-host .xterm-helper-textarea').first()).toBeFocused({ timeout: 30_000 })
  const palette = page.locator('[data-palette]')
  const area = page.locator('[data-editor-area]')
  const lines = area.locator('.monaco-editor .view-lines')
  const row = (key: string) => palette.locator(`[data-palette-group="page"] [data-palette-row="${key}"]`)
  // No file open: none of the rows.
  await page.keyboard.press('Alt+/')
  await expect(palette).toBeVisible()
  await expect(palette.locator('[data-palette-group="page"]')).toBeVisible()
  await expect(row('page:save')).toHaveCount(0)
  await palette.locator('input[data-palette-input]').fill(':notes.txt')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await expect(area).toHaveAttribute('data-editor-area', 'open', { timeout: 30_000 })
  await expect(lines).toContainText('alpha', { timeout: 30_000 })
  // The four rows, with the file open and clean: Save and Ask the agent say why they wait.
  await page.keyboard.press('Alt+/')
  await expect(row('page:keys')).toBeVisible()
  await expect(row('page:close-file')).toBeVisible()
  await expect(row('page:save')).toHaveAttribute('aria-disabled', 'true')
  await expect(row('page:save')).toContainText('Nothing to save')
  await expect(row('page:ask-selection')).toHaveAttribute('aria-disabled', 'true')
  await expect(row('page:ask-selection')).toContainText('Select some lines first')
  await page.keyboard.press('Escape')
  await expect(palette).toBeHidden()
  // A change: Save from the palette writes it.
  await lines.click()
  await page.keyboard.press('Control+End')
  await page.keyboard.type('gamma\n')
  await expect(area.locator('[data-editor-dirty]')).toHaveCount(1)
  await page.keyboard.press('Alt+/')
  await expect(row('page:save')).not.toHaveAttribute('aria-disabled', 'true')
  // Keys → Neovim waits for the save, with the keymap button's own words.
  await expect(row('page:keys')).toHaveAttribute('aria-disabled', 'true')
  await expect(row('page:keys')).toContainText('Save or discard the changes first')
  await row('page:save').click()
  await expect(palette).toBeHidden()
  await expect(area.locator('[data-editor-saved]')).toBeVisible({ timeout: 15_000 })
  await expect(area.locator('[data-editor-dirty]')).toHaveCount(0)
  expect(readFileSync(file, 'utf8')).toBe('alpha\nbeta\ngamma\n')
  // Dirty again and folded: the editor's text is not at hand, so Save waits for it (a save then would have written the text as
  // loaded and lost the edit: Codex's round); brought back, Save from the palette writes the edit. After a row runs, the next
  // chord waits for the palette to be gone: pressed while it fades out, the chord is swallowed (#323).
  await lines.click()
  await page.keyboard.press('Control+End')
  await page.keyboard.type('delta\n')
  await expect(area.locator('[data-editor-dirty]')).toHaveCount(1)
  await page.keyboard.press('Alt+/')
  await row('page:fold').click()
  await expect(palette).toBeHidden()
  await expect(area).toHaveAttribute('data-editor-area', 'folded')
  await page.keyboard.press('Alt+/')
  await expect(row('page:save')).toHaveAttribute('aria-disabled', 'true')
  await expect(row('page:save')).toContainText('Bring the editor back first')
  await row('page:fold').click()
  await expect(palette).toBeHidden()
  await expect(area).toHaveAttribute('data-editor-area', 'open')
  await expect(lines).toContainText('delta', { timeout: 15_000 })
  await page.keyboard.press('Alt+/')
  await expect(row('page:save')).not.toHaveAttribute('aria-disabled', 'true')
  await row('page:save').click()
  await expect(palette).toBeHidden()
  await expect(area.locator('[data-editor-saved]')).toBeVisible({ timeout: 15_000 })
  await expect(area.locator('[data-editor-dirty]')).toHaveCount(0, { timeout: 15_000 })
  expect(readFileSync(file, 'utf8')).toBe('alpha\nbeta\ngamma\ndelta\n')
  // Dirty once more: Close file asks, as Ctrl+W does; Cancel keeps the tab.
  await lines.click()
  await page.keyboard.press('Control+End')
  await page.keyboard.type('epsilon\n')
  await expect(area.locator('[data-editor-dirty]')).toHaveCount(1)
  await page.keyboard.press('Alt+/')
  await row('page:close-file').click()
  await expect(palette).toBeHidden()
  const prompt = page.locator('[role="dialog"]', { hasText: 'Save notes.txt?' })
  await expect(prompt).toBeVisible()
  await prompt.getByRole('button', { name: 'Cancel' }).click()
  await expect(prompt).toBeHidden()
  await expect(area.locator('[data-editor-crumbs]')).toContainText('notes.txt')
  // Saved (Ctrl+S from the editor), the keymap row is live again; where Neovim is offered, Enter switches the keymap and back.
  await lines.click()
  await page.keyboard.press('Control+s')
  await expect(area.locator('[data-editor-dirty]')).toHaveCount(0, { timeout: 15_000 })
  expect(readFileSync(file, 'utf8')).toBe('alpha\nbeta\ngamma\ndelta\nepsilon\n')
  await page.keyboard.press('Alt+/')
  const offered = (await row('page:keys').getAttribute('aria-disabled')) !== 'true'
  if (offered) {
    await row('page:keys').click()
    await expect(palette).toBeHidden()
    await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'nvim', { timeout: 15_000 })
    // The real Neovim is up and holds the file (as vim.spec waits for it); then the row reads the other way and switches back.
    await expect(area.locator('[data-nvim-status]')).toHaveAttribute('data-nvim-mode', /^(n|normal)$/, { timeout: 30_000 })
    await page.keyboard.press('Alt+/')
    await expect(row('page:keys')).toContainText('Keys → default')
    await expect(row('page:keys')).not.toHaveAttribute('aria-disabled', 'true', { timeout: 15_000 })
    await row('page:keys').click()
    await expect(palette).toBeHidden()
    await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'default', { timeout: 15_000 })
    await expect(area.locator('[data-nvim-status]')).toHaveCount(0, { timeout: 15_000 })
  } else {
    await expect(row('page:keys')).toContainText('Neovim')
    await page.keyboard.press('Escape')
  }
})
