import { existsSync, readFileSync, rmSync } from 'node:fs'
import { join } from 'node:path'
import { expect, member, test } from './fixtures'

// Codex's command approval (Codex 0.161, seen in the rc.7 app on
// 2026-10-11): a crew member whose Codex asks, after its prompt, "Would you
// like to run the following command?" is in Needs you with the dialog's
// words and its three choices, in the sidebar's row and on the Yard, with
// nobody watching it; a choice types its key alone, which the stub reads;
// the bell Codex rings after its turn's done leaves the member done; the
// same dialog, shown again for the next command, is asked again.
test.describe.configure({ mode: 'serial' })

const words = 'Would you like to run the following command? $ curl -sI https://example.com'
let crewId = ''
const runs: string[] = []

test.beforeAll(async ({ api, state }) => {
  // No startup question in the way: those are the trust spec's.
  for (const m of ['stub-hooks-review', 'stub-background', 'stub-update']) rmSync(join(state.home, '.codex', m), { force: true })
  crewId = (
    await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
      name: 'e2e approval',
      goal: 'approval',
      cwd: '',
      where: 'server',
      isolation: 'none',
      openAfterLaunch: false,
      yolo: false,
      members: [{ name: 'fetch', agentId: 'codex', prompt: 'stub approve curl -sI https://example.com', start: { when: 'immediately' } }],
    })
  ).crew.id
})

test.afterAll(async ({ api }) => {
  for (const id of runs) await api.stopRun(id)
  if (crewId) await api.call('DELETE', `/api/crews/${encodeURIComponent(crewId)}`)
})

test("a Codex member's command approval is in Needs you with its words and three choices, and asked again for the next command", async ({ page, api, state }) => {
  const run = await api.launchCrew(crewId)
  runs.push(run.id)
  let id = ''
  const source = async () => {
    id = member(await api.run(run.id), 'fetch').sessionId ?? ''
    return id ? ((await api.session(id)).attention?.source ?? '') : ''
  }
  // The approval comes after the member's prompt, with nobody watching it.
  await expect.poll(source, { timeout: 45_000, message: 'the approval is asked' }).toBe('question')
  const asked = (await api.session(id)).attention
  expect(asked?.state).toBe('needs_input')
  expect(asked?.message).toBe(words)
  expect(asked?.options?.map((o) => o.label)).toEqual(['Yes, proceed', "Yes, and don't ask again", 'No, tell Codex what to do differently'])
  expect(asked?.options?.map((o) => o.input)).toEqual(['y', 'p', '\u001b'])
  expect(member(await api.run(run.id), 'fetch').needsInput).toBe(true)

  const transcript = async () => {
    const agent = (await api.session(id)).agentSession?.id ?? ''
    const file = join(state.home, '.stub-sessions', `${agent}.txt`)
    return agent && existsSync(file) ? readFileSync(file, 'utf8') : ''
  }

  // The sidebar's row, in Needs you: the words and the three choices; the first types y alone.
  await page.goto('/crews')
  const row = page.locator(`[data-session-list="sidebar"] [data-sidebar-row="s:${id}"]`).first()
  await expect(row).toHaveAttribute('data-row-state', 'needs', { timeout: 30_000 })
  await expect(row.locator('[data-row-prompt]')).toHaveText(words)
  await expect(row.locator('[data-row-choice]')).toHaveCount(3)
  await expect(row.locator('[data-row-choice="2"]')).toContainText("Yes, and don't ask again")
  await row.locator('[data-row-choice="1"]').click()
  await expect.poll(transcript, { timeout: 15_000, message: 'the stub read y' }).toContain('approval: approved\n')
  // The turn ends as Codex's did in the rc.7 app: done, then the bell. The bell is the turn's own: the member stays done.
  await expect.poll(transcript, { timeout: 15_000, message: 'the stub rang after its done' }).toContain('bell: after done\n')
  await expect.poll(async () => (await api.session(id)).attention?.state, { timeout: 15_000 }).toBe('done')
  await page.waitForTimeout(1_000)
  expect((await api.session(id)).attention?.state, 'the turn-end bell left the member waiting on nothing').toBe('done')
  await expect(row).not.toHaveAttribute('data-row-state', 'needs')

  // The same dialog for the next command (a broadcast types the line): asked again, here on the Yard, and declined with Escape.
  await api.ok('POST', `/api/runs/${encodeURIComponent(run.id)}/broadcast`, { text: 'stub approve curl -sI https://example.com' })
  await expect.poll(source, { timeout: 30_000, message: 'the approval is asked again' }).toBe('question')
  expect((await api.session(id)).attention?.message).toBe(words)
  await page.goto('/yard')
  const card = page.locator(`[data-wall-queue] [data-queue-session="${id}"]`)
  await expect(card).toBeVisible({ timeout: 30_000 })
  await expect(card).toContainText(words)
  await expect(card.getByRole('button', { name: /^(Yes, proceed|Yes, and don't ask again|No, tell Codex what to do differently)$/ })).toHaveCount(3)
  await card.getByRole('button', { name: /No, tell Codex what to do differently/ }).click()
  await expect.poll(transcript, { timeout: 15_000, message: 'the stub read Escape' }).toContain('approval: declined\n')
  expect((await transcript()).match(/^approval: /gm)).toHaveLength(2)
  await expect.poll(async () => ((await transcript()).match(/^bell: after done$/gm) ?? []).length, { timeout: 15_000 }).toBe(2)
  await page.waitForTimeout(1_000)
  expect((await api.session(id)).attention?.state).toBe('done')
})
