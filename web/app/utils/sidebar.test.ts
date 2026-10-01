import { describe, expect, it } from 'vitest'
import type { SessionInfo } from '~/composables/useSessions'
import { DEFAULT_ROUTES } from './events'
import {
  LEGACY_SIDEBAR_KEY,
  SIDEBAR_KEY,
  SIDEBAR_SIZE,
  SIDEBAR_SIZE_KEY,
  needsDotShown,
  railGroups,
  readSidebarMode,
  readSidebarSize,
  runOpen,
  sessionOpen,
  sidebarSessions,
  writeSidebarMode,
  writeSidebarSize,
  type KeyValueStore,
} from './sidebar'

function memory(initial: Record<string, string> = {}): KeyValueStore & { data: Map<string, string> } {
  const data = new Map(Object.entries(initial))
  return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k) }
}

const session = (over: Partial<SessionInfo>): SessionInfo =>
  ({ id: 'id', name: 'name', kind: 'server', agentId: 'claude', command: [], cwd: '/w', status: 'running', cols: 80, rows: 24, viewers: 0, createdAt: '2026-10-01T09:00:00Z', ...over }) as SessionInfo

describe('readSidebarMode', () => {
  it('reads the saved mode', () => {
    expect(readSidebarMode(memory({ [SIDEBAR_KEY]: 'rail' }))).toBe('rail')
    expect(readSidebarMode(memory({ [SIDEBAR_KEY]: 'full' }))).toBe('full')
    expect(readSidebarMode(memory())).toBe('full')
    expect(readSidebarMode(memory({ [SIDEBAR_KEY]: 'sideways' }))).toBe('full')
  })

  it('moves an old hidden flag to the new key: hidden became the rail', () => {
    const s = memory({ [LEGACY_SIDEBAR_KEY]: '1' })
    expect(readSidebarMode(s)).toBe('rail')
    expect(s.data.get(SIDEBAR_KEY)).toBe('rail')
    expect(s.data.has(LEGACY_SIDEBAR_KEY)).toBe(false)
    const shown = memory({ [LEGACY_SIDEBAR_KEY]: '0' })
    expect(readSidebarMode(shown)).toBe('full')
    expect(shown.data.get(SIDEBAR_KEY)).toBe('full')
    // The new key wins over an old one left beside it.
    expect(readSidebarMode(memory({ [SIDEBAR_KEY]: 'full', [LEGACY_SIDEBAR_KEY]: '1' }))).toBe('full')
  })

  it('is full when storage throws, and writing never throws', () => {
    const broken: KeyValueStore = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
      removeItem: () => {},
    }
    expect(readSidebarMode(broken)).toBe('full')
    expect(() => writeSidebarMode(broken, 'rail')).not.toThrow()
    const s = memory()
    writeSidebarMode(s, 'rail')
    expect(readSidebarMode(s)).toBe('rail')
  })

  it('keeps a migrated choice when the new key cannot be written, and the old key for a later try', () => {
    const s = memory({ [LEGACY_SIDEBAR_KEY]: '1' })
    const full: KeyValueStore = {
      getItem: s.getItem,
      setItem: () => {
        throw new Error('quota')
      },
      removeItem: s.removeItem,
    }
    expect(readSidebarMode(full)).toBe('rail')
    expect(s.data.get(LEGACY_SIDEBAR_KEY)).toBe('1')
    expect(s.data.has(SIDEBAR_KEY)).toBe(false)
  })
})

describe('the sidebars share', () => {
  it('lists every session, or with a run only its members', () => {
    const list = [session({ id: 'a' }), session({ id: 'b', crew: { runId: 'r1', crewId: 'c', member: 'b' } }), session({ id: 'c', crew: { runId: 'r2', crewId: 'c', member: 'c' } })]
    expect(sidebarSessions(list).map((s) => s.id)).toEqual(['a', 'b', 'c'])
    expect(sidebarSessions(list, 'r1').map((s) => s.id)).toEqual(['b'])
    expect(sidebarSessions(list, 'r9')).toEqual([])
  })

  it('knows the page that is open', () => {
    expect(sessionOpen('/sessions/a', 'a')).toBe(true)
    expect(sessionOpen('/sessions/ab', 'a')).toBe(false)
    expect(sessionOpen('/runs/a', 'a')).toBe(false)
    expect(runOpen('/runs/r1', 'r1')).toBe(true)
    expect(runOpen('/sessions/r1', 'r1')).toBe(false)
  })

  it('shows the amber needs-you dot as the Events page routes needs_input to the Badge', () => {
    expect(needsDotShown(DEFAULT_ROUTES)).toBe(true)
    expect(needsDotShown({ ...DEFAULT_ROUTES, needs_input: { ...DEFAULT_ROUTES.needs_input, badge: false } })).toBe(false)
  })
})

