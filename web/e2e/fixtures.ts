import { existsSync, readFileSync } from 'node:fs'
import { test as base, expect, type Page } from '@playwright/test'
import { git } from './server'
import { readState, type E2EState } from './state'

export { expect, git }

export interface RunMember {
  name: string
  agentId: string
  status: 'pending' | 'starting' | 'running' | 'ended'
  sessionId?: string
  branch?: string
  startedAt?: string
  needsInput?: boolean
  error?: string
}

export interface Run {
  /** GET /api/runs/{run} only: the run's chat, oldest first. */
  chat?: Array<{ id: string; kind: string; text?: string; to?: string }>
  id: string
  crewId: string
  name: string
  state: string
  stoppedAt?: string
  members: RunMember[]
  log: Array<{ at: string; type: string; message?: string }>
}

export interface Session {
  id: string
  name: string
  status: string
  command?: string[]
  cwd?: string
  cols?: number
  crew?: { runId: string; crewId: string; member: string }
  /** `id` names the question (a reply to it names it). */
  attention?: { state: string; message?: string; source?: string; kind?: string; options?: Array<{ label: string; input: string }>; id?: string }
  /** Who answered the last prompt, and its words. */
  lastAnswer?: { byName: string; message?: string }
  /** The agent's own session (its conversation), as the server captured or chose it. */
  agentSession?: { id: string; resumable?: boolean; source?: string }
  /** The session this one resumed or relaunched. */
  resumedFrom?: string
}

/**
 * The admin API of a test server: fetch with the token in the Authorization header. (No parameter properties: Node 23+ loads the
 * specs with its own type stripping, which refuses them.)
 */
export class Api {
  readonly server: { baseURL: string; token: string }

  constructor(server: { baseURL: string; token: string }) {
    this.server = server
  }

