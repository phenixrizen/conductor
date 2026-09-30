import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SessionInfo } from '~/composables/useSessions'
import type { ActivityEntry, AttentionState } from '~/utils/protocol'
import { DEFAULT_ROUTES, EVENT_INFO, EVENT_TYPES, EntryHold, appendRing, attentionSettled, entryIcon, eventDetail, eventTypeOf, linkableUrl, markOf, parseRoutes } from './events'

function session(state: AttentionState, message?: string): SessionInfo {
  return {
    id: 's1',
    name: 'api-sweep',
    kind: 'server',
    agentId: 'claude',
    command: ['claude'],
    cwd: '/',
    status: 'running',
    cols: 80,
    rows: 24,
    viewers: 0,
    attention: { state, message },
    createdAt: '2026-09-29T00:00:00Z',
  }
}

function entry(type: ActivityEntry['type'], fields: Partial<ActivityEntry> = {}): ActivityEntry {
  return { at: '2026-09-29T12:00:00Z', type, ...fields }
}

const ALL_TYPES = ['needs_input', 'done', 'working', 'tool_denied', 'progress', 'artifact', 'handoff', 'error', 'exit_nonzero', 'tool_use']

describe('DEFAULT_ROUTES', () => {
  it('has a row with all four routes for every event type', () => {
    expect(Object.keys(DEFAULT_ROUTES).sort()).toEqual([...ALL_TYPES].sort())
    expect([...EVENT_TYPES].sort()).toEqual([...ALL_TYPES].sort())
    for (const t of EVENT_TYPES) expect(Object.keys(DEFAULT_ROUTES[t]).sort()).toEqual(['badge', 'browser', 'feed', 'wall'])
  })

  it('mirrors the mockup matrix', () => {
    const on = (badge: boolean, browser: boolean, wall: boolean, feed: boolean) => ({ badge, browser, wall, feed })
    expect(DEFAULT_ROUTES.needs_input).toEqual(on(true, true, true, true))
    expect(DEFAULT_ROUTES.done).toEqual(on(true, false, false, true))
    expect(DEFAULT_ROUTES.working).toEqual(on(false, false, false, true))
    expect(DEFAULT_ROUTES.tool_denied).toEqual(on(true, true, false, true))
    expect(DEFAULT_ROUTES.progress).toEqual(on(false, false, false, true))
    expect(DEFAULT_ROUTES.artifact).toEqual(on(false, true, false, true))
    expect(DEFAULT_ROUTES.handoff).toEqual(on(false, false, true, true))
    expect(DEFAULT_ROUTES.error).toEqual(on(true, true, false, true))
    expect(DEFAULT_ROUTES.exit_nonzero).toEqual(on(true, true, false, true))
    expect(DEFAULT_ROUTES.tool_use).toEqual(on(false, false, false, true))
  })
})

describe('eventTypeOf', () => {
  it('maps an attention entry by the state of its session', () => {
    expect(eventTypeOf(entry('attention', { message: 'Claude needs your permission to use Bash' }), session('needs_input'))).toBe('needs_input')
    expect(eventTypeOf(entry('attention', { message: 'All tests pass' }), session('done', 'All tests pass'))).toBe('done')
  })

  it('reads the state from an attention entry recorded without a message', () => {
    // The session records the state itself when the report had no message.
    expect(eventTypeOf(entry('attention', { message: 'working' }))).toBe('working')
    expect(eventTypeOf(entry('attention', { message: 'done' }), session(''))).toBe('done')
    expect(eventTypeOf(entry('attention', { message: 'needs_input' }), session('working', 'compiling'))).toBe('needs_input')
  })

  it('prefers the session when its attention is the one the entry records', () => {
    expect(eventTypeOf(entry('attention', { message: 'done' }), session('needs_input', 'done'))).toBe('needs_input')
  })

  it('gives no type to an attention entry it cannot place', () => {
    expect(eventTypeOf(entry('attention', { message: 'Waiting' }))).toBeNull()
    expect(eventTypeOf(entry('attention', { message: 'Waiting' }), session(''))).toBeNull()
  })

  it('maps an exit with a non-zero code to exit_nonzero, and nothing else of a status', () => {
    expect(eventTypeOf(entry('status', { message: 'exited (exit 1)' }))).toBe('exit_nonzero')
    expect(eventTypeOf(entry('status', { message: 'exited (exit -1)' }))).toBe('exit_nonzero')
    expect(eventTypeOf(entry('status', { message: 'exited (exit 0)' }))).toBeNull()
    expect(eventTypeOf(entry('status', { message: 'exited' }))).toBeNull()
    // An admin's Stop ends the process with a signal: not a failure of the agent.
    expect(eventTypeOf(entry('status', { message: 'stopped (exit 143)' }))).toBeNull()
    expect(eventTypeOf(entry('status', { message: 'running' }))).toBeNull()
  })

  it('maps the agent event types to themselves and the roster types to nothing', () => {
    for (const t of ['progress', 'artifact', 'handoff', 'tool_use', 'tool_denied', 'error'] as const) expect(eventTypeOf(entry(t))).toBe(t)
    for (const t of ['join', 'leave', 'input', 'link'] as const) expect(eventTypeOf(entry(t, { message: 'x' }))).toBeNull()
  })
})

