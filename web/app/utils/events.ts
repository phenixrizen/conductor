import type { SessionInfo } from '~/composables/useSessions'
import type { ActivityEntry } from '~/utils/protocol'

/**
 * Something a session reported, as the Events page routes it: the six event
 * types agents report, the three attention states (an `attention` entry counts
 * as the state it recorded) and `exit_nonzero`, a process that exited on its
 * own with a non-zero code.
 */
export type EventType = 'needs_input' | 'done' | 'working' | 'tool_denied' | 'progress' | 'artifact' | 'handoff' | 'error' | 'exit_nonzero' | 'tool_use'

/**
 * Where events of one type go in this browser: a badge on the session in the
 * sidebar and on its wall tile, a browser notification with the chime, a jump
 * of the carousel's follow mode, and a line in the live feed.
 */
export interface RouteRow { badge: boolean; browser: boolean; wall: boolean; feed: boolean }

/** Every event type, in the order the routing matrix lists them. */
export const EVENT_TYPES: readonly EventType[] = ['needs_input', 'done', 'working', 'tool_denied', 'progress', 'artifact', 'handoff', 'error', 'exit_nonzero', 'tool_use']

function row(badge: boolean, browser: boolean, wall: boolean, feed: boolean): RouteRow {
  return Object.freeze({ badge, browser, wall, feed })
}

/**
 * Routes nobody has changed: the mockup's matrix, with `error` routed like
 * `tool_denied`. Frozen; parseRoutes hands out copies.
 */
export const DEFAULT_ROUTES: Record<EventType, RouteRow> = Object.freeze({
  needs_input: row(true, true, true, true),
  done: row(true, false, false, true),
  working: row(false, false, false, true),
  tool_denied: row(true, true, false, true),
  progress: row(false, false, false, true),
  artifact: row(false, true, false, true),
  handoff: row(false, false, true, true),
  error: row(true, true, false, true),
  exit_nonzero: row(true, true, false, true),
  tool_use: row(false, false, false, true),
})

export type EventColor = 'warning' | 'success' | 'error' | 'info' | 'neutral'

export interface EventInfo {
  /** Short name on a session badge. */
  label: string
  icon: string
  /** Its colour in the feed and on a session badge, by severity. */
  color: EventColor
  /** What reports it, under its name in the routing matrix. */
  source: string
}

export const EVENT_INFO: Record<EventType, EventInfo> = {
  needs_input: { label: 'needs input', icon: 'i-lucide-hand', color: 'warning', source: 'permission and question hooks · bell · OSC 9/777 · screen pattern' },
  done: { label: 'done', icon: 'i-lucide-check', color: 'success', source: 'the agent finished its turn' },
  working: { label: 'working', icon: 'i-lucide-loader-circle', color: 'neutral', source: 'a prompt was submitted · clears the badge' },
  tool_denied: { label: 'denied', icon: 'i-lucide-shield-x', color: 'error', source: 'a tool call was refused' },
  progress: { label: 'progress', icon: 'i-lucide-list-checks', color: 'success', source: 'skill: a step of a long task' },
  artifact: { label: 'artifact', icon: 'i-lucide-link', color: 'info', source: 'skill: a pull request, file or URL' },
  handoff: { label: 'handoff', icon: 'i-lucide-arrow-right-left', color: 'info', source: 'skill: work passed to another member' },
  error: { label: 'error', icon: 'i-lucide-triangle-alert', color: 'error', source: 'an error the agent hit' },
  exit_nonzero: { label: 'exited', icon: 'i-lucide-circle-x', color: 'error', source: 'the process exited with a non-zero code' },
  tool_use: { label: 'tool', icon: 'i-lucide-wrench', color: 'neutral', source: 'every tool call (chatty)' },
}

type StateEvent = 'needs_input' | 'working' | 'done'

function isState(s?: string): s is StateEvent {
  return s === 'needs_input' || s === 'working' || s === 'done'
}

/**
 * Whether the session's attention is the change this attention entry records:
 * the session logs the report's message, or the state itself when the report
 * had none.
 */
function records(session: SessionInfo | undefined, e: ActivityEntry): boolean {
  const a = session?.attention
  if (!a || !isState(a.state)) return false
  return a.message ? a.message === e.message : e.message === a.state
}

