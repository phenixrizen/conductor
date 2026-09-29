import type { ActivityEntry } from '~/utils/protocol'

/**
 * The events feed. This is a placeholder: the admin stream already delivers
 * every session's activity entries here (see useAttention), and the events
 * page replaces this file with the store that keeps and routes them.
 */
export function useEvents() {
  return {
    /** Takes one activity entry of a session, as the admin stream delivers it. */
    push(_sessionId: string, _entry: ActivityEntry): void {
      /* nothing keeps them yet */
    },
  }
}
