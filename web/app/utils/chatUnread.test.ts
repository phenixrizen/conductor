import { describe, expect, it } from 'vitest'
import type { ChatMessage } from './protocol'
import { IDS_KEPT, UNREAD_KEY, countUnread, forgetUnread, openUnread, readUnread, unreadCount, writeUnread, type UnreadState } from './chatUnread'
import type { KeyValueStore } from './sidebar'

function memory(initial: Record<string, string> = {}): KeyValueStore & { data: Map<string, string> } {
  const data = new Map(Object.entries(initial))
  return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k) }
}
const msg = (id: string, at: string, by = 'p1', kind: ChatMessage['kind'] = 'message'): ChatMessage => ({ t: 'chat', id, at, scope: 'session', kind, by: { id: by, name: 'Priya', role: 'view' }, text: 'hi' })

describe('countUnread', () => {
  const key = 'session:s1'
  it('counts a message from someone else once, never a system line, never this browser\'s own', () => {
    let s: UnreadState = {}
    s = countUnread(s, key, msg('a', '2026-10-06T08:32:00Z'), ['me'])
    expect(unreadCount(s, key)).toBe(1)
    const again = countUnread(s, key, msg('a', '2026-10-06T08:32:00Z'), ['me'])
    expect(again).toBe(s)
    expect(countUnread(s, key, msg('b', '2026-10-06T08:33:00Z', 'p1', 'system'), ['me'])).toBe(s)
    s = countUnread(s, key, msg('c', '2026-10-06T08:33:00Z', 'me'), ['me'])
    expect(unreadCount(s, key)).toBe(1)
    s = countUnread(s, key, msg('d', '2026-10-06T08:34:00Z', 'p1', 'sent_to_agent'), ['me'])
    expect(unreadCount(s, key)).toBe(2)
    expect(unreadCount(s, 'session:other')).toBe(0)
  })

  it('counts nothing older than the last opening, and nothing while the thread is open, which moves the opening along', () => {
    let s = openUnread({}, key, '2026-10-06T09:00:00Z')
    expect(unreadCount(s, key)).toBe(0)
    s = countUnread(s, key, msg('old', '2026-10-06T08:59:00Z'), [])
    expect(unreadCount(s, key)).toBe(0)
    s = countUnread(s, key, msg('new', '2026-10-06T09:01:00Z'), [])
    expect(unreadCount(s, key)).toBe(1)
    s = openUnread(s, key, '2026-10-06T09:02:00Z')
    expect(unreadCount(s, key)).toBe(0)
    s = countUnread(s, key, msg('seen', '2026-10-06T09:03:00Z'), [], true)
    expect(unreadCount(s, key)).toBe(0)
    expect(s[key]!.openedAt).toBe('2026-10-06T09:03:00Z')
    // The same message, once the thread closed: seen, not counted.
    s = countUnread(s, key, msg('seen', '2026-10-06T09:03:00Z'), [])
    expect(unreadCount(s, key)).toBe(0)
    // The ids kept are bounded.
    for (let i = 0; i < IDS_KEPT + 10; i++) s = countUnread(s, key, msg(`m${i}`, '2026-10-06T09:10:00Z'), [])
    expect(s[key]!.ids).toHaveLength(IDS_KEPT)
    expect(unreadCount(s, key)).toBe(IDS_KEPT + 10)
  })

  it('forgets a thread, and reads and writes the store, dropping what it cannot use', () => {
    let s = countUnread({}, key, msg('a', '2026-10-06T08:32:00Z'), [])
    s = countUnread(s, 'run:r1', msg('b', '2026-10-06T08:32:00Z'), [])
    expect(Object.keys(forgetUnread(s, key))).toEqual(['run:r1'])
    expect(forgetUnread(s, 'nope')).toBe(s)
    const store = memory()
    writeUnread(store, s)
    expect(readUnread(store)).toEqual(s)
    expect(readUnread(memory({ [UNREAD_KEY]: 'nonsense' }))).toEqual({})
    expect(readUnread(memory({ [UNREAD_KEY]: '[1,2]' }))).toEqual({})
    expect(readUnread(memory({ [UNREAD_KEY]: JSON.stringify({ 'session:x': { count: -2, openedAt: 5, ids: ['a', 3] }, bad: null }) }))).toEqual({ 'session:x': { count: 0, openedAt: '', ids: ['a'] } })
  })
})
