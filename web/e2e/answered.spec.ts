import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Browser, BrowserContext, Locator, Page } from '@playwright/test'
import { expect, member, test, type Api, type Session } from './fixtures'

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
  await startupChain({ page, browser, api, state }, async ({ priya, row, answered, asked }) => {
    // Nate's 1: the stub runs without the daemon and asks whether to update.
    await row(page).locator('[data-row-choice="1"]').click()
    await expect.poll(() => answered('background'), { timeout: 15_000 }).toBe('1')
    await expect.poll(async () => (await asked()).message ?? '', { timeout: 15_000, message: 'the update question is asked' }).toMatch(/^Update available/)
    return { next: await asked() }
  })
})

// Codex's background-server question also takes 2 (restart with these settings), which no quick reply offers: typed in the
// terminal, it answers the question, so a 1 clicked late from a page that still shows it is refused and never reaches the update
// question, which is asked with an id of its own. (Codex's review, thirteenth pass, finding 2.)
test("a 2 typed in Codex's terminal answers its background-server question, and a late click never reaches the update", async ({ page, browser, api, state }) => {
  await startupChain({ page, browser, api, state }, async ({ id, first, answered, asked }) => {
    await page.goto(`/sessions/${encodeURIComponent(id)}`)
    await page.locator('.terminal-host .xterm-screen').first().click()
    await page.keyboard.press('2')
    await expect.poll(() => answered('background'), { timeout: 15_000 }).toBe('2')
    await expect.poll(async () => (await asked()).message ?? '', { timeout: 15_000, message: 'the update question is asked' }).toMatch(/^Update available/)
    const next = await asked()
    expect(next.id).not.toBe(first.id)
    await page.goto('/crews')
    return { next }
  })
})

// Alt+Z pressed in the session's terminal at Codex's update question: xterm sends ESC z, which Codex reads as one Alt chord
// and ignores (the stub too). It is no Escape: the question stays, and holds the member's prompt back, whose Enter would run
// the update; its own answer then takes it. (Codex's review, seventeenth pass.)
test("an Alt chord typed at Codex's update question never lets the member's prompt answer it", async ({ page, api, state }) => {
  const dir = join(state.home, '.codex')
  mkdirSync(dir, { recursive: true })
  const mark = join(dir, 'stub-update')
  const answerFile = join(dir, 'stub-update-answer')
  const answered = () => (existsSync(answerFile) ? readFileSync(answerFile, 'utf8').trim() : '')
  writeFileSync(mark, '')
  rmSync(answerFile, { force: true })
  const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
    name: 'e2e answered alt',
    goal: 'answered',
    cwd: '',
    where: 'server',
    isolation: 'none',
    openAfterLaunch: false,
    yolo: false,
    members: [{ name: 'solo', agentId: 'codex', prompt: 'say hello', start: { when: 'immediately' } }],
  })
  try {
    const run = await api.launchCrew(crew.crew.id)
    runs.push(run.id)
    let id = ''
    const asked = async () => {
      id = member(await api.run(run.id), 'solo').sessionId ?? ''
      return id ? ((await api.session(id)).attention ?? { state: '' }) : { state: '' }
    }
    await expect.poll(async () => (await asked()).message ?? '', { timeout: 45_000, message: 'the update question is asked' }).toMatch(/^Update available/)
    const first = await asked()

    await page.goto(`/sessions/${encodeURIComponent(id)}`)
    await page.locator('.terminal-host .xterm-screen').first().click()
    await page.keyboard.press('Alt+z')
    await page.waitForTimeout(2_000)
    expect(answered(), 'the update question got no answer').toBe('')
    const waiting = await asked()
    expect(waiting.state).toBe('needs_input')
    expect(waiting.id).toBe(first.id)

    // Its own answer takes it.
    await page.locator('[data-quick-reply]').getByRole('button', { name: /Skip until next version/ }).click()
    await expect.poll(answered, { timeout: 15_000 }).toBe('skip-version')
  } finally {
    for (const m of [mark, answerFile]) rmSync(m, { force: true })
    await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
  }
})

