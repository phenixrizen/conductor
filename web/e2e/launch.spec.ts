import { expect, test, type Session } from './fixtures'

const sessions: string[] = []

// What a test launched stops as it ends: a session left running stays on the Yard's wall for the rest of the suite, where it narrows
// the tiles later specs measure (#219).
test.afterEach(async ({ api }) => {
  for (const id of sessions.splice(0)) await api.stopSession(id)
})

// The launch dialog asks where the agent runs only when there are two
// answers. The e2e workbench is opened at 127.0.0.1, this computer, so the
// question is absent and Launch starts the agent on the server at once.
test('on a workbench at this computer the launch dialog has one answer and launches', async ({ page, api }) => {
  await page.goto('/')
  await page.getByRole('button', { name: 'Launch agent' }).first().click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('button', { name: 'Launch' })).toBeVisible()
  await expect(dialog.locator('[data-runs-on]')).toHaveCount(0)
  await expect(dialog.getByText('My machine')).toHaveCount(0)
  const list = async () => {
    const r = await api.ok<{ sessions?: Session[] } | Session[]>('GET', '/api/sessions')
    return Array.isArray(r) ? r : (r.sessions ?? [])
  }
  const before = new Set((await list()).map((s) => s.id))
  // The session the launch made is held for the teardown from its answer, before anything else here can fail.
  const created = page.waitForResponse((r) => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/sessions')
  await dialog.getByRole('button', { name: 'Launch' }).click()
  const answer = await created
  const launched = answer.ok() ? ((await answer.json()) as Session).id : ''
  if (launched) sessions.push(launched)
  expect(answer.status(), 'the launch made a session').toBe(201)
  await expect.poll(async () => (await list()).map((s) => s.id).filter((id) => !before.has(id)), { timeout: 15_000 }).toEqual([launched])
})

// The dialog's first open on a page says it is checking while the catalog is
// under way, never a blank grid or "no agents" (round 14: on the Windows
// app's first open the list stayed empty for a long while); opened again it
// lists the last catalog at once and refreshes it behind. The catalog's reply
// is held back three seconds here to stand for a slow first lookup.
test('the launch dialog says it is checking on its first open, and lists at once when opened again', async ({ page }) => {
  await page.route('**/api/catalog', async (route) => {
    await new Promise((r) => setTimeout(r, 3000))
    await route.continue()
  })
  await page.goto('/')
  await page.getByRole('button', { name: 'Launch agent' }).first().click()
  const dialog = page.getByRole('dialog')
  const radios = dialog.getByRole('radiogroup', { name: 'Agent' }).getByRole('radio')
  await expect(dialog.locator('[data-agents-checking]')).toBeVisible({ timeout: 1000 })
  await expect(dialog.locator('[data-none-available]')).toHaveCount(0)
  await expect(radios.first()).toBeVisible({ timeout: 10_000 })
  await expect(dialog.locator('[data-agents-checking]')).toHaveCount(0)
  const listed = await radios.count()
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  // Again: the list at once, though the refresh behind it takes three seconds.
  await page.getByRole('button', { name: 'Launch agent' }).first().click()
  await expect(radios).toHaveCount(listed, { timeout: 500 })
  await expect(dialog.locator('[data-agents-checking]')).toHaveCount(0)
})