/** The code of a process that exited on its own, from its status entry ("exited (exit 1)"); null otherwise. */
export function exitCode(message?: string): number | null {
  const m = /^exited \(exit (-?\d+)\)$/.exec(message ?? '')
  return m ? Number(m[1]) : null
}

/**
 * The event type of an activity entry, or null for entries that are not
 * events (join, leave, input, link, other status changes). An `attention`
 * entry counts as the state of the session's attention it records; one
 * recorded without a message carries the state itself. A status entry counts
 * only for a process that exited with a non-zero code: an admin's Stop ends
 * one with a signal, which is not the agent failing.
 */
export function eventTypeOf(entry: ActivityEntry, session?: SessionInfo): EventType | null {
  switch (entry.type) {
    case 'attention': {
      if (records(session, entry)) return session!.attention!.state as StateEvent
      if (isState(entry.message)) return entry.message
      const state = session?.attention?.state
      return isState(state) ? state : null
    }
    case 'status': {
      const code = exitCode(entry.message)
      return code !== null && code !== 0 ? 'exit_nonzero' : null
    }
    case 'progress':
    case 'artifact':
    case 'handoff':
    case 'tool_use':
    case 'tool_denied':
    case 'error':
      return entry.type
  }
  return null
}

/**
 * Whether eventTypeOf can type an entry against this session yet. The admin
 * stream sends an attention entry just before the session change that carries
 * its state, so one with a message of its own waits for the session to show
 * that message; one recorded without a message names its state.
 */
export function attentionSettled(entry: ActivityEntry, session?: SessionInfo): boolean {
  return entry.type !== 'attention' || records(session, entry) || isState(entry.message)
}

/** Longest URL an event carries, in bytes (the server's limit). */
export const MAX_URL_BYTES = 2048

const encoder = new TextEncoder()

/**
 * The URL itself when it is safe to render as a link: http(s) only, at most
 * 2048 bytes, no whitespace or control characters. Anything else (a
 * `javascript:` URL, a local path) is shown as text, never linked.
 */
export function linkableUrl(url?: string): string | null {
  if (!url || /[\s\u0000-\u001f\u007f-\u009f]/.test(url)) return null
  if (encoder.encode(url).length > MAX_URL_BYTES) return null
  let parsed: URL
  try {
    parsed = new URL(url)
  } catch {
    return null
  }
  return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? url : null
}

function flag(v: unknown, fallback: boolean): boolean {
  return typeof v === 'boolean' ? v : fallback
}

/**
 * Routes from their stored JSON: known types and keys with boolean values are
 * kept, unknown keys ignored, and whatever is missing or malformed falls back
 * to DEFAULT_ROUTES. Always a fresh object.
 */
export function parseRoutes(raw: string | null | undefined): Record<EventType, RouteRow> {
  let stored: unknown
  try {
    stored = raw ? JSON.parse(raw) : undefined
  } catch {
    stored = undefined
  }
  const obj = stored && typeof stored === 'object' && !Array.isArray(stored) ? (stored as Record<string, unknown>) : {}
  const out = {} as Record<EventType, RouteRow>
  for (const t of EVENT_TYPES) {
    const r = Object.hasOwn(obj, t) ? obj[t] : undefined
    const given = r && typeof r === 'object' && !Array.isArray(r) ? (r as Record<string, unknown>) : {}
    const d = DEFAULT_ROUTES[t]
    out[t] = { badge: flag(given.badge, d.badge), browser: flag(given.browser, d.browser), wall: flag(given.wall, d.wall), feed: flag(given.feed, d.feed) }
  }
  return out
}

/** The list with item appended, keeping the newest `max` entries. */
export function appendRing<T>(list: readonly T[], item: T, max: number): T[] {
  const out = list.length >= max ? list.slice(list.length - max + 1) : list.slice()
  out.push(item)
  return out
}

/** What an entry says beyond its type, for one line of the feed or a tooltip. The URL of an artifact is rendered on its own. */
export function eventDetail(e: ActivityEntry): string {
  const msg = e.message ?? ''
  switch (e.type) {
    case 'attention':
      return isState(msg) ? '' : msg
    case 'handoff':
      return e.to ? `to ${e.to}${msg ? `: ${msg}` : ''}` : msg
    case 'tool_use':
    case 'tool_denied':
    case 'error':
      return [e.tool, msg].filter(Boolean).join(': ')
  }
  return msg
}