// A line typed in the session's reply bar, or its sidebar row's field, names the question it was begun for. Someone else answers
// that question and the next one comes while it is being typed: sent, it is refused ("Already answered"), and never typed into
// the next question. (Codex's review, twenty-ninth pass; the sidebar's, thirtieth.)
for (const where of ['the reply bar', 'the sidebar'] as const) {
  test(`a reply line begun for one question in ${where} is refused once that question is answered, never sent to the next`, async ({ page, api, state }) => {
    const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: `e2e draft ${where}` })
    const transcript = async () => {
      const id = (await api.session(s.id)).agentSession?.id ?? ''
      const file = join(state.home, '.stub-sessions', `${id}.txt`)
      return id && existsSync(file) ? readFileSync(file, 'utf8') : ''
    }
    try {
      await api.ok('POST', `/api/sessions/${s.id}/attention`, { state: 'needs_input', message: 'Which branch?' })
      await page.goto(`/sessions/${encodeURIComponent(s.id)}`)
      const bar = page.locator('[data-quick-reply]')
      const row = page.locator(`[data-session-list="sidebar"] [data-sidebar-row="s:${s.id}"]`).first()
      const shows = where === 'the reply bar' ? bar : row
      await expect(shows).toContainText('Which branch?', { timeout: 30_000 })
      const field = where === 'the reply bar' ? bar.locator('input') : row.locator('[data-row-answer] input')
      await field.fill('feature-draft')
      // Someone else answers it, and the next question comes.
      await api.ok('POST', `/api/sessions/${s.id}/attention`, { state: 'working' })
      await api.ok('POST', `/api/sessions/${s.id}/attention`, { state: 'needs_input', message: 'Which file?' })
      await expect(shows).toContainText('Which file?', { timeout: 15_000 })
      await expect(field).toHaveValue('feature-draft')
      await field.press('Enter')
      await expect(page.locator('li').filter({ hasText: 'Already answered' }).last()).toBeVisible({ timeout: 15_000 })
      await page.waitForTimeout(1_500)
      expect(await transcript(), 'the line never reached the next question').not.toContain('feature-draft')
      expect((await api.session(s.id)).attention?.message).toBe('Which file?')
    } finally {
      await api.stopSession(s.id)
    }
  })
}

type StartupCtx = {
  priya: Page
  id: string
  first: { id?: string }
  row: (p: Page) => Locator
  answered: (which: 'background' | 'update') => string
  asked: () => Promise<{ state: string; message?: string; source?: string; id?: string }>
}

/**
 * Codex's startup chain (the stub's background-server question, then its update question) in a crew of one, shown on Nate's
 * page and on Priya's, whose stream is held back at the background-server question. `answer` answers the first question the
 * way the test says and returns the update question asked then; Priya then clicks the first question's 1, which is refused
 * and never reaches the update question, and the update question takes its own answer.
 */
async function startupChain(
  { page, browser, api, state }: { page: Page; browser: Browser; api: Api; state: { home: string; token: string } },
  answer: (ctx: StartupCtx) => Promise<{ next: { id?: string } }>,
) {
  const dir = join(state.home, '.codex')
  mkdirSync(dir, { recursive: true })
  const marks = ['stub-background', 'stub-update'].map((m) => join(dir, m))
  const files = { background: join(dir, 'stub-background-answer'), update: join(dir, 'stub-update-answer') }
  const answered = (which: 'background' | 'update') => (existsSync(files[which]) ? readFileSync(files[which], 'utf8').trim() : '')
  for (const m of marks) writeFileSync(m, '')
  for (const a of Object.values(files)) rmSync(a, { force: true })
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

    const { next } = await answer({ priya, id, first, row, answered, asked })

    // Priya's page still shows the first question; her 1 names it and is refused, so it never runs the update.
    await row(priya).locator('[data-row-choice="1"]').click()
    const toast = priya.locator('li').filter({ hasText: 'Already answered' }).last()
    await expect(toast).toBeVisible({ timeout: 15_000 })
    await priya.waitForTimeout(1_500)
    expect(answered('update'), 'the update question got no answer').toBe('')
    const waiting = await asked()
    expect(waiting.state).toBe('needs_input')
    expect(waiting.id).toBe(next.id)

    // The update question takes its own answer.
    await expect(row(page).locator('[data-row-prompt]')).toContainText('Update available', { timeout: 15_000 })
    await row(page).getByRole('button', { name: /Skip until next version/ }).click()
    await expect.poll(() => answered('update'), { timeout: 15_000 }).toBe('skip-version')
  } finally {
    await late?.close()
    for (const m of [...marks, ...Object.values(files)]) rmSync(m, { force: true })
    await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
  }
}

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
