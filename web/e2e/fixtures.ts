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
  attention?: { state: string; message?: string; source?: string; kind?: string; options?: Array<{ label: string; input: string }> }
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
  // Every page starts with the admin token where the workbench keeps it (useAdminToken: localStorage conductor.adminToken).
  page: async ({ page, state }, use) => {
    await page.addInitScript((token) => {
      try {
        localStorage.setItem('conductor.adminToken', token)
      } catch {
        /* storage refused: the page asks for the token, and the test fails on what it cannot find */
      }
    }, state.token)
    await use(page)
  },
})
