import { expect, test, type Session } from './fixtures'

// Renaming a server session (#318): from a sidebar row's More menu, from the palette's row (the same descriptors, #302) and from
// the session page's menu, through the one dialog. The name shows at once wherever the session does (the events stream), a name
// with nothing left or past 120 bytes is not sent, a rename made elsewhere reaches an open page, and a resume starts under the
// new name.
test.describe.configure({ mode: 'serial' })

const sessions: string[] = []

test.afterAll(async ({ api }) => {
  for (const id of sessions) await api.stopSession(id)
})

async function launch(api: { ok: <T>(method: string, path: string, body?: unknown) => Promise<T> }, name: string): Promise<Session> {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name })
  sessions.push(s.id)
  return s
}

test('a sidebar row renames its session through the dialog, which sends only a name the server keeps whole', async ({ page, api }) => {
  const s = await launch(api, 'e2e rename row')
  await page.goto('/crews')
  const row = page.locator(`[data-session-list] [data-sidebar-row="s:${s.id}"]`).first()
  await expect(row).toContainText('e2e rename row')
  await row.hover()
  await row.locator('[data-row-more]').click()
  await page.getByRole('menuitem', { name: /^Rename/ }).click()
  const dialog = page.getByRole('dialog')
  const field = dialog.getByRole('textbox', { name: 'Session name' })
  const save = dialog.locator('[data-rename-save]')
  await expect(field).toHaveValue('e2e rename row')
  // Nothing left once the space is trimmed: not sent.
  await field.fill('   ')
  await expect(save).toBeDisabled()
  // Forty-one three-byte characters, 123 bytes: the dialog asks for a shorter name rather than send one the server would cut.
  await field.fill('名'.repeat(41))
  await expect(dialog).toContainText('Too long')
  await expect(save).toBeDisabled()
  await field.fill('  e2e renamed row  ')
  await save.click()
  await expect(dialog).toHaveCount(0)
  // The row shows the name the server kept, trimmed, from the events stream.
  await expect(row).toContainText('e2e renamed row')
  expect((await api.session(s.id)).name).toBe('e2e renamed row')
})

test("the palette's row offers the same Rename…", async ({ page, api }) => {
  const s = await launch(api, 'e2e rename palette')
  await page.goto('/agents')
  await expect(page.locator('[data-session-list]')).toBeVisible({ timeout: 30_000 })
  await page.keyboard.press('Control+k')
  const palette = page.locator('[data-palette]')
  await expect(palette).toBeVisible()
  await palette.locator('input[data-palette-input]').fill('e2e rename palette')
  const row = palette.locator(`[data-palette-row="session:${s.id}"]`)
  await expect(row).toBeVisible()
  await page.keyboard.press('ArrowDown')
  await expect(row).toHaveAttribute('data-palette-focused', '')
  await page.keyboard.press('ArrowRight')
  await row.locator('[data-palette-action="rename"]').click()
  await expect(palette).toBeHidden()
  const dialog = page.getByRole('dialog')
  const field = dialog.getByRole('textbox', { name: 'Session name' })
  await expect(field).toHaveValue('e2e rename palette')
  await field.fill('e2e renamed palette')
  await field.press('Enter')
  await expect(dialog).toHaveCount(0)
  await expect(page.locator(`[data-session-list] [data-sidebar-row="s:${s.id}"]`).first()).toContainText('e2e renamed palette')
  expect((await api.session(s.id)).name).toBe('e2e renamed palette')
})

test("the session page renames its session from its menu; its header, its tab's title and its row follow", async ({ page, api }) => {
  const s = await launch(api, 'e2e rename page')
  await page.goto(`/sessions/${s.id}`)
  const title = page.locator('[data-session-title]')
  await expect(title).toHaveText('e2e rename page')
  await page.getByRole('button', { name: 'More', exact: true }).click()
  await page.getByRole('menuitem', { name: /^Rename/ }).click()
  const dialog = page.getByRole('dialog')
  const field = dialog.getByRole('textbox', { name: 'Session name' })
  await expect(field).toHaveValue('e2e rename page')
  await field.fill('e2e renamed page')
  await field.press('Enter')
  await expect(dialog).toHaveCount(0)
  await expect(title).toHaveText('e2e renamed page')
  await expect(page).toHaveTitle(/e2e renamed page/)
  await expect(page.locator(`[data-session-list] [data-sidebar-row="s:${s.id}"]`).first()).toContainText('e2e renamed page')
})

test('a rename made elsewhere reaches an open page, and a resume starts under the new name', async ({ page, api }) => {
  const s = await launch(api, 'e2e rename resume')
  await page.goto(`/sessions/${s.id}`)
  const title = page.locator('[data-session-title]')
  await expect(title).toHaveText('e2e rename resume')
  // Another workbench renames it: this page hears it on the events stream.
  const renamed = await api.ok<Session>('PATCH', `/api/sessions/${s.id}`, { name: 'e2e resumed under it' })
  expect(renamed.name).toBe('e2e resumed under it')
  await expect(title).toHaveText('e2e resumed under it')
  await api.stopSession(s.id)
  await expect.poll(async () => (await api.session(s.id)).status, { timeout: 15_000 }).toMatch(/exited|stopped/)
  await page.locator('[data-resume][data-resume-kind="label"]').first().click()
  await page.waitForURL((u) => u.pathname.startsWith('/sessions/') && !u.pathname.endsWith(s.id), { timeout: 15_000 })
  const nextId = decodeURIComponent(new URL(page.url()).pathname.split('/').pop() ?? '')
  sessions.push(nextId)
  const next = await api.session(nextId)
  expect(next.name).toBe('e2e resumed under it')
  expect(next.resumedFrom).toBe(s.id)
  await expect(title).toHaveText('e2e resumed under it')
})
