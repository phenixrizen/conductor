import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test as base, expect, type Page } from '@playwright/test'
import { Api, logged, member, type Run, type Session } from './fixtures'
import { git, renderConfig, scratchRepo, startServer, stopServer, type Started } from './server'

// The live tier: the real Claude Code and Codex, launched as one-member crews
// by a server of their own. Skipped unless CONDUCTOR_E2E_LIVE=1 and
// CONDUCTOR_E2E_LIVE_REPO names a git repository both agents already trust
// (a worktree of a trusted repository needs no trust question), and the
// agent is on the PATH. It runs with the real HOME, where the agents keep
// their logins (or the keys in ANTHROPIC_API_KEY and OPENAI_API_KEY, which
// pass through); it writes nothing there itself beyond what the agents
// write as they always do, and takes the trust it made them save off again.
// The worktrees and branches it makes in the repository are removed after.
//
// What it checks, per agent: a typed prompt answered without a person
// pressing Enter (and, for Codex, no false "needs input" from its hidden
// title thread meanwhile); the trust question held in a repository the
// agent has not seen, answered by Enter in its terminal; yolo (Codex's trust
// override makes the question not appear and its config gains nothing;
// Claude Code still asks); and a conversation resumed after its session
// ended.

const here = dirname(fileURLToPath(import.meta.url))
const repo = process.env.CONDUCTOR_E2E_LIVE_REPO ?? ''
const live = process.env.CONDUCTOR_E2E_LIVE === '1' && repo !== ''
const PROMPT = 'Reply with the single word READY and nothing else'
const TRUST_WORDS: Record<string, RegExp> = { claude: /project you created or one you trust/i, codex: /trust this folder/i }
const MODEL_ENV: Record<string, Record<string, string>> = {
  // The cheapest model each agent takes from its environment (verify with each release).
  claude: { ANTHROPIC_MODEL: process.env.CONDUCTOR_E2E_LIVE_CLAUDE_MODEL ?? 'claude-haiku-4-5-20251001' },
  codex: {},
}