describe('attentionSettled', () => {
  it('holds only an attention entry whose state the session does not show yet', () => {
    expect(attentionSettled(entry('progress', { message: '4/7' }))).toBe(true)
    expect(attentionSettled(entry('attention', { message: 'done' }))).toBe(true)
    expect(attentionSettled(entry('attention', { message: 'Approve?' }), session('needs_input', 'Approve?'))).toBe(true)
    expect(attentionSettled(entry('attention', { message: 'Approve?' }), session('working'))).toBe(false)
    expect(attentionSettled(entry('attention', { message: 'Approve?' }))).toBe(false)
  })
})

describe('linkableUrl', () => {
  it('links only http(s) URLs up to 2048 bytes', () => {
    expect(linkableUrl('javascript:alert(1)')).toBeNull()
    expect(linkableUrl('https://x')).toBe('https://x')
    expect(linkableUrl(`https://example.com/${'a'.repeat(2980)}`)).toBeNull()
    expect(linkableUrl('http://example.com/pr/1')).toBe('http://example.com/pr/1')
  })

  it('refuses other schemes, paths, blanks and whitespace', () => {
    for (const u of ['JavaScript:alert(1)', 'data:text/html,hi', 'file:///etc/passwd', 'ftp://x', '/srv/app/report.html', 'report.html', '', ' https://x', 'https://x y', 'https://x\n']) {
      expect(linkableUrl(u)).toBeNull()
    }
    expect(linkableUrl()).toBeNull()
  })

  it('counts bytes, not characters', () => {
    const base = 'https://example.com/'
    expect(linkableUrl(base + 'a'.repeat(2048 - base.length))).not.toBeNull()
    expect(linkableUrl(base + 'a'.repeat(2049 - base.length))).toBeNull()
    expect(linkableUrl(base + 'é'.repeat(1015))).toBeNull() // 1015 × 2 bytes + 20
  })
})

describe('parseRoutes', () => {
  it('falls back to the defaults for missing, broken or foreign data', () => {
    for (const raw of [null, '', 'not json', '[]', '3', 'null']) expect(parseRoutes(raw)).toEqual(DEFAULT_ROUTES)
    const r = parseRoutes(null)
    r.done.browser = true
    expect(DEFAULT_ROUTES.done.browser).toBe(false)
  })

  it('keeps stored booleans, ignores unknown keys and fills in the rest', () => {
    const r = parseRoutes(JSON.stringify({ done: { browser: true, feed: false, extra: true }, bogus: { badge: true }, working: 'x', tool_use: { badge: 'yes' } }))
    expect(r.done).toEqual({ badge: true, browser: true, wall: false, feed: false })
    expect(r.working).toEqual(DEFAULT_ROUTES.working)
    expect(r.tool_use).toEqual(DEFAULT_ROUTES.tool_use)
    expect(Object.keys(r).sort()).toEqual([...ALL_TYPES].sort())
  })
})

describe('appendRing', () => {
  it('keeps the newest entries up to the limit', () => {
    let ring: number[] = []
    for (let i = 1; i <= 7; i++) ring = appendRing(ring, i, 5)
    expect(ring).toEqual([3, 4, 5, 6, 7])
  })
})

describe('markOf', () => {
  it('labels a session badge and colours it by severity', () => {
    expect(markOf('done', entry('attention', { message: 'done' }))).toMatchObject({ label: 'done', color: 'success' })
    expect(markOf('tool_denied', entry('tool_denied', { tool: 'Bash' }))).toMatchObject({ label: 'denied', color: 'error' })
    expect(markOf('error', entry('error', { message: 'boom' }))).toMatchObject({ label: 'error', color: 'error' })
    expect(markOf('exit_nonzero', entry('status', { message: 'exited (exit 1)' }))).toMatchObject({ label: 'exit 1', color: 'error' })
    expect(markOf('artifact', entry('artifact', { url: 'https://x' }))).toMatchObject({ label: 'artifact', color: 'info' })
  })
})

