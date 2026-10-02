import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test as base, expect } from '@playwright/test'
import { Api, member, type Run } from './fixtures'
import { git, renderConfig, startServer, stopServer, type Started } from './server'

// The live check: the real Claude Code and Codex, launched as one-member crews by a server of their own, take a typed prompt and
// answer it without a person pressing Enter. Skipped unless CONDUCTOR_E2E_LIVE=1, CONDUCTOR_E2E_LIVE_REPO names a git repository
// both agents already trust (a worktree of a trusted repository needs no trust question), and the agent is on the PATH. It runs with
// the real HOME, where the agents keep their logins; it writes nothing there itself (the agents update their own files as they
// always do). The worktrees and branches it makes in the repository are removed afterwards.

const here = dirname(fileURLToPath(import.meta.url))
const repo = process.env.CONDUCTOR_E2E_LIVE_REPO ?? ''
const live = process.env.CONDUCTOR_E2E_LIVE === '1' && repo !== ''
const PROMPT = 'Reply with the single word READY and nothing else'

function onPath(program: string): boolean {
  try {
    execFileSync('sh', ['-c', 'command -v "$1"', 'sh', program], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
}

const test = base.extend<object, { server: Started & { root: string } }>({
  server: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      const root = mkdtempSync(join(tmpdir(), 'conductor-e2e-live-'))
      const config = join(root, 'conductor.json')
      renderConfig(join(here, 'conductor.e2e-live.json'), config)
      mkdirSync(join(root, 'data'))
      const started = await startServer({
        config,
        home: homedir(),
        data: join(root, 'data'),
        allowedRoot: repo,
        defaultCwd: repo,
        log: join(root, 'server.log'),
        passEnv: ['USER', 'LOGNAME', 'SHELL', 'TMPDIR', 'XDG_RUNTIME_DIR', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'CODEX_HOME'],
      })
      try {
        await use({ ...started, root })
      } finally {
        await stopServer(started.pid)
        if (process.env.CONDUCTOR_E2E_KEEP !== '1') rmSync(root, { recursive: true, force: true })
      }
    },
    { scope: 'worker' },
  ],
})

test.skip(!live, 'set CONDUCTOR_E2E_LIVE=1 and CONDUCTOR_E2E_LIVE_REPO to an already-trusted git repository')

for (const agent of ['claude', 'codex']) {
  test.describe(agent, () => {
    test.skip(!onPath(agent), `${agent} is not on the PATH`)

    test('answers a typed prompt without a person pressing Enter', async ({ server }) => {
      test.setTimeout(180_000)
      const api = new Api(server)
      const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
        name: `Live ${agent}`,
        goal: '',
        cwd: repo,
        where: 'server',
        isolation: 'worktree',
        openAfterLaunch: false,
        members: [{ name: 'solo', agentId: agent, prompt: PROMPT, start: { when: 'immediately' } }],
      })
      const run: Run = await api.launchCrew(crew.crew.id)
      try {
        // Claude Code's Stop hook reports done with its answer; Codex's notify reports its turn as needs_input with it.
        await expect
          .poll(async () => {
            const id = member(await api.run(run.id), 'solo').sessionId
            return id ? ((await api.session(id)).attention?.message ?? '') : ''
          }, { timeout: 150_000, intervals: [1_000] })
          .toContain('READY')
        const r = await api.run(run.id)
        expect(r.log.some((e) => (e.message ?? '').includes("typed solo's prompt"))).toBe(true)
      } finally {
        await api.stopRun(run.id)
        const solo = member(await api.run(run.id), 'solo')
        if (solo.branch) {
          try {
            git(repo, 'worktree', 'remove', '--force', join(repo, '.conductor', 'worktrees', run.id, 'solo'))
            git(repo, 'branch', '-D', solo.branch)
          } catch {
            /* left for a person to remove: the run's id names them */
          }
        }
        await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
      }
    })
  })
}