function onPath(program: string): boolean {
  try {
    execFileSync('sh', ['-c', 'command -v "$1"', 'sh', program], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
}

/** A fresh git repository neither agent has seen, under the live root beside the trusted one. */
function untrustedRepo(root: string): string {
  const dir = mkdtempSync(join(root, 'untrusted-'))
  scratchRepo(dir)
  return dir
}

/** Takes the trust the agents saved for dir off again: Codex's config.toml block, Claude Code's project entry. */
function forgetTrust(dir: string) {
  const toml = join(homedir(), '.codex', 'config.toml')
  if (existsSync(toml)) {
    const text = readFileSync(toml, 'utf8')
    const block = new RegExp(`\\n?\\[projects\\.${JSON.stringify(dir).replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\]\\n(?:[^\\[\\n][^\\n]*\\n?)*`, 'g')
    const next = text.replace(block, '\n')
    if (next !== text) writeFileSync(toml, next)
  }
  const claude = join(homedir(), '.claude.json')
  if (existsSync(claude)) {
    try {
      const j = JSON.parse(readFileSync(claude, 'utf8')) as { projects?: Record<string, unknown> }
      if (j.projects && dir in j.projects) {
        delete j.projects[dir]
        writeFileSync(claude, JSON.stringify(j, null, 2))
      }
    } catch {
      /* left as it is */
    }
  }
}

const test = base.extend<object, { server: Started & { root: string; untrusted: string } }>({
  server: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      const root = mkdtempSync(join(tmpdir(), 'conductor-e2e-live-'))
      const config = join(root, 'conductor.json')
      renderConfig(join(here, 'conductor.e2e-live.json'), config)
      mkdirSync(join(root, 'data'))
      const liveRoot = dirname(repo)
      const untrusted = untrustedRepo(liveRoot)
      const started = await startServer({
        config,
        home: homedir(),
        data: join(root, 'data'),
        allowedRoot: `${repo},${untrusted}`,
        defaultCwd: repo,
        log: join(root, 'server.log'),
        passEnv: ['USER', 'LOGNAME', 'SHELL', 'TMPDIR', 'XDG_RUNTIME_DIR', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME', 'XDG_STATE_HOME', 'CODEX_HOME', 'ANTHROPIC_API_KEY', 'OPENAI_API_KEY'],
      })
      try {
        await use({ ...started, root, untrusted })
      } finally {
        await stopServer(started.pid)
        forgetTrust(untrusted)
        if (process.env.CONDUCTOR_E2E_KEEP !== '1') {
          rmSync(root, { recursive: true, force: true })
          rmSync(untrusted, { recursive: true, force: true })
        }
      }
    },
    { scope: 'worker' },
  ],
})

test.skip(!live, 'set CONDUCTOR_E2E_LIVE=1 and CONDUCTOR_E2E_LIVE_REPO to an already-trusted git repository')

/** The admin token where the workbench keeps it, so a page can type into a session. */
async function signIn(page: Page, server: Started) {
  await page.addInitScript((token) => {
    try {
      localStorage.setItem('conductor.adminToken', token)
    } catch {
      /* the page asks */
    }
  }, server.token)
}

async function pressInTerminal(page: Page, server: Started, sessionId: string, key: string) {
  await page.goto(`${server.baseURL}/sessions/${encodeURIComponent(sessionId)}`)
  const screen = page.locator('.terminal-host .xterm-screen').first()
  await expect(screen).toBeVisible()
  await screen.click()
  await page.keyboard.press(key)
}

function cleanup(api: Api, run: Run, repoDir: string) {
  return (async () => {
    await api.stopRun(run.id)
    const solo = member(await api.run(run.id), 'solo')
    if (solo.branch) {
      try {
        git(repoDir, 'worktree', 'remove', '--force', join(repoDir, '.conductor', 'worktrees', run.id, 'solo'))
        git(repoDir, 'branch', '-D', solo.branch)
      } catch {
        /* left for a person to remove: the run's id names them */
      }
    }
  })()
}

for (const agent of ['claude', 'codex']) {
  test.describe(agent, () => {
    test.skip(!onPath(agent), `${agent} is not on the PATH`)

    const crewBody = (name: string, cwd: string, prompt: string, yolo: boolean | null, isolation: 'none' | 'worktree') => ({
      name: `Live ${agent} ${name}`,
      goal: '',
      cwd,
      where: 'server',
      isolation,
      openAfterLaunch: false,
      yolo,
      members: [{ name: 'solo', agentId: agent, prompt, start: { when: 'immediately' } }],
    })

    const answerOf = async (api: Api, runId: string) => {
      const id = member(await api.run(runId), 'solo').sessionId
      return id ? ((await api.session(id)).attention?.message ?? '') : ''
    }

    test('answers a typed prompt without a person pressing Enter', async ({ server }) => {
      test.setTimeout(180_000)
      const api = new Api(server)
      const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', crewBody('prompt', repo, PROMPT, null, 'worktree'))
      const run: Run = await api.launchCrew(crew.crew.id)
      try {
        let sawNeedsInput = false
        await expect
          .poll(async () => {
            const r = await api.run(run.id)
            if (member(r, 'solo').needsInput) sawNeedsInput = true
            return answerOf(api, run.id)
          }, { timeout: 150_000, intervals: [250] })
          .toContain('READY')
        const r = await api.run(run.id)
        expect(logged(r, "typed solo's prompt")).toBe(true)
        if (agent === 'codex') expect(sawNeedsInput, "Codex's title thread raises no false needs_input").toBe(false)
      } finally {
        await cleanup(api, run, repo)
        await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
      }
    })

    test('is held by the trust question in a repository it has not seen, and Enter in its terminal answers it', async ({ page, server }) => {
      test.setTimeout(240_000)
      const api = new Api(server)
      await signIn(page, server)
      const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', crewBody('trust', server.untrusted, PROMPT, false, 'none'))
      const run: Run = await api.launchCrew(crew.crew.id)
      try {
        let solo: Session | undefined
        await expect
          .poll(async () => {
            const m = member(await api.run(run.id), 'solo')
            if (!m.sessionId) return ''
            solo = await api.session(m.sessionId)
            return solo.attention?.source === 'trust' ? solo.attention.state : solo.attention?.state ?? ''
          }, { timeout: 90_000, intervals: [500], message: 'the trust question holds the member' })
          .toBe('needs_input')
        expect(solo?.attention?.message ?? '').toMatch(TRUST_WORDS[agent]!)
        let r = await api.run(run.id)
        expect(logged(r, "typed solo's prompt")).toBe(false)
        expect(logged(r, 'solo asks')).toBe(true)
        // Enter accepts the trust (the highlighted answer; verify with each release that it is the trusting one).
        await pressInTerminal(page, server, solo!.id, 'Enter')
        await expect
          .poll(async () => {
            r = await api.run(run.id)
            const m = member(r, 'solo')
            if (m.status === 'ended') return 'ended'
            return logged(r, "typed solo's prompt") ? 'typed' : 'held'
          }, { timeout: 60_000, intervals: [500] })
          .not.toBe('held')
        expect(member(r, 'solo').status, 'Enter must trust, not exit').not.toBe('ended')
        await expect.poll(() => answerOf(api, run.id), { timeout: 150_000, intervals: [1_000] }).toContain('READY')
      } finally {
        await cleanup(api, run, server.untrusted)
        await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
      }
    })

    test('with yolo on, Codex asks nothing and saves nothing; Claude Code still asks', async ({ server }) => {
      test.setTimeout(240_000)
      forgetTrust(server.untrusted)
      const api = new Api(server)
      const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', crewBody('yolo', server.untrusted, PROMPT, true, 'none'))
      const run: Run = await api.launchCrew(crew.crew.id)
      try {
        const toml = join(homedir(), '.codex', 'config.toml')
        const before = existsSync(toml) ? readFileSync(toml, 'utf8') : ''
        if (agent === 'codex') {
          let sawTrust = false
          await expect
            .poll(async () => {
              const r = await api.run(run.id)
              const m = member(r, 'solo')
              if (m.sessionId && (await api.session(m.sessionId)).attention?.source === 'trust') sawTrust = true
              return logged(r, "typed solo's prompt")
            }, { timeout: 60_000, intervals: [500] })
            .toBe(true)
          expect(sawTrust, 'the yolo trust override makes the question not appear').toBe(false)
          const solo = await api.session(member(await api.run(run.id), 'solo').sessionId ?? '')
          expect(solo.command).toContain('--dangerously-bypass-approvals-and-sandbox')
          expect((solo.command ?? []).some((a) => a.startsWith('projects={') && a.includes('trust_level="trusted"'))).toBe(true)
          await expect.poll(() => answerOf(api, run.id), { timeout: 150_000, intervals: [1_000] }).toContain('READY')
          const after = existsSync(toml) ? readFileSync(toml, 'utf8') : ''
          expect(after.includes(server.untrusted), 'config.toml gains no trust entry').toBe(false)
          expect(after === before || !after.includes(server.untrusted)).toBe(true)
        } else {
          await expect
            .poll(async () => {
              const m = member(await api.run(run.id), 'solo')
              return m.sessionId ? (await api.session(m.sessionId)).attention?.source ?? '' : ''
            }, { timeout: 90_000, intervals: [500] })
            .toBe('trust')
        }
      } finally {
        await cleanup(api, run, server.untrusted)
        forgetTrust(server.untrusted)
        await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
      }
    })

    test('resumes its conversation after its session ended', async ({ server }) => {
      test.setTimeout(300_000)
      const api = new Api(server)
      const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', crewBody('resume', repo, 'Remember the word PINEAPPLE. Reply with the single word OK and nothing else', null, 'worktree'))
      const run: Run = await api.launchCrew(crew.crew.id)
      try {
        await expect.poll(() => answerOf(api, run.id), { timeout: 150_000, intervals: [1_000] }).toContain('OK')
        const first = member(await api.run(run.id), 'solo')
        await expect.poll(async () => (await api.session(first.sessionId ?? '')).agentSession?.resumable, { timeout: 30_000 }).toBe(true)
        await api.stopSession(first.sessionId ?? '')
        await expect.poll(async () => member(await api.run(run.id), 'solo').status, { timeout: 30_000 }).toBe('ended')
        const r = await api.resumeMember(run.id, 'solo')
        expect(r.status, JSON.stringify(r.body)).toBe(201)
        await expect.poll(async () => member(await api.run(run.id), 'solo').status, { timeout: 60_000 }).toBe('running')
        const next = member(await api.run(run.id), 'solo')
        expect(next.sessionId).not.toBe(first.sessionId)
        expect(next.branch).toBe(first.branch)
        const resumed = await api.session(next.sessionId ?? '')
        expect(resumed.agentSession?.source).toBe('resumed')
        await api.ok('POST', `/api/runs/${encodeURIComponent(run.id)}/broadcast`, { text: 'Which word did I ask you to remember? Reply with that word only' })
        await expect.poll(() => answerOf(api, run.id), { timeout: 150_000, intervals: [1_000] }).toContain('PINEAPPLE')
      } finally {
        await cleanup(api, run, repo)
        await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
      }
    })
  })
}

void MODEL_ENV
