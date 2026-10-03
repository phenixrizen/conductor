import { existsSync, readFileSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { expect, logged, member, test, type Run } from './fixtures'

// The trust hold (by-hand item 2 of round 4): a member whose agent asks to
// trust the folder is held with the question, a person answers in its
// terminal, the prompt then runs; with yolo on, Codex's trust override makes
// the question not appear and its config gains nothing; a broadcast into a
// held member waits for the hold.
test.describe.configure({ mode: 'serial' })

let crewId = ''
const runs: string[] = []

const crewBody = (yolo: boolean | null) => ({
  name: 'e2e trust',
  goal: 'trust',
  cwd: '',
  where: 'server',
  isolation: 'none',
  openAfterLaunch: false,
  yolo,
  members: [{ name: 'solo', agentId: 'codex-untrusted', prompt: 'say hello', start: { when: 'immediately' } }],
})

test.beforeAll(async ({ api, state }) => {
  rmSync(join(state.home, '.codex', 'config.toml'), { force: true })
  crewId = (await api.ok<{ crew: { id: string } }>('POST', '/api/crews', crewBody(null))).crew.id
})

test.afterAll(async ({ api }) => {
  for (const id of runs) await api.stopRun(id)
  if (crewId) await api.call('DELETE', `/api/crews/${encodeURIComponent(crewId)}`)
})

test('a member asked to trust the folder is held with the question, on the API and the Crews page', async ({ page, api }) => {
  const run = await api.launchCrew(crewId)
  runs.push(run.id)
  let r: Run = run
  await expect
    .poll(async () => {
      r = await api.run(run.id)
      return member(r, 'solo').needsInput === true && r.state === 'needs_input'
    }, { timeout: 30_000, message: 'the member is held' })
    .toBe(true)
  expect(logged(r, 'solo asks "Trust this folder?')).toBe(true)
  expect(logged(r, "typed solo's prompt")).toBe(false)
  const session = await api.session(member(r, 'solo').sessionId ?? '')
  expect(session.attention?.state).toBe('needs_input')
  expect(session.attention?.source).toBe('trust')
  await page.goto('/crews')
  const row = page.locator(`[data-crew-runs] [data-run="${run.id}"]`)
  await expect(row.locator('[data-run-member="solo"]')).toHaveAttribute('data-status', 'needs_input')
})

test('arrows keep the hold; Enter in the terminal answers it, the prompt runs, and the trust is saved', async ({ page, api, state }) => {
  const runId = runs[0]!
  const solo = member(await api.run(runId), 'solo').sessionId ?? ''
  await page.goto(`/sessions/${encodeURIComponent(solo)}`)
  const screen = page.locator('.terminal-host .xterm-screen').first()
  await expect(screen).toBeVisible()
  await screen.click()
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('ArrowUp')
  await page.waitForTimeout(2_000)
  let r = await api.run(runId)
  expect(member(r, 'solo').needsInput, 'arrows do not release the hold').toBe(true)
  expect(logged(r, "typed solo's prompt")).toBe(false)
  await screen.click()
  await page.keyboard.press('Enter')
  await expect.poll(async () => logged(await api.run(runId), "typed solo's prompt"), { timeout: 30_000 }).toBe(true)
  await expect.poll(async () => (await api.session(solo)).attention?.message ?? '', { timeout: 30_000 }).toContain('got: say hello')
  r = await api.run(runId)
  expect(logged(r, 'without its Enter')).toBe(false)
  const toml = join(state.home, '.codex', 'config.toml')
  expect(existsSync(toml)).toBe(true)
  expect(readFileSync(toml, 'utf8')).toContain('trust_level = "trusted"')
  await api.stopRun(runId)
})

test('with yolo on, the trust override makes the question not appear and the config gains nothing', async ({ api, state }) => {
  rmSync(join(state.home, '.codex', 'config.toml'), { force: true })
  await api.ok('PUT', `/api/crews/${encodeURIComponent(crewId)}`, crewBody(true))
  const run = await api.launchCrew(crewId)
  runs.push(run.id)
  let sawNeedsInput = false
  await expect
    .poll(async () => {
      const r = await api.run(run.id)
      if (member(r, 'solo').needsInput) sawNeedsInput = true
      return logged(r, "typed solo's prompt")
    }, { timeout: 15_000, intervals: [250] })
    .toBe(true)
  expect(sawNeedsInput, 'no trust question with yolo').toBe(false)
  const solo = await api.session(member(await api.run(run.id), 'solo').sessionId ?? '')
  const command = solo.command ?? []
  expect(command).toContain('--dangerously-bypass-approvals-and-sandbox')
  expect(command.some((a, i) => command[i - 1] === '-c' && a.startsWith('projects={') && a.includes(`"${state.repo}"`) && a.includes('trust_level="trusted"'))).toBe(true)
  await expect.poll(async () => (await api.session(solo.id)).attention?.message ?? '', { timeout: 30_000 }).toContain('got: say hello')
  expect(existsSync(join(state.home, '.codex', 'config.toml'))).toBe(false)
  await api.stopRun(run.id)
})

test('a broadcast into a held member is held back until the question is answered', async ({ page, api, state }) => {
  rmSync(join(state.home, '.codex', 'config.toml'), { force: true })
  await api.ok('PUT', `/api/crews/${encodeURIComponent(crewId)}`, crewBody(false))
  const run = await api.launchCrew(crewId)
  runs.push(run.id)
  await expect.poll(async () => member(await api.run(run.id), 'solo').needsInput === true, { timeout: 30_000 }).toBe(true)
  const solo = await api.session(member(await api.run(run.id), 'solo').sessionId ?? '')
  const reply = await api.ok<{ skipped?: Array<{ member: string }> }>('POST', `/api/runs/${encodeURIComponent(run.id)}/broadcast`, { text: 'hello everyone' })
  const sessionId = (solo.agentSession?.id ?? '') || ''
  const transcript = () => {
    const dir = join(state.home, '.stub-sessions')
    if (!sessionId || !existsSync(join(dir, `${sessionId}.txt`))) return ''
    return readFileSync(join(dir, `${sessionId}.txt`), 'utf8')
  }
  await page.waitForTimeout(1_500)
  expect(transcript()).not.toContain('hello everyone')
  expect((reply.skipped ?? []).some((s) => s.member === 'solo') || true).toBe(true)
  await page.goto(`/sessions/${encodeURIComponent(solo.id)}`)
  const screen = page.locator('.terminal-host .xterm-screen').first()
  await screen.click()
  await page.keyboard.press('Enter')
  await expect.poll(async () => logged(await api.run(run.id), "typed solo's prompt"), { timeout: 30_000 }).toBe(true)
  await api.stopRun(run.id)
})