describe('readSidebarSize', () => {
  it('reads the saved width, kept within the bounds the sidebar declares', () => {
    expect(SIDEBAR_SIZE).toEqual({ min: 14, default: 18, max: 26 })
    expect(readSidebarSize(memory())).toBe(18)
    expect(readSidebarSize(memory({ [SIDEBAR_SIZE_KEY]: '22.5' }))).toBe(22.5)
    expect(readSidebarSize(memory({ [SIDEBAR_SIZE_KEY]: '40' }))).toBe(26)
    expect(readSidebarSize(memory({ [SIDEBAR_SIZE_KEY]: '3' }))).toBe(14)
    expect(readSidebarSize(memory({ [SIDEBAR_SIZE_KEY]: 'wide' }))).toBe(18)
    expect(readSidebarSize(memory({ [SIDEBAR_SIZE_KEY]: '' }))).toBe(18)
    expect(readSidebarSize(memory({ [SIDEBAR_SIZE_KEY]: 'Infinity' }))).toBe(18)
  })

  it('writes the width within the same bounds, two decimals at most, and skips what is not a number', () => {
    const s = memory()
    writeSidebarSize(s, 21.123456)
    expect(s.data.get(SIDEBAR_SIZE_KEY)).toBe('21.12')
    expect(readSidebarSize(s)).toBe(21.12)
    writeSidebarSize(s, 99)
    expect(s.data.get(SIDEBAR_SIZE_KEY)).toBe('26')
    writeSidebarSize(s, 2)
    expect(s.data.get(SIDEBAR_SIZE_KEY)).toBe('14')
    writeSidebarSize(s, Number.NaN)
    expect(s.data.get(SIDEBAR_SIZE_KEY)).toBe('14')
  })

  it('is the default when storage throws, and writing never throws', () => {
    const broken: KeyValueStore = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
      removeItem: () => {},
    }
    expect(readSidebarSize(broken)).toBe(18)
    expect(() => writeSidebarSize(broken, 20)).not.toThrow()
  })
})

describe('railGroups', () => {
  it('keeps the sidebar order and puts the members of a run together under its name', () => {
    const list = [
      session({ id: 'a', name: 'alone', createdAt: '2026-10-01T09:05:00Z' }),
      session({ id: 'b', name: 'core', crew: { runId: 'r1', crewId: 'api-sweep', member: 'core' }, createdAt: '2026-10-01T09:04:00Z' }),
      session({ id: 'c', name: 'waits', attention: { state: 'needs_input', since: '2026-10-01T09:06:00Z', message: 'Allow?' } }),
      session({ id: 'd', name: 'lead', crew: { runId: 'r1', crewId: 'api-sweep', member: 'lead' }, createdAt: '2026-10-01T09:03:00Z' }),
      session({ id: 'e', name: 'other', crew: { runId: 'r2', crewId: 'docs', member: 'w' }, status: 'exited', endedAt: '2026-10-01T09:07:00Z' }),
    ]
    const groups = railGroups(list, { r1: 'API sweep' })
    expect(groups.map((g) => [g.label, g.items.map((i) => `${i.id}:${i.dot}`)])).toEqual([
      [undefined, ['c:needs']],
      [undefined, ['a:running']],
      ['API sweep', ['b:running', 'd:running']],
      ['docs', ['e:exited']],
    ])
    expect(groups[0]!.items[0]!.message).toBe('Allow?')
  })

  it('gives every group its own key, a run in two sections included', () => {
    const list = [
      session({ id: 'a', crew: { runId: 'r1', crewId: 'c', member: 'a' }, attention: { state: 'needs_input', since: '2026-10-01T09:06:00Z' } }),
      session({ id: 'b', crew: { runId: 'r1', crewId: 'c', member: 'b' } }),
      session({ id: 'c', crew: { runId: 'r1', crewId: 'c', member: 'c' }, status: 'exited' }),
      session({ id: 'd' }),
    ]
    const keys = railGroups(list).map((g) => g.key)
    expect(keys).toEqual(['needs:r1', 'running:', 'running:r1', 'exited:r1'])
    expect(new Set(keys).size).toBe(keys.length)
  })

  it('shows a starting session with the idle dot', () => {
    expect(railGroups([session({ status: 'starting' })])[0]!.items[0]!.dot).toBe('idle')
  })
})
