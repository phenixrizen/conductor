import type { ChatMessage } from '~/utils/protocol'
import { UNREAD_KEY, countUnread, forgetUnread, openUnread, readUnread, unreadCount, writeUnread, type UnreadState } from '~/utils/chatUnread'

/**
 * Unread chat, per browser (design 2b, 2g): the messages from others that
 * arrived while this person did not have the thread open, by thread
 * (`session:<id>`, `run:<id>`), fed by the open page's connection and by the
 * `chat` events of the admin stream (useAttention), each message counted
 * once. Kept in localStorage (utils/chatUnread.ts), so a reload recounts
 * nothing seen, and read again when another tab writes it. A thread that is
 * open counts nothing; opening one clears it. System lines and this browser's
 * own sends never count.
 */
export function useChatUnread() {
  const state = useState<UnreadState>('chatUnreadState', () => (import.meta.client ? readUnread(localStorage) : {}))
  const open = useState<Record<string, boolean>>('chatOpen', () => ({}))
  const selfIds = useState<string[]>('chatSelfIds', () => [])
  const listening = useState<boolean>('chatUnreadListening', () => false)

  function put(next: UnreadState) {
    if (next === state.value) return
    state.value = next
    if (import.meta.client) writeUnread(localStorage, next)
  }

  /** A subscriber id of this browser's own connections, from a welcome: its messages are never unread. */
  function registerSelf(id: string) {
    if (id && !selfIds.value.includes(id)) selfIds.value = [...selfIds.value.slice(-19), id]
  }

  /** A message that arrived live (not a replay) on `key`, from the connection or the stream. */
  function accept(key: string, m: ChatMessage) {
    put(countUnread(state.value, key, m, selfIds.value, !!open.value[key]))
  }

  function openThread(key: string) {
    open.value = { ...open.value, [key]: true }
    put(openUnread(state.value, key, new Date().toISOString()))
  }

  function closeThread(key: string) {
    if (open.value[key]) open.value = { ...open.value, [key]: false }
  }

  function forget(key: string) {
    put(forgetUnread(state.value, key))
  }

  function count(key: string): number {
    return unreadCount(state.value, key)
  }

  /** The counts by thread, for the rail. */
  const counts = computed<Readonly<Record<string, number>>>(() => Object.fromEntries(Object.entries(state.value).map(([k, t]) => [k, t.count])))

  /** Follows what another tab writes (once per app). */
  function listen() {
    if (!import.meta.client || listening.value) return
    listening.value = true
    window.addEventListener('storage', (ev) => {
      if (ev.key === UNREAD_KEY || ev.key === null) state.value = readUnread(localStorage)
    })
  }

  return { counts, registerSelf, accept, openThread, closeThread, forget, count, listen }
}
