import { expect, test, type Session } from './fixtures'

// The Yard's queue shows only while something needs input (#360): a question
// answered from the queue takes the queue away with it, the answered line and
// all, and the session stays on the grid as a tile.

const sessions: string[] = []

test.afterAll(async ({ api }) => {
  for (const id of sessions) await api.stopSession(id)
})

test('the queue goes once the last question is answered, though the answer was just given', async ({ page, api }) => {
  const asker = await api.ok<Session>('POST', '/api/sessions', { agentId: 'asker', name: 'e2e yard queue' })
  sessions.push(asker.id)
  await expect.poll(async () => (await api.session(asker.id)).attention?.state, { timeout: 30_000 }).toBe('needs_input')
  const first = (await api.session(asker.id)).attention?.options?.[0]
  expect(first, 'the asker offers choices').toBeTruthy()

  await page.goto('/yard')
  const queue = page.locator('[data-wall-queue]')
  const card = queue.locator(`[data-queue-session="${asker.id}"]`)
  await expect(card).toBeVisible({ timeout: 30_000 })
  await card.getByRole('button', { name: first!.label, exact: true }).click()

  // Answered: nothing waits, so the queue is gone (its "answered" lines with it), and the tile remains.
  await expect.poll(async () => (await api.session(asker.id)).lastAnswer?.byName, { timeout: 30_000 }).toBeTruthy()
  await expect(queue).toHaveCount(0, { timeout: 30_000 })
  await expect(page.locator('[data-session-tile]', { hasText: 'e2e yard queue' }).first()).toBeVisible({ timeout: 30_000 })
})