  async call<T>(method: string, path: string, body?: unknown): Promise<{ status: number; body: T }> {
    const res = await fetch(this.server.baseURL + path, {
      method,
      headers: { Authorization: `Bearer ${this.server.token}`, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await res.text()
    return { status: res.status, body: (text ? JSON.parse(text) : undefined) as T }
  }

  /** call, failing on a status of 300 or more with what the server said. */
  async ok<T>(method: string, path: string, body?: unknown): Promise<T> {
    const r = await this.call<T>(method, path, body)
    if (r.status >= 300) throw new Error(`${method} ${path}: ${r.status} ${JSON.stringify(r.body)}`)
    return r.body
  }

  crew(id: string) {
    return this.ok<{ crew: { id: string; cwd: string; members: Array<{ name: string; start?: { when: string; member?: string } }> } }>('GET', `/api/crews/${encodeURIComponent(id)}`).then((r) => r.crew)
  }

  launchCrew(id: string) {
    return this.ok<{ run: Run }>('POST', `/api/crews/${encodeURIComponent(id)}/launch`).then((r) => r.run)
  }

  run(id: string) {
    return this.ok<{ run: Run }>('GET', `/api/runs/${encodeURIComponent(id)}`).then((r) => r.run)
  }

  stopRun(id: string) {
    return this.call('POST', `/api/runs/${encodeURIComponent(id)}/stop`)
  }

  session(id: string) {
    return this.ok<{ session: Session }>('GET', `/api/sessions/${encodeURIComponent(id)}`).then((r) => r.session)
  }

  /** Stops a running session (a second call on an ended one removes it). */
  stopSession(id: string) {
    return this.call('DELETE', `/api/sessions/${encodeURIComponent(id)}`)
  }

  /** Resumes (or relaunches) an ended session; the reply is the new session. */
  resumeSession(id: string) {
    return this.call<{ session?: Session; resumed?: boolean; notice?: string; error?: { code: string; message: string } }>('POST', `/api/sessions/${encodeURIComponent(id)}/resume`)
  }

  /** Resumes an ended member of a run. */
  resumeMember(runId: string, name: string) {
    return this.call<{ run?: Run; error?: { code: string; message: string } }>('POST', `/api/runs/${encodeURIComponent(runId)}/members/${encodeURIComponent(name)}/resume`)
  }
}

/** Whether a line of the run's log contains text. */
export function logged(run: Run, text: string): boolean {
  return run.log.some((e) => (e.message ?? '').includes(text))
}

/** The member of a run with the given name. */
export function member(run: Run, name: string): RunMember {
  const m = run.members.find((x) => x.name === name)
  if (!m) throw new Error(`run ${run.id} has no member ${name}`)
  return m
}

/** The hrefs of the session and run links of the full sidebar's list, in the order they show. */
export function sidebarLinks(page: Page): Promise<string[]> {
  return page
    .locator('[data-session-list] a[href^="/sessions/"], [data-session-list] a[href^="/runs/"]')
    .evaluateAll((els) => els.map((el) => el.getAttribute('href') ?? ''))
}

/**
 * The id of the session a `conductor host` registered, or '' before it has: it is on the line the host prints once the server has
 * answered (`conductor: hosting session <id> at …`, internal/cli/host.go), read from `log`, which must be this one host's file so
 * that the first such line is its own.
 */
export function registeredId(log: string): string {
  if (!existsSync(log)) return ''
  return /conductor: hosting session ([0-9a-f]+) at /.exec(readFileSync(log, 'utf8'))?.[1] ?? ''
}

/**
 * The session a `conductor host` registered (registeredId), read back through the API: it must carry `name`, the name the host was
 * given. A hosted session is found this way, never by its name alone: an earlier run's (a repetition's, its host gone but listed for
 * a minute more) has the same name (#202, #211).
 */
export async function hostedSession(api: Api, log: string, name: string): Promise<Session> {
  let id = ''
  await expect
    .poll(() => {
      id = registeredId(log)
      return id
    }, { timeout: 30_000, message: 'the host is registered' })
    .not.toBe('')
  const session = await api.session(id)
  expect(session.name).toBe(name)
  return session
}

/**
 * Ends the session the host logging to `log` registered (registeredId), if it did, through its host, and removes it: for a spec's
 * teardown before it kills the host. The spec holds the log's path from before it starts the host, so that a test that failed
 * anywhere, even before it had the id itself, still leaves nothing behind. Killing the host alone leaves the session listed for a
 * minute and more (signal.DisconnectGrace, then the sweep), where a later spec or repetition finds it (#211, #218). 200: the host
 * was told to stop its command, and the session ends, then is removed; 204: it had ended already, and is removed; 404: gone.
 * Anything else (a 500: the host left on its own) fails here, where the leftover begins, rather than in whatever spec meets it
 * next. Bounded by `ms`, so that a server that stops answering cannot hold the hook past its own deadline.
 */
export async function endHostedSession(api: Api, log: string, ms = 45_000): Promise<void> {
  const id = log ? registeredId(log) : ''
  if (!id) return
  await within(ms, `ending the hosted session ${id}`, async () => {
    const r = await api.stopSession(id)
    if (r.status === 200) {
      await expect.poll(async () => (await api.session(id)).status, { timeout: 15_000, message: `the hosted session ${id} ends` }).toMatch(/^(exited|stopped)$/)
      await api.ok('DELETE', `/api/sessions/${encodeURIComponent(id)}`)
    } else if (r.status !== 204 && r.status !== 404) {
      throw new Error(`the hosted session ${id} could not be ended: ${r.status} ${JSON.stringify(r.body)}`)
    }
  })
}

/** What `f` returns, or a failure naming `what` once `ms` have passed without it. */
async function within<T>(ms: number, what: string, f: () => Promise<T>): Promise<T> {
  let timer: NodeJS.Timeout | undefined
  const late = new Promise<never>((_, reject) => {
    timer = setTimeout(() => reject(new Error(`${what}: no answer in ${ms / 1000} s`)), ms)
  })
  try {
    return await Promise.race([f(), late])
  } finally {
    clearTimeout(timer)
  }
}

export const test = base.extend<{ api: Api }, { state: E2EState }>({
  state: [
    // eslint-disable-next-line no-empty-pattern
    async ({}, use) => {
      await use(readState())
    },
    { scope: 'worker' },
  ],
  api: async ({ state }, use) => {
    await use(new Api(state))
  },
  baseURL: async ({ state }, use) => {
    await use(state.baseURL)
  },
  // Every page starts with the workbench token where the workbench keeps it (useWorkbenchToken: localStorage conductor.workbenchToken).
  page: async ({ page, state }, use) => {
    await page.addInitScript((token) => {
      try {
        localStorage.setItem('conductor.workbenchToken', token)
      } catch {
        /* storage refused: the page asks for the token, and the test fails on what it cannot find */
      }
    }, state.token)
    await use(page)
  },
})
