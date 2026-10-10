import { expect, test, type Session } from './fixtures'

// A share link with an expiry works until it expires: a guest on it is
// disconnected as it expires, and the page says the link expired (the
// close a revoke makes, with its own words), with nothing reconnecting on
// its own; opened again, the join page says the link cannot be used.
const sessions: string[] = []

test.afterAll(async ({ api }) => {
  for (const id of sessions) await api.stopSession(id)
})

test('a guest on a link that expires is disconnected as it expires, and the page says the link expired', async ({ api, browser }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'expiring link' })
  sessions.push(s.id)
  // Long enough for the page to load and join, short enough to wait for.
  const { token, link } = await api.ok<{ token: string; link: { expiresAt: string } }>('POST', `/api/sessions/${s.id}/links`, { role: 'view', ttlSeconds: 15 })
  const expiresAt = Date.parse(link.expiresAt)

  const guestCtx = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Priya'))
  const guest = await guestCtx.newPage()
  await guest.goto(`/join/${token}`)
  await guest.getByRole('button', { name: 'Join session' }).click()
  const badge = guest.locator('[data-transport-state]').first()
  await expect(badge).toHaveAttribute('data-transport-state', 'open', { timeout: 15_000 })
  expect(Date.now()).toBeLessThan(expiresAt)

  // At the expiry the server closes the connection; the terminal's overlay names why, with the error's code below it.
  const overlay = guest.locator('p').filter({ hasText: /^link expired$/ })
  await expect(overlay).toBeVisible({ timeout: expiresAt - Date.now() + 10_000 })
  expect(Date.now()).toBeGreaterThanOrEqual(expiresAt)
  await expect(guest.locator('p').filter({ hasText: /^expired$/ })).toBeVisible()
  await expect(badge).toHaveAttribute('data-transport-state', 'closed')
  // Nothing tries again on its own: still closed a moment later.
  await guest.waitForTimeout(1_500)
  await expect(badge).toHaveAttribute('data-transport-state', 'closed')
  await expect(overlay).toBeVisible()

  // Opened again, the link is refused before anything connects.
  await guest.reload()
  await expect(guest.getByText('This link cannot be used')).toBeVisible({ timeout: 15_000 })
  await expect(guest.getByRole('button', { name: 'Join session' })).toHaveCount(0)
  await guestCtx.close()
})