/** The badge a session gets for an event routed to Badge: a short label and a colour by severity. */
export function markOf(type: EventType, entry: ActivityEntry): { label: string; color: EventColor; detail: string } {
  const code = type === 'exit_nonzero' ? exitCode(entry.message) : null
  const label = code !== null ? `exit ${code}` : EVENT_INFO[type].label
  return { label, color: EVENT_INFO[type].color, detail: eventDetail(entry) || entry.url || '' }
}

/** Tailwind text colour of each event colour. */
export const COLOR_TEXT: Record<EventColor, string> = {
  warning: 'text-warning',
  success: 'text-success',
  error: 'text-error',
  info: 'text-info',
  neutral: 'text-muted',
}

/**
 * The icon and colour of an activity entry in a session's Activity tab. An
 * attention entry with a message of its own gets a plain bell: which state it
 * recorded is known only while it is the session's current one.
 */
export function entryIcon(e: ActivityEntry): { icon: string; color: EventColor } {
  const t = eventTypeOf(e)
  if (t) return { icon: EVENT_INFO[t].icon, color: EVENT_INFO[t].color }
  switch (e.type) {
    case 'attention':
      return { icon: 'i-lucide-bell', color: 'neutral' }
    case 'input':
      return { icon: 'i-lucide-keyboard', color: 'neutral' }
    case 'join':
      return { icon: 'i-lucide-log-in', color: 'neutral' }
    case 'leave':
      return { icon: 'i-lucide-log-out', color: 'neutral' }
    case 'link':
      return { icon: 'i-lucide-share-2', color: 'neutral' }
  }
  return { icon: 'i-lucide-power', color: 'neutral' }
}

/**
 * Holds activity entries per session until eventTypeOf can type them. The
 * admin stream sends an attention entry just before the session change that
 * carries its state, so an entry attentionSettled refuses waits for settle()
 * after that change, or `holdMs` at most, and every later entry of the same
 * session waits behind it so the session's entries come out in order. Each
 * released entry goes to `release`, which types it against the session as the
 * store has it then.
 */
export class EntryHold {
  private held = new Map<string, { items: Array<{ entry: ActivityEntry; until: number }>; timer?: ReturnType<typeof setTimeout> }>()

  constructor(
    private readonly session: (sessionId: string) => SessionInfo | undefined,
    private readonly release: (sessionId: string, entry: ActivityEntry) => void,
    private readonly holdMs = 2000,
  ) {}

  /** Takes one entry of a session: released now when nothing of its session waits and it can be typed, held otherwise. */
  push(sessionId: string, entry: ActivityEntry): void {
    const q = this.held.get(sessionId)
    if (!q && attentionSettled(entry, this.session(sessionId))) {
      this.release(sessionId, entry)
      return
    }
    const item = { entry, until: Date.now() + this.holdMs }
    if (q) q.items.push(item)
    else this.held.set(sessionId, { items: [item] })
    this.drain(sessionId)
  }

  /** The store changed a session, or every session (a snapshot): release what that settles. */
  settle(sessionId?: string): void {
    if (sessionId !== undefined) this.drain(sessionId)
    else for (const id of [...this.held.keys()]) this.drain(id)
  }

  /** The session is gone: release what it holds now, in order. */
  forget(sessionId: string): void {
    const q = this.held.get(sessionId)
    if (!q) return
    clearTimeout(q.timer)
    this.held.delete(sessionId)
    for (const item of q.items) this.release(sessionId, item.entry)
  }

  /**
   * Releases a session's entries in order while the first is settled or out
   * of time; `expired` releases the first regardless (its timer fired). A
   * timer then waits for whichever entry comes first.
   */
  private drain(sessionId: string, expired = false): void {
    const q = this.held.get(sessionId)
    if (!q) return
    clearTimeout(q.timer)
    q.timer = undefined
    while (q.items.length) {
      const first = q.items[0]!
      if (!expired && Date.now() < first.until && !attentionSettled(first.entry, this.session(sessionId))) break
      expired = false
      q.items.shift()
      this.release(sessionId, first.entry)
    }
    if (!q.items.length) {
      this.held.delete(sessionId)
      return
    }
    q.timer = setTimeout(() => this.drain(sessionId, true), Math.max(0, q.items[0]!.until - Date.now()))
  }
}
