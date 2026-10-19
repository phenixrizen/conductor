import { expect, test, type Session } from './fixtures'

// Share is one click: opening the dialog makes a view link, copies it and
// says where it reaches; opening it again on the same session shows the
// same link rather than minting another. The e2e server publishes nowhere
// (CONDUCTOR_RENDEZVOUS=0) and has reach off, so the link is local.
const sessions: string[] = []

// Its session stops as the test ends: one left running stays on the Yard's wall for the rest of the suite, where it narrows the
// tiles later specs measure (#219).
test.afterEach(async ({ api }) => {
  for (const id of sessions.splice(0)) await api.stopSession(id)
})

test('Share makes a link in one click, says where it reaches, and keeps it for the next open', async ({ page, api }) => {
  const session = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'share me' })
  sessions.push(session.id)
  await page.goto(`/sessions/${session.id}`)
  await page.getByRole('button', { name: 'Share', exact: true }).first().click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.locator('[data-created-url]')).toBeVisible({ timeout: 15_000 })
  await expect(dialog.locator('[data-link-reach]')).toHaveAttribute('data-link-reach', 'local')
  await expect(dialog.locator('[data-share-link]')).toHaveCount(1)
  await expect(dialog.locator('[data-share-link]').first()).toContainText('View')
  const url = await dialog.locator('[data-created-url]').getAttribute('title')
  expect(url).toMatch(/\/join\//)
  // The link as a QR code for a phone (#144), labelled with the link's text, drawn module by module.
  const qr = dialog.locator('[data-share-qr]')
  await expect(qr).toHaveAttribute('aria-label', url!)
  expect(((await qr.locator('path').getAttribute('d')) ?? '').split('h1v1h-1z').length, 'modules drawn').toBeGreaterThan(200)
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await page.getByRole('button', { name: 'Share', exact: true }).first().click()
  await expect(page.getByRole('dialog').locator('[data-created-url]')).toHaveAttribute('title', url!)
  await expect(page.getByRole('dialog').locator('[data-share-link]')).toHaveCount(1)
  // Control on the link itself: a control link replaces the view one nobody used, and the next Share makes control.
  await page.getByRole('dialog').locator('[data-share-role-option="control"]').click()
  await expect(page.getByRole('dialog').locator('[data-share-role-option="control"]')).toHaveAttribute('aria-checked', 'true')
  await expect(page.getByRole('dialog').locator('[data-share-link]')).toHaveCount(1)
  await expect(page.getByRole('dialog').locator('[data-share-link]').first()).toContainText('Control')
  expect(await page.evaluate(() => localStorage.getItem('conductor.share.role'))).toBe('control')
  await page.getByRole('dialog').locator('[data-share-role-option="view"]').click()
  await expect(page.getByRole('dialog').locator('[data-share-link]').first()).toContainText('View')
  // Another link, with control, from the secondary form.
  await page.getByRole('dialog').getByText('Another link').click()
  await page.getByRole('dialog').locator('[data-another-link]').getByRole('radio', { name: /Control/ }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Create link' }).click()
  await expect(page.getByRole('dialog').locator('[data-share-link]')).toHaveCount(2)
})
