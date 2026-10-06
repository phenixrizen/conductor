import type { ChatMessage } from './protocol'
import type { KeyValueStore } from './sidebar'

/**
 * Unread chat, per browser (design 2b, 2g): the messages from others that
 * arrived on a thread (`session:<id>`, `run:<id>`) since this person last had
 * it open, kept in localStorage so a reload recounts nothing seen. A message
 * counts once (its id), never a system line, never this browser's own, never
 * one older than the thread's last opening. One count per session; a run
 * counts its own chat, not its members'.
 */
export const UNREAD_KEY = 'conductor.chat.unread'
/** Message ids remembered per thread, so the same message (the connection and the stream both carry it) counts once. */
export const IDS_KEPT = 64

export interface UnreadThread {
  count: number
  /** When the thread was last open, as the message times are (RFC 3339); '' never. */
  openedAt: string
  ids: string[]
}
export type UnreadState = Readonly<Record<string, UnreadThread>>

const str = (v: unknown): v is string => typeof v === 'string'

/** The counts kept for this browser; unusable storage or data reads as none. */
export function readUnread(storage: KeyValueStore): UnreadState {
  try {
    const v = JSON.parse(storage.getItem(UNREAD_KEY) ?? '{}') as unknown
    if (!v || typeof v !== 'object' || Array.isArray(v)) return {}
    const out: Record<string, UnreadThread> = {}
    for (const [key, t] of Object.entries(v as Record<string, unknown>)) {
      if (!t || typeof t !== 'object') continue
      const { count, openedAt, ids } = t as Record<string, unknown>
      out[key] = {
        count: typeof count === 'number' && count > 0 ? Math.floor(count) : 0,
        openedAt: str(openedAt) ? openedAt : '',
        ids: Array.isArray(ids) ? ids.filter(str).slice(-IDS_KEPT) : [],
      }
    }
    return out
  } catch {
    return {}
  }
}

export function writeUnread(storage: KeyValueStore, state: UnreadState): void {
  try {
    storage.setItem(UNREAD_KEY, JSON.stringify(state))
  } catch {
    /* storage refused: the counts last the page */
  }
}

const EMPTY: UnreadThread = { count: 0, openedAt: '', ids: [] }

/**
 * The state after `m` arrived on `key`: counted when it is a message or a
 * marker from someone else, not seen before and newer than the last opening.
 * While the thread is open (`open`) nothing counts, and the opening moves to
 * the message's time, so a reload recounts nothing seen. The same object
 * when nothing changes.
 */
export function countUnread(state: UnreadState, key: string, m: Pick<ChatMessage, 'id' | 'kind' | 'at' | 'by'>, selfIds: readonly string[], open = false): UnreadState {
  if (m.kind === 'system') return state
  const t = state[key] ?? EMPTY
  if (t.ids.includes(m.id)) return state
  const ids = [...t.ids, m.id].slice(-IDS_KEPT)
  if (open) return { ...state, [key]: { ...t, ids, openedAt: m.at > t.openedAt ? m.at : t.openedAt } }
  if (selfIds.includes(m.by.id) || m.at <= t.openedAt) return { ...state, [key]: { ...t, ids } }
  return { ...state, [key]: { count: t.count + 1, openedAt: t.openedAt, ids } }
}

/** The thread opened now: nothing unread, and nothing older than now will count. */
export function openUnread(state: UnreadState, key: string, now: string): UnreadState {
  const t = state[key] ?? EMPTY
  if (t.count === 0 && t.openedAt >= now) return state
  return { ...state, [key]: { ...t, count: 0, openedAt: now > t.openedAt ? now : t.openedAt } }
}

/** A thread gone with its session or run. */
export function forgetUnread(state: UnreadState, key: string): UnreadState {
  if (!(key in state)) return state
  const { [key]: _gone, ...rest } = state
  return rest
}

export function unreadCount(state: UnreadState, key: string): number {
  return state[key]?.count ?? 0
}
