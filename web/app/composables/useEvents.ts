import type { SessionInfo } from './useSessions'
import type { ActivityEntry } from '~/utils/protocol'
import { EntryHold, appendRing, eventTypeOf, markOf, parseRoutes, type EventColor, type EventType, type RouteRow } from '~/utils/events'

/** Browser storage key of the routing matrix: routes are per browser. */
export const ROUTES_KEY = 'conductor.events.routes'
/** Lines the live feed keeps, across sessions, in memory only. */
export const FEED_SIZE = 500
/** Longest an entry waits for the session change that carries its state. */
const HOLD_MS = 2000

/** One line of the live feed: the entry, its session, its event type and the session's name when it arrived. */
export type FeedEntry = ActivityEntry & { sessionId: string; event: EventType; sessionName: string; seq: number }

/**
 * The badge a session carries for its newest event routed to Badge, until the
 * session reports `working` or `needs_input` (whose amber dot follows the live
 * state instead) or its page is opened.
 */
export interface EventMark {
  type: EventType
  at: string
  label: string
  color: EventColor
  detail: string
}

/** An event with its type, as listeners (browser alerts, the carousel's follow mode) receive it; each checks its own route. */
export interface RoutedEvent {
  sessionId: string
  session?: SessionInfo
  type: EventType
  entry: ActivityEntry
}

type Listener = (e: RoutedEvent) => void

// Entries waiting for the session change that types them (one hold for the
// app), and who hears about typed events.
let hold: EntryHold | undefined
const listeners = new Set<Listener>()
let seq = 0

function readRoutes(): Record<EventType, RouteRow> {
  try {
    return parseRoutes(localStorage.getItem(ROUTES_KEY))
  } catch {
    return parseRoutes(null)
  }
}

/**
 * Events of every session, as the admin stream delivers them (useAttention
 * pushes each `activity` entry here): the live feed, the routing matrix and
 * the badges it puts on sessions. Nothing is polled and nothing persists but
 * the routes.
 */
export function useEvents() {
  const feed = useState<FeedEntry[]>('eventsFeed', () => [])
  const routes = useState<Record<EventType, RouteRow>>('eventsRoutes', () => (import.meta.client ? readRoutes() : parseRoutes(null)))
  const marks = useState<Record<string, EventMark>>('eventsMarks', () => ({}))
  const store = useAttentionStore()

  /** The feed, oldest first; a type whose Feed route is off now is left out. */
  const entries = computed(() => feed.value.filter((e) => routes.value[e.event].feed))

  function session(id: string): SessionInfo | undefined {
    return store.value.sessions.get(id)
  }

  function clearMark(sessionId: string) {
    if (!(sessionId in marks.value)) return
    const next = { ...marks.value }
    delete next[sessionId]
    marks.value = next
  }

  /** Types an entry against its session as the store has it now and sends it where its routes say. */
  function accept(sessionId: string, entry: ActivityEntry) {
    const s = session(sessionId)
    const type = eventTypeOf(entry, s)
    if (!type) return
    const route = routes.value[type]
    if (route.feed) feed.value = appendRing(feed.value, { ...entry, sessionId, event: type, sessionName: s?.name || sessionId, seq: ++seq }, FEED_SIZE)
    if (type === 'working' || type === 'needs_input') clearMark(sessionId)
    else if (route.badge) marks.value = { ...marks.value, [sessionId]: { type, at: entry.at, ...markOf(type, entry) } }
    const e: RoutedEvent = { sessionId, session: s, type, entry }
    for (const fn of listeners) {
      try {
        fn(e)
      } catch {
        /* one listener's failure is its own */
      }
    }
  }

  // The refs are app-wide, so the first caller's accept serves every caller.
  hold ??= new EntryHold(session, accept, HOLD_MS)
  const entryHold = hold

  /**
   * Takes one activity entry of a session. The admin stream sends an
   * attention entry just before the session change that carries its state,
   * so an attention entry the store cannot type yet waits for that change
   * (settle), at most two seconds, and so does every later entry of the same
   * session, which keeps them in order.
   */
  function push(sessionId: string, e: ActivityEntry): void {
    entryHold.push(sessionId, e)
  }

  /** The store changed a session (or all of them): release what its state now settles. */
  function settle(sessionId?: string) {
    entryHold.settle(sessionId)
  }

  /** The session left the registry: what it still holds is typed without it, and its badge goes. */
  function forget(sessionId: string) {
    entryHold.forget(sessionId)
    clearMark(sessionId)
  }

  /** Changes one route and saves the matrix in this browser. Turning Badge off takes that type's badges away. */
  function setRoute(t: EventType, k: keyof RouteRow, v: boolean) {
    routes.value = { ...routes.value, [t]: { ...routes.value[t], [k]: v } }
    try {
      localStorage.setItem(ROUTES_KEY, JSON.stringify(routes.value))
    } catch {
      /* storage unavailable: the change lasts until reload */
    }
    if (k === 'badge' && !v) {
      marks.value = Object.fromEntries(Object.entries(marks.value).filter(([, m]) => m.type !== t))
    }
  }

  /** Calls fn with every typed event; returns the function that stops it. */
  function onEvent(fn: Listener): () => void {
    listeners.add(fn)
    return () => {
      listeners.delete(fn)
    }
  }

  return { entries, push, routes, setRoute, marks, clearMark, settle, forget, onEvent }
}