describe('eventDetail', () => {
  it('says what an entry adds to its type, leaving an artifact URL to its link', () => {
    expect(eventDetail(entry('handoff', { to: 'tests', message: '/v1/users done' }))).toBe('to tests: /v1/users done')
    expect(eventDetail(entry('tool_denied', { tool: 'Bash', message: 'rm -rf /' }))).toBe('Bash: rm -rf /')
    expect(eventDetail(entry('artifact', { url: 'https://x', message: 'PR 212' }))).toBe('PR 212')
    expect(eventDetail(entry('artifact', { url: 'https://x' }))).toBe('')
    expect(eventDetail(entry('attention', { message: 'needs_input' }))).toBe('')
    expect(eventDetail(entry('attention', { message: 'Approve the edit?' }))).toBe('Approve the edit?')
  })
})

describe('entryIcon', () => {
  it('gives events their type\'s icon and colour and the roster rows their own', () => {
    expect(entryIcon(entry('artifact', { url: 'https://x' }))).toEqual({ icon: EVENT_INFO.artifact.icon, color: 'info' })
    expect(entryIcon(entry('tool_denied'))).toEqual({ icon: EVENT_INFO.tool_denied.icon, color: 'error' })
    expect(entryIcon(entry('status', { message: 'exited (exit 2)' }))).toEqual({ icon: EVENT_INFO.exit_nonzero.icon, color: 'error' })
    expect(entryIcon(entry('attention', { message: 'done' }))).toEqual({ icon: EVENT_INFO.done.icon, color: 'success' })
    expect(entryIcon(entry('status', { message: 'exited (exit 0)' })).color).toBe('neutral')
    expect(entryIcon(entry('attention', { message: 'Approve the edit?' })).icon).toBe('i-lucide-bell')
    const roster = ['join', 'leave', 'input', 'link'].map((t) => entryIcon(entry(t as ActivityEntry['type'])).icon)
    expect(new Set(roster).size).toBe(4)
  })
})

describe('EntryHold', () => {
  let sessions: Map<string, SessionInfo>
  let released: string[]
  let hold: EntryHold

  beforeEach(() => {
    vi.useFakeTimers()
    sessions = new Map([['s1', session('working')], ['s2', session('')]])
    released = []
    hold = new EntryHold(
      (id) => sessions.get(id),
      (id, e) => released.push(`${id}:${e.type}:${e.message ?? ''}`),
      2000,
    )
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('passes an entry it can type now straight through', () => {
    hold.push('s1', entry('progress', { message: '4/7' }))
    hold.push('s1', entry('attention', { message: 'done' }))
    expect(released).toEqual(['s1:progress:4/7', 's1:attention:done'])
  })

  it('holds an attention entry until its session shows it, and later entries of that session behind it, in order', () => {
    hold.push('s1', entry('attention', { message: 'Approve?' }))
    hold.push('s1', entry('tool_use', { message: 'Bash' }))
    hold.push('s2', entry('progress', { message: 'other session' }))
    expect(released).toEqual(['s2:progress:other session'])
    hold.settle('s1') // a change that does not carry it yet
    expect(released).toHaveLength(1)
    sessions.set('s1', session('needs_input', 'Approve?'))
    hold.settle('s1')
    expect(released).toEqual(['s2:progress:other session', 's1:attention:Approve?', 's1:tool_use:Bash'])
  })

  it('settles every session at once after a snapshot', () => {
    hold.push('s1', entry('attention', { message: 'Approve?' }))
    hold.push('s2', entry('attention', { message: 'Pick one' }))
    sessions.set('s1', session('needs_input', 'Approve?'))
    sessions.set('s2', session('needs_input', 'Pick one'))
    hold.settle()
    expect(released.sort()).toEqual(['s1:attention:Approve?', 's2:attention:Pick one'])
  })

  it('lets a held entry go after two seconds with whatever the session shows', () => {
    hold.push('s1', entry('attention', { message: 'Approve?' }))
    vi.advanceTimersByTime(1999)
    expect(released).toEqual([])
    vi.advanceTimersByTime(1)
    expect(released).toEqual(['s1:attention:Approve?'])
  })

  it('bounds each entry by its own arrival, not by the one ahead of it', () => {
    hold.push('s1', entry('attention', { message: 'first' }))
    vi.advanceTimersByTime(1500)
    hold.push('s1', entry('attention', { message: 'second' }))
    vi.advanceTimersByTime(500)
    expect(released).toEqual(['s1:attention:first'])
    vi.advanceTimersByTime(1500)
    expect(released).toEqual(['s1:attention:first', 's1:attention:second'])
  })

  it('releases what a removed session held at once', () => {
    hold.push('s1', entry('attention', { message: 'Approve?' }))
    hold.push('s1', entry('progress', { message: 'x' }))
    hold.forget('s1')
    expect(released).toEqual(['s1:attention:Approve?', 's1:progress:x'])
    vi.advanceTimersByTime(5000)
    expect(released).toHaveLength(2)
  })
})
