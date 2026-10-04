import { expect, test, type Session } from './fixtures'

// Share is one click: opening the dialog makes a view link, copies it and
// says where it reaches; opening it again on the same session shows the
// same link rather than minting another. The e2e server publishes nowhere
// (CONDUCTOR_RENDEZVOUS=0) and has reach off, so the link is local.
test('Share makes a link in one click, says where it reaches, and keeps it for the next open', async ({ page, api }) => {
  const session = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'share me' })
  await page.goto(`/sessions/${session.id}`)
  await page.getByRole('button', { name: 'Share', exact: true }).first().click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.locator('[data-created-url]')).toBeVisible({ timeout: 15_000 })
  await expect(dialog.locator('[data-link-reach]')).toHaveAttribute('data-link-reach', 'local')
  await expect(dialog.locator('[data-share-link]')).toHaveCount(1)
  await expect(dialog.locator('[data-share-link]').first()).toContainText('View')
  const url = await dialog.locator('[data-created-url]').getAttribute('title')
  expect(url).toMatch(/\/join\//)
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await page.getByRole('button', { name: 'Share', exact: true }).first().click()
  await expect(page.getByRole('dialog').locator('[data-created-url]')).toHaveAttribute('title', url!)
  await expect(page.getByRole('dialog').locator('[data-share-link]')).toHaveCount(1)
  // Another link, with control, from the secondary form.
  await page.getByRole('dialog').getByText('Another link').click()
  await page.getByRole('dialog').getByRole('radio', { name: /Control/ }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Create link' }).click()
  await expect(page.getByRole('dialog').locator('[data-share-link]')).toHaveCount(2)
})
