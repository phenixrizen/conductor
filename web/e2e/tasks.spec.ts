import { execFileSync } from 'node:child_process'
import { expect, logged, task, test, type Run } from './fixtures'

// A managed run end to end, with the stub as its workers (#334, row 6): a
// crew with tasks launches, the tasks are dispatched into attempt worktrees,
// the worker reads its task through the MCP tools (the builder) or the CLI
// (the writer) and submits its result, a check that fails starts a fresh
// attempt at the result commit with the log in its task file, the dependent
// task starts at the accepted commit, and the run ends with an outcome. A
// done finishes nothing: the stub reports done after every submit, and no
// member ever started because another was done.
test.describe.configure({ mode: 'serial' })

let crewId = ''
const runs: string[] = []

const crewBody = {
  name: 'e2e tasks',
  goal: 'prove the harness',
  cwd: '',
  where: 'server',
  isolation: 'worktree',
  openAfterLaunch: false,
  yolo: null,
  members: [
    { name: 'builder', agentId: 'worker', prompt: '', start: { when: 'task' } },
    { name: 'writer', agentId: 'worker-cli', prompt: '', start: { when: 'task' } },
  ],
  tasks: [
    // The check fails until the builder adds stub-fixed.txt, which it does on its second attempt.
    { id: 'build', member: 'builder', kind: 'work', instructions: 'Build it.', checks: [{ id: 'fixed', argv: ['git', 'cat-file', '-e', 'HEAD:stub-fixed.txt'] }] },
    { id: 'docs', member: 'writer', kind: 'work', instructions: 'Document it.', needs: ['build'] },
  ],
}

test.beforeAll(async ({ api }) => {
  crewId = (await api.ok<{ crew: { id: string } }>('POST', '/api/crews', crewBody)).crew.id
})

test.afterAll(async ({ api }) => {
  for (const id of runs) await api.stopRun(id)
  if (crewId) await api.call('DELETE', `/api/crews/${encodeURIComponent(crewId)}`)
})

const git = (cwd: string, ...args: string[]) => execFileSync('git', args, { cwd, encoding: 'utf8' }).trim()

test('a check that fails starts a second attempt at the result, the dependent task starts at the accepted commit, and the run succeeds', async ({ api, state }) => {
  const run = await api.launchCrew(crewId)
  runs.push(run.id)
  expect(run.managed, 'launched as a managed run').toBeTruthy()
  let last: Run = run
  await expect
    .poll(async () => {
      last = await api.run(run.id)
      return last.outcome ?? ''
    }, { timeout: 120_000, intervals: [500] })
    .toBe('succeeded')
  expect(last.state).toBe('finished')
  // build: attempt 1 submitted and failed its check; attempt 2 started at that commit and passed.
  const build = task(last, 'build')
  expect(build.state).toBe('succeeded')
  expect(build.attempts).toBe(2)
  const { attempts } = await api.ok<{ task: unknown; attempts: Array<{ n: number; state: string; stage?: string; error?: string; start?: string; result?: string; submitted?: { summary?: string } }> }>(
    'GET',
    `/api/runs/${encodeURIComponent(run.id)}/tasks/build`,
  )
  expect(attempts.map((a) => [a.n, a.state])).toEqual([
    [1, 'failed'],
    [2, 'succeeded'],
  ])
  expect(attempts[0].stage).toBe('check fixed')
  expect(attempts[0].submitted?.summary).toBe('stub builder did build, attempt 1')
  expect(attempts[1].start).toBe(attempts[0].result)
  expect(build.result?.commit).toBe(git(state.repo, 'rev-parse', `crew/${run.id}/build.2`))
  expect(git(state.repo, 'cat-file', '-e', `${build.result?.commit}:stub-fixed.txt`)).toBe('')
  // docs: one attempt, in a worktree made at build's result; its commit descends from it.
  const docs = task(last, 'docs')
  expect(docs.state).toBe('succeeded')
  expect(docs.attempts).toBe(1)
  expect(docs.attempt?.start).toBe(build.result?.commit)
  expect(git(state.repo, 'merge-base', '--is-ancestor', build.result!.commit!, docs.result!.commit!)).toBe('')
  // The run log: the dispatch, the failed check, the results; never a start by done.
  expect(logged(last, 'task build: attempt 1 failed at check fixed')).toBe(true)
  expect(logged(last, 'task docs: attempt 1 started as writer')).toBe(true)
  expect(logged(last, 'every task is done: the run succeeded')).toBe(true)
  expect(last.log.some((l) => /is done: starting/.test(l.message ?? ''))).toBe(false)
})

test('a task read by the admin is the attempt\'s task file, and a task of an unknown run is not found', async ({ api }) => {
  const run = await api.run(runs[0]!)
  const build = task(run, 'build')
  const r = await api.call<{ markdown: string; page: number; pages: number; task: { task: string; attempt: number } }>('GET', `/api/sessions/${build.attempt?.sessionId}/task`)
  expect(r.status).toBe(200)
  expect(r.body.page).toBe(1)
  expect(r.body.markdown).toContain('Build it.')
  expect(r.body.markdown).toContain('The attempt before')
  expect([r.body.task.task, r.body.task.attempt]).toEqual(['build', 2])
  expect((await api.call('GET', '/api/runs/nope-00000000/tasks/build')).status).toBe(404)
  expect((await api.call('GET', `/api/runs/${encodeURIComponent(run.id)}/tasks/nope`)).status).toBe(404)
})
