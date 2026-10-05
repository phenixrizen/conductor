import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// A needs-input report with choices (conductor notify --choices): the
// session page and the wall queue show them as buttons, and a click types
// the choice into the session as a line, which the stub's transcript holds.
test.describe.configure({ mode: 'serial' })

const sessions: string[] = []

test.afterAll(async ({ api }) => {
  for (const id of sessions) await api.stopSession(id)
})

async function ask(api: { ok: <T>(m: string, p: string, b?: unknown) => Promise<T> }): Promise<Session> {
  // POST /api/sessions answers the session itself (its agent session is set at launch, from the agent's recipe).
  const session = await api.ok<Session>('POST', '/api/sessions', { agentId: 'asker' })
  sessions.push(session.id)
  return session
}

function transcript(home: string, s: Session): string {
  const file = join(home, '.stub-sessions', `${s.agentSession?.id ?? ''}.txt`)
  return existsSync(file) ? readFileSync(file, 'utf8') : ''
}

test('the session page offers the choices and a click types one as a line', async ({ page, api, state }) => {
  const s = await ask(api)
  await expect
    .poll(async () => (await api.session(s.id)).attention?.options?.map((o) => o.label), { timeout: 30_000 })
    .toEqual(['Postgres', 'SQLite', 'Keep both'])
  const info = await api.session(s.id)
  expect(info.attention?.state).toBe('needs_input')
  expect(info.attention?.kind).toBe('prompt')
  expect(info.attention?.message).toBe('Which database?')
  expect(info.attention?.options?.map((o) => o.input)).toEqual(['Postgres\r', 'SQLite\r', 'Keep both\r'])

  await page.goto(`/sessions/${encodeURIComponent(s.id)}`)
  const bar = page.locator('[data-quick-reply]')
  await expect(bar).toBeVisible({ timeout: 30_000 })
  await expect(bar.getByRole('button')).toHaveCount(3)
  await bar.getByRole('button', { name: /Keep both/ }).click()
  await expect.poll(() => transcript(state.home, s), { timeout: 15_000, message: 'the choice was typed' }).toContain('Keep both\n')
  await expect.poll(async () => (await api.session(s.id)).attention?.state, { timeout: 15_000 }).not.toBe('needs_input')
  await expect(bar).toBeHidden()
})

test('the wall queue offers the same buttons', async ({ page, api, state }) => {
  const s = await ask(api)
  await expect.poll(async () => (await api.session(s.id)).attention?.state, { timeout: 30_000 }).toBe('needs_input')
  await page.goto('/yard')
  const card = page.locator(`[data-wall-queue] [data-queue-session="${s.id}"]`)
  await expect(card).toBeVisible({ timeout: 30_000 })
  await card.getByRole('button', { name: /SQLite/ }).click()
  await expect.poll(() => transcript(state.home, s), { timeout: 15_000, message: 'the choice was typed' }).toContain('SQLite\n')
  await expect.poll(async () => (await api.session(s.id)).attention?.state, { timeout: 15_000 }).not.toBe('needs_input')
})
