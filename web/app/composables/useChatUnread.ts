import type { ChatMessage } from '~/utils/protocol'

/**
 * Unread chat, per browser (design 2b, 2g): messages from others that arrived
 * while this person did not have the thread open, by thread (`session:<id>`,
 * `run:<id>`). A thread that is open counts nothing; opening one clears it.
 * System lines and this browser's own sends never count. In memory for now;
 * the sidebar's counts and the persistence come with the pills.
 */
export function useChatUnread() {
  const counts = useState<Record<string, number>>('chatUnread', () => ({}))
  const open = useState<Record<string, boolean>>('chatOpen', () => ({}))
  const selfIds = useState<string[]>('chatSelfIds', () => [])

  /** A subscriber id of this browser's own connections, from a welcome: its messages are never unread. */
  function registerSelf(id: string) {
    if (id && !selfIds.value.includes(id)) selfIds.value = [...selfIds.value.slice(-19), id]
  }

  /** A message that arrived live (not a replay) on `key`. */
  function accept(key: string, m: ChatMessage) {
    if (m.kind === 'system') return
    if (selfIds.value.includes(m.by.id)) return
    if (open.value[key]) return
    counts.value = { ...counts.value, [key]: (counts.value[key] ?? 0) + 1 }
  }

  function openThread(key: string) {
    open.value = { ...open.value, [key]: true }
    if (counts.value[key]) counts.value = { ...counts.value, [key]: 0 }
  }

  function closeThread(key: string) {
    if (open.value[key]) open.value = { ...open.value, [key]: false }
  }

  function count(key: string): number {
    return counts.value[key] ?? 0
  }

  return { counts: readonly(counts), registerSelf, accept, openThread, closeThread, count }
}
