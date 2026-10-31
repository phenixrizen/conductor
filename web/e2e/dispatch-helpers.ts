import type { BrowserContext, Page } from '@playwright/test'
import type { Api } from './fixtures'

// What the Dispatch specs of PR-9 share (dispatch-views.spec.ts, dispatch-keys.spec.ts, the palette specs' Dispatch tests): a project of
// a spec's own under the first free key of its base, the tickets made through the API, the ticket as the store holds it, the rows a
// project's list shows, a second person's browser. Every spec names what it made and counts only the rows of its own project.

/** The first of BASE, BASE1, BASE2… that no project, archived or not, holds on this server. */
export async function freeProjectKey(api: Api, base: string): Promise<string> {
  const taken = new Set<string>()
  let cursor = ''
  do {
    const page = await api.ok<{ projects: Array<{ key: string }>; next?: string }>('GET', `/api/dispatch/projects?archived=1${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`)
    for (const p of page.projects) taken.add(p.key)
    cursor = page.next ?? ''
  } while (cursor)
  for (let i = 0; i < 1000; i++) {
    const key = i === 0 ? base : `${base}${i}`
    if (!taken.has(key)) return key
  }
  throw new Error(`every ${base} key is taken on this server`)
}

/** An op id of the spec's own, unique on the server since the project's key is: ev3-t1. */
export function opOf(key: string, what: string): string {
  return `${key.toLowerCase()}-${what}`
}

/** A project made through the API; its close policy the default (the owner's acceptance) unless one is given. */
export async function makeProject(api: Api, key: string, closeRequires?: string[]): Promise<void> {
  await api.ok('POST', '/api/dispatch/projects', { opId: opOf(key, 'p'), key, name: `E2E project ${key}`, ...(closeRequires ? { closeRequires } : {}) })
}

/** A ticket made through the API, `n` naming its op; its key (KEY-n) returned. */
export async function makeTicket(api: Api, key: string, n: string, fields: Record<string, unknown>): Promise<string> {
  const r = await api.ok<{ ticket: { key: string } }>('POST', '/api/dispatch/tickets', { opId: opOf(key, `t${n}`), project: key, ...fields })
  return r.ticket.key
}

/** A milestone made through the API; its id returned. */
export async function makeMilestone(api: Api, key: string, name: string, target = ''): Promise<string> {
  const r = await api.ok<{ milestone: { id: string } }>('POST', `/api/dispatch/projects/${key}/milestones`, { opId: opOf(key, `m-${name}`), name, ...(target ? { target } : {}) })
  return r.milestone.id
}

export interface StoredTicket {
  key: string
  title: string
  status: string
  priority: number
  horizon: string
  owner: string
  assignees: string[]
  labels: string[]
  milestoneId: string
  rev: number
}

/** The ticket as the store holds it. */
export async function storedTicket(api: Api, key: string): Promise<StoredTicket> {
  return (await api.ok<{ ticket: StoredTicket }>('GET', `/api/dispatch/tickets/${key}`)).ticket
}

/** The keys of the rows a project's list shows, in their order: the rows of that project alone. */
export function rowKeys(page: Page, project: string): Promise<string[]> {
  return page.locator(`[data-ticket-row^="${project}-"]`).evaluateAll((els) => els.map((el) => el.getAttribute('data-ticket-row') ?? ''))
}

/** A second person's workbench on the same server: the token, and a display name of its own. */
export async function secondBrowser(context: BrowserContext, token: string, name = 'Priya'): Promise<Page> {
  await context.addInitScript(
    ({ t, n }) => {
      localStorage.setItem('conductor.workbenchToken', t)
      localStorage.setItem('conductor.displayName', n)
    },
    { t: token, n: name },
  )
  return context.newPage()
}

/** Marks the page's window, to tell later that it was not loaded again. */
export async function mark(page: Page): Promise<void> {
  await page.evaluate(() => ((window as unknown as { dispatchMark?: string }).dispatchMark = 'kept'))
}
export function marked(page: Page): Promise<string | undefined> {
  return page.evaluate(() => (window as unknown as { dispatchMark?: string }).dispatchMark)
}

/** How far the document scrolls sideways: 0 when nothing of the page is wider than the window. */
export function sideways(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
}
