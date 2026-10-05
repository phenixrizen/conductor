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
