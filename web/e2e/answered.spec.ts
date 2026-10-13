import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { BrowserContext, Page } from '@playwright/test'
import { expect, member, test, type Session } from './fixtures'

// Two people answer the same question: Codex's command approval, with the
// next approval queued behind it (the stub's `stub approvals a|b`). Nate
// clicks Yes in his sidebar; Priya, whose page still shows the first
// approval (her live stream held back, as a slow network would), clicks Yes
// after him. Her click names the question she was shown, which is answered:
// the session refuses it and her page says so, the stub reads exactly one
// answer, and the next approval waits, untouched, until its own answer.
test.describe.configure({ mode: 'serial' })

let crewId = ''
const runs: string[] = []
let other: BrowserContext | undefined
/** The member's prompt: two approvals in a row, this run's own (the stub's transcript is found by it). */
const prompt = `stub approvals curl -sI https://example.com/${Date.now()}|ls -la`

test.beforeAll(async ({ api, state }) => {
  // No startup question in the way: those are the trust spec's.
  for (const m of ['stub-hooks-review', 'stub-background', 'stub-update']) rmSync(join(state.home, '.codex', m), { force: true })
  crewId = (
    await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
      name: 'e2e answered',
      goal: 'answered',
      cwd: '',
      where: 'server',
      isolation: 'none',
      openAfterLaunch: false,
      yolo: false,
      members: [{ name: 'fetch', agentId: 'codex', prompt, start: { when: 'immediately' } }],
    })
  ).crew.id
})

test.afterAll(async ({ api }) => {
  await other?.close()
  for (const id of runs) await api.stopRun(id)
  if (crewId) await api.call('DELETE', `/api/crews/${encodeURIComponent(crewId)}`)
})

// Codex asks whether to run without its background server, then whether to
// update, where 1 runs npm install -g @openai/codex: a 1 meant for the first,
// clicked late from a page that still shows it, must never reach the second.
test("a late click on Codex's background-server question never reaches its update question", async ({ page, browser, api, state }) => {
  const dir = join(state.home, '.codex')
  mkdirSync(dir, { recursive: true })
  const marks = ['stub-background', 'stub-update'].map((m) => join(dir, m))
  const [background, update] = ['stub-background-answer', 'stub-update-answer'].map((m) => join(dir, m)) as [string, string]
  for (const m of marks) writeFileSync(m, '')
  for (const a of [background, update]) rmSync(a, { force: true })
  const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
    name: 'e2e answered startup',
    goal: 'answered',
    cwd: '',
    where: 'server',
    isolation: 'none',
    openAfterLaunch: false,
    yolo: false,
    members: [{ name: 'solo', agentId: 'codex', prompt: 'say hello', start: { when: 'immediately' } }],
  })
  let late: BrowserContext | undefined
  try {
    const run = await api.launchCrew(crew.crew.id)
    runs.push(run.id)
    let id = ''
    const asked = async () => {
      id = member(await api.run(run.id), 'solo').sessionId ?? ''
      return id ? ((await api.session(id)).attention ?? { state: '' }) : { state: '' }
    }
    await expect.poll(async () => (await asked()).message ?? '', { timeout: 45_000, message: 'the background-server question is asked' }).toMatch(/Background server has incompatible/)
    const first = await asked()
    expect(first.source).toBe('trust')

    const listed = (await api.ok<{ sessions: Session[] }>('GET', '/api/sessions')).sessions
    late = await browser.newContext({ viewport: { width: 1440, height: 900 } })
    const priya = await heldBackPage(late, state.token, listed)
    await page.goto('/crews')
    await priya.goto('/crews')
    const row = (p: Page) => p.locator(`[data-session-list="sidebar"] [data-sidebar-row="s:${id}"]`).first()
    for (const p of [page, priya]) {
      await expect(row(p).locator('[data-row-prompt]')).toContainText('Background server', { timeout: 30_000 })
      await expect(row(p).locator('[data-row-choice="1"]')).toContainText('Run without the daemon this time')
    }

    // Nate's 1: the stub runs without the daemon and asks whether to update.
    await row(page).locator('[data-row-choice="1"]').click()
    await expect.poll(() => (existsSync(background) ? readFileSync(background, 'utf8').trim() : ''), { timeout: 15_000 }).toBe('1')
    await expect.poll(async () => (await asked()).message ?? '', { timeout: 15_000, message: 'the update question is asked' }).toMatch(/^Update available/)
    const next = await asked()

    // Priya's page still shows the first question; her 1 names it and is refused, so it never runs the update.
    await row(priya).locator('[data-row-choice="1"]').click()
    const toast = priya.locator('li').filter({ hasText: 'Already answered' }).last()
    await expect(toast).toBeVisible({ timeout: 15_000 })
    await priya.waitForTimeout(1_500)
    expect(existsSync(update), 'the update question got no answer').toBe(false)
    const waiting = await asked()
    expect(waiting.state).toBe('needs_input')
    expect(waiting.id).toBe(next.id)

    // The update question takes its own answer.
    await expect(row(page).locator('[data-row-prompt]')).toContainText('Update available', { timeout: 15_000 })
    await row(page).getByRole('button', { name: /Skip until next version/ }).click()
    await expect.poll(() => (existsSync(update) ? readFileSync(update, 'utf8').trim() : ''), { timeout: 15_000 }).toBe('skip-version')
  } finally {
    await late?.close()
    for (const m of [...marks, background, update]) rmSync(m, { force: true })
    await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
  }
})

