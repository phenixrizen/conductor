import { expect, test } from './fixtures'

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
  const count = async () => {
    const r = await api.ok<{ sessions?: unknown[] } | unknown[]>('GET', '/api/sessions')
    return (Array.isArray(r) ? r : (r.sessions ?? [])).length
  }
  const before = await count()
  await dialog.getByRole('button', { name: 'Launch' }).click()
  await expect.poll(count, { timeout: 15_000 }).toBe(before + 1)
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