/**
 * A second person's workbench whose live stream is held at what the server listed when `listed` was read: every read of the
 * sessions (the stream's snapshot, the list it falls back to) answers that, as a page whose stream fell behind would show.
 */
async function heldBackPage(context: BrowserContext, token: string, listed: Session[]): Promise<Page> {
  await context.addInitScript((token) => {
    localStorage.setItem('conductor.workbenchToken', token)
    localStorage.setItem('conductor.displayName', 'Priya')
  }, token)
  await context.route(/\/api\/events$/, (route) => route.fulfill({ status: 200, contentType: 'text/event-stream', body: `event: snapshot\ndata: ${JSON.stringify(listed)}\n\n` }))
  await context.route(/\/api\/sessions$/, (route) => (route.request().method() === 'GET' ? route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ sessions: listed }) }) : route.continue()))
  return context.newPage()
}

test('a choice clicked once its question was answered is refused, says so, and never lands on the next question', async ({ page, browser, api, state }) => {
  const run = await api.launchCrew(crewId)
  runs.push(run.id)
  let id = ''
  const asked = async () => {
    id = member(await api.run(run.id), 'fetch').sessionId ?? ''
    return id ? ((await api.session(id)).attention ?? { state: '' }) : { state: '' }
  }
  await expect.poll(async () => (await asked()).message ?? '', { timeout: 45_000, message: 'the first approval is asked' }).toContain('curl -sI https://example.com')
  const first = await asked()
  expect(first.source).toBe('question')
  expect(first.id).toMatch(/^[0-9a-f]{16}$/)

  // The stub's transcript: the one that holds this run's line (Codex's own session id is told only as its turn ends, after both
  // approvals).
  const transcript = () => {
    const dir = join(state.home, '.stub-sessions')
    for (const f of existsSync(dir) ? readdirSync(dir) : []) {
      const text = readFileSync(join(dir, f), 'utf8')
      if (text.includes(prompt)) return text
    }
    return ''
  }
  const approvals = () => transcript().match(/^approval: .*$/gm) ?? []

  // Priya's page shows the first approval and keeps showing it.
  const listed = (await api.ok<{ sessions: Session[] }>('GET', '/api/sessions')).sessions
  other = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const priya = await heldBackPage(other, state.token, listed)
  await page.goto('/crews')
  await priya.goto('/crews')
  const row = (p: Page) => p.locator(`[data-session-list="sidebar"] [data-sidebar-row="s:${id}"]`).first()
  for (const p of [page, priya]) {
    await expect(row(p)).toHaveAttribute('data-row-state', 'needs', { timeout: 30_000 })
    await expect(row(p).locator('[data-row-prompt]')).toContainText('curl -sI https://example.com')
    await expect(row(p).locator('[data-row-choice]')).toHaveCount(3)
  }

  // Nate answers: the stub reads y, and asks the next approval at once.
  await row(page).locator('[data-row-choice="1"]').click()
  await expect.poll(approvals, { timeout: 15_000, message: 'the stub read Nate\'s y' }).toEqual(['approval: approved'])
  await expect.poll(async () => (await asked()).message ?? '', { timeout: 15_000, message: 'the next approval is asked' }).toContain('ls -la')
  const next = await asked()
  expect(next.id).not.toBe(first.id)

  // Priya's page still shows the first; her Yes names it, and is refused.
  await expect(row(priya).locator('[data-row-prompt]')).toContainText('curl -sI https://example.com')
  await row(priya).locator('[data-row-choice="1"]').click()
  const toast = priya.locator('li').filter({ hasText: 'Already answered' }).last()
  await expect(toast).toBeVisible({ timeout: 15_000 })
  await expect(toast).toContainText('answered before your reply arrived. Nothing was sent.')

  // Exactly one answer reached the stub; the next approval waits, untouched.
  await priya.waitForTimeout(1_500)
  expect(await approvals()).toEqual(['approval: approved'])
  const waiting = await asked()
  expect(waiting.state).toBe('needs_input')
  expect(waiting.id).toBe(next.id)

  // Nate's page shows the next approval; its own answer takes it.
  await expect(row(page).locator('[data-row-prompt]')).toContainText('ls -la', { timeout: 15_000 })
  await row(page).locator('[data-row-choice="1"]').click()
  await expect.poll(approvals, { timeout: 15_000, message: 'the stub read the next approval\'s y' }).toEqual(['approval: approved', 'approval: approved'])
})
