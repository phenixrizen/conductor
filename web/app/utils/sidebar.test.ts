import { describe, expect, it } from 'vitest'
import type { RunInfo, SessionInfo } from '~/composables/useSessions'
import { DEFAULT_ROUTES } from './events'
import {
  DEFAULT_FOLDS,
  LEGACY_SIDEBAR_KEY,
  SIDEBAR_FOLDS_KEY,
  SIDEBAR_KEY,
  SIDEBAR_SIZE,
  SIDEBAR_SIZE_KEY,
  blockSubtitle,
  focusRows,
  moveFocus,
  needsDotShown,
  railModel,
  readFolds,
  readSidebarMode,
  readSidebarSize,
  reopenNeeds,
  rowMeta,
  rowState,
  runOpen,
  sectionPreview,
  sessionOpen,
  sidebarModel,
  writeFolds,
  writeSidebarMode,
  writeSidebarSize,
  type Folds,
  type KeyValueStore,
  type RunBlock,
  type SessionRow,
} from './sidebar'

function memory(initial: Record<string, string> = {}): KeyValueStore & { data: Map<string, string> } {
  const data = new Map(Object.entries(initial))
  return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v), removeItem: (k) => void data.delete(k) }
}

const broken: KeyValueStore = {
  getItem: () => {
    throw new Error('blocked')
  },
  setItem: () => {
    throw new Error('blocked')
  },
  removeItem: () => {},
}

const session = (over: Partial<SessionInfo>): SessionInfo =>
  ({ id: 'id', name: 'name', kind: 'server', agentId: 'claude', command: [], cwd: '/w', status: 'running', cols: 80, rows: 24, viewers: 0, createdAt: '2026-10-01T09:00:00Z', ...over }) as SessionInfo

const run = (over: Partial<RunInfo>): RunInfo =>
  ({ id: 'r1', crewId: 'api-sweep', name: 'API sweep', goal: '', cwd: '/w', isolation: 'none', startedAt: '2026-10-01T08:31:00Z', members: [], log: [], state: 'running', needsInput: 0, yolo: false, ...over }) as RunInfo

const member = (name: string, agentId = 'claude'): RunInfo['members'][number] => ({ name, agentId, start: { when: 'immediately' }, status: 'running' })

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
    expect(readSidebarSize(broken)).toBe(18)
    expect(() => writeSidebarSize(broken, 20)).not.toThrow()
  })
})

describe('rowState', () => {
  it('is exited once ended, needs while waiting on input, running while the process runs, idle otherwise', () => {
    expect(rowState(session({ status: 'exited' }))).toBe('exited')
    expect(rowState(session({ status: 'stopped', attention: { state: 'needs_input' } }))).toBe('exited')
    expect(rowState(session({ attention: { state: 'needs_input' } }))).toBe('needs')
    expect(rowState(session({ status: 'starting', attention: { state: 'needs_input' } }))).toBe('needs')
    expect(rowState(session({ status: 'running' }))).toBe('running')
    expect(rowState(session({ status: 'starting' }))).toBe('idle')
    expect(rowState(session({ status: 'host_disconnected' as SessionInfo['status'] }))).toBe('idle')
  })
})

describe('sidebarModel', () => {
  const ids = (items: readonly (SessionRow | RunBlock)[]) => items.map((it) => (it.kind === 'run' ? `${it.runId}[${it.members.map((m) => m.id).join(',')}]` : it.id))

  it('keeps a run whole where its most urgent member is, the members by urgency then the crew order', () => {
    const list = [
      session({ id: 'core', crew: { runId: 'r1', crewId: 'api-sweep', member: 'core' }, createdAt: '2026-10-01T09:04:00Z' }),
      session({ id: 'review', crew: { runId: 'r1', crewId: 'api-sweep', member: 'review' }, status: 'starting', attention: { state: 'needs_input', since: '2026-10-01T09:06:00Z', message: 'Trust this folder?' } }),
      session({ id: 'lead', crew: { runId: 'r1', crewId: 'api-sweep', member: 'lead' }, status: 'exited', exitCode: 0, endedAt: '2026-10-01T09:07:00Z' }),
      session({ id: 'alone', createdAt: '2026-10-01T09:05:00Z' }),
    ]
    const r1 = run({ members: [member('lead'), member('core'), member('tests', 'codex'), member('review', 'codex-untrusted')] })
    const m = sidebarModel(list, (id) => (id === 'r1' ? r1 : undefined))
    expect(ids(m.needs)).toEqual(['r1[review,core,lead]'])
    expect(ids(m.running)).toEqual(['alone'])
    expect(m.exited).toEqual([])
    const block = m.needs[0] as RunBlock
    expect(block.title).toBe('API sweep')
    expect(block.agents).toBe(4)
    expect(block.state).toBe('needs')
    expect(block.needs).toBe(1)
    expect(block.members.map((x) => x.state)).toEqual(['needs', 'running', 'exited'])
    expect(block.members[0]!.prompt).toEqual({ message: 'Trust this folder?', options: [], kind: undefined, since: '2026-10-01T09:06:00Z' })
    expect(block.members[2]!.exitWord).toBe('exit 0')
    // The counts: who needs you, the Running section's live sessions, the Exited section's.
    expect(m.counts).toEqual({ needs: 1, running: 1, exited: 0 })
  })

  it('places a wholly exited run in Exited, a run of running members in Running, and names a forgotten run by its crew', () => {
    const list = [
      session({ id: 'a', crew: { runId: 'r1', crewId: 'docs', member: 'w' }, status: 'exited', endedAt: '2026-10-01T09:07:00Z' }),
      session({ id: 'b', crew: { runId: 'r1', crewId: 'docs', member: 'x' }, status: 'stopped', endedAt: '2026-10-01T09:08:00Z' }),
      session({ id: 'c', crew: { runId: 'r2', crewId: 'api', member: 'core' }, createdAt: '2026-10-01T09:02:00Z' }),
      session({ id: 'd', crew: { runId: 'r2', crewId: 'api', member: 'lead' }, status: 'starting', createdAt: '2026-10-01T09:01:00Z' }),
      session({ id: 'e', status: 'exited', endedAt: '2026-10-01T09:09:00Z' }),
    ]
    const m = sidebarModel(list, (id) => (id === 'r2' ? run({ id: 'r2', crewId: 'api', name: 'API', label: 'Monday run', members: [member('lead'), member('core')] }) : undefined))
    expect(ids(m.running)).toEqual(['r2[c,d]'])
    expect((m.running[0] as RunBlock).title).toBe('Monday run')
    expect((m.running[0] as RunBlock).state).toBe('running')
    // A run the server forgot keeps its members in the order they came.
    expect(ids(m.exited)).toEqual(['e', 'r1[a,b]'])
    expect((m.exited[1] as RunBlock).title).toBe('docs')
    expect((m.exited[1] as RunBlock).agents).toBe(2)
    expect(m.counts).toEqual({ needs: 0, running: 2, exited: 3 })
  })

  it('tags a hosted session with its machine, under no heading, and never says "server" for it', () => {
    const list = [session({ id: 'h1', kind: 'hosted', hostName: 'priya-mbp' }), session({ id: 'h2', kind: 'hosted', hostName: '' }), session({ id: 'm' })]
    const m = sidebarModel(list)
    const rows = m.running as SessionRow[]
    expect(rows.map((r) => [r.id, r.machine])).toEqual([
      ['h1', 'priya-mbp'],
      ['h2', 'unnamed host'],
      ['m', undefined],
    ])
    expect(rowMeta(list[0]!, false, Date.parse('2026-10-01T09:12:00Z'))).toEqual(['priya-mbp', 'claude', '12m'])
    expect(rowMeta(list[2]!, false, Date.parse('2026-10-01T09:12:00Z'))).toEqual(['claude', 'server', '12m'])
    expect(rowMeta(list[2]!, true, Date.parse('2026-10-01T09:12:00Z'))).toEqual(['claude', '12m'])
    expect(rowMeta(session({ viewers: 2 }), false)).toEqual(['claude', 'server', '2 here'])
  })

  it('orders a section loose rows first, then blocks, each by its newest signal', () => {
    const list = [
      session({ id: 'old', attention: { state: 'needs_input', since: '2026-10-01T09:01:00Z' } }),
      session({ id: 'new', attention: { state: 'needs_input', since: '2026-10-01T09:09:00Z' } }),
      session({ id: 'b1', crew: { runId: 'r1', crewId: 'one', member: 'a' }, attention: { state: 'needs_input', since: '2026-10-01T09:05:00Z' } }),
      session({ id: 'b2', crew: { runId: 'r2', crewId: 'two', member: 'a' }, attention: { state: 'needs_input', since: '2026-10-01T09:08:00Z' } }),
      session({ id: 'r-old', createdAt: '2026-10-01T09:00:00Z' }),
      session({ id: 'r-new', createdAt: '2026-10-01T09:10:00Z' }),
    ]
    const m = sidebarModel(list)
    expect(ids(m.needs)).toEqual(['new', 'old', 'r2[b2]', 'r1[b1]'])
    expect(ids(m.running)).toEqual(['r-new', 'r-old'])
  })

  it('has empty sections and zero counts for no sessions', () => {
    expect(sidebarModel([])).toEqual({ needs: [], running: [], exited: [], counts: { needs: 0, running: 0, exited: 0 } })
  })
})

describe('blockSubtitle', () => {
  const at = (iso: string) => new Date(iso).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', hour12: false })
  it('says when the run started or stopped, and how many agents', () => {
    expect(blockSubtitle({ startedAt: '2026-10-01T08:31:00Z', agents: 3 })).toBe(`started ${at('2026-10-01T08:31:00Z')} · 3 agents`)
    expect(blockSubtitle({ startedAt: '2026-10-01T08:31:00Z', stoppedAt: '2026-10-01T08:40:00Z', agents: 1 })).toBe(`stopped ${at('2026-10-01T08:40:00Z')} · 1 agent`)
    expect(blockSubtitle({ agents: 2 })).toBe('2 agents')
    expect(blockSubtitle({ startedAt: 'never', agents: 2 })).toBe('started  · 2 agents')
  })
})

describe('sectionPreview', () => {
  it('lists the states of a section, a run by its members, six at most', () => {
    const m = sidebarModel([
      session({ id: 'a', attention: { state: 'needs_input' } }),
      session({ id: 'b', crew: { runId: 'r1', crewId: 'c', member: 'b' }, attention: { state: 'needs_input' } }),
      session({ id: 'c', crew: { runId: 'r1', crewId: 'c', member: 'c' } }),
      session({ id: 'd', crew: { runId: 'r1', crewId: 'c', member: 'd' }, status: 'exited' }),
    ])
    expect(sectionPreview(m.needs)).toEqual(['needs', 'needs', 'running', 'exited'])
    expect(sectionPreview(Array.from({ length: 9 }, (_, i) => sidebarModel([session({ id: String(i) })]).running[0]!))).toHaveLength(6)
  })
})

describe('folds', () => {
  it('starts with Exited folded and the rest open, and reads what was saved, field by field', () => {
    expect(DEFAULT_FOLDS).toEqual({ needs: false, running: false, shared: false, exited: true })
    expect(readFolds(memory())).toEqual(DEFAULT_FOLDS)
    expect(readFolds(memory({ [SIDEBAR_FOLDS_KEY]: JSON.stringify({ running: true, exited: false }) }))).toEqual({ needs: false, running: true, shared: false, exited: false })
    expect(readFolds(memory({ [SIDEBAR_FOLDS_KEY]: JSON.stringify({ running: 'yes', other: true }) }))).toEqual(DEFAULT_FOLDS)
    expect(readFolds(memory({ [SIDEBAR_FOLDS_KEY]: '{not json' }))).toEqual(DEFAULT_FOLDS)
    expect(readFolds(memory({ [SIDEBAR_FOLDS_KEY]: '[]' }))).toEqual(DEFAULT_FOLDS)
    expect(readFolds(broken)).toEqual(DEFAULT_FOLDS)
  })

  it('writes the folds and never throws', () => {
    const s = memory()
    const folds: Folds = { needs: true, running: false, shared: true, exited: true }
    writeFolds(s, folds)
    expect(readFolds(s)).toEqual(folds)
    expect(() => writeFolds(broken, folds)).not.toThrow()
  })

  it('reopens Needs you for a new prompt and otherwise leaves the folds alone', () => {
    const folded: Folds = { ...DEFAULT_FOLDS, needs: true }
    expect(reopenNeeds(folded, [])).toBe(folded)
    expect(reopenNeeds(folded, [session({ attention: { state: 'needs_input' } })])).toEqual({ ...folded, needs: false })
    const open: Folds = { ...DEFAULT_FOLDS }
    expect(reopenNeeds(open, [session({ attention: { state: 'needs_input' } })])).toBe(open)
  })
})

describe('focusRows and moveFocus', () => {
  const m = sidebarModel([
    session({ id: 'a', attention: { state: 'needs_input', options: [{ label: 'Yes', input: 'y\r' }] } }),
    session({ id: 'b', crew: { runId: 'r1', crewId: 'c', member: 'b' } }),
    session({ id: 'c', crew: { runId: 'r1', crewId: 'c', member: 'c' } }),
    session({ id: 'd', status: 'exited' }),
  ])
  const shared = [{ id: 'j1' }]

  it('lists the rows a key can land on, in order, a run header among them, and nothing from a folded section', () => {
    const rows = focusRows(m, shared, { ...DEFAULT_FOLDS, exited: false })
    expect(rows.map((r) => r.id)).toEqual(['s:a', 'r:r1', 's:b', 's:c', 'j:j1', 's:d'])
    expect(rows[0]).toEqual({ id: 's:a', kind: 'session', sessionId: 'a', runId: undefined, options: 1 })
    expect(rows[2]).toEqual({ id: 's:b', kind: 'session', sessionId: 'b', runId: 'r1', options: 0 })
    expect(focusRows(m, shared, DEFAULT_FOLDS).map((r) => r.id)).toEqual(['s:a', 'r:r1', 's:b', 's:c', 'j:j1'])
    expect(focusRows(m, shared, { needs: true, running: true, shared: true, exited: true })).toEqual([])
  })

  it('moves between rows, clamped at the ends, and enters the list at the first or the last', () => {
    const rows = focusRows(m, shared, DEFAULT_FOLDS)
    expect(moveFocus(rows, null, 1)).toBe('s:a')
    expect(moveFocus(rows, null, -1)).toBe('j:j1')
    expect(moveFocus(rows, 's:a', 1)).toBe('r:r1')
    expect(moveFocus(rows, 'r:r1', -1)).toBe('s:a')
    expect(moveFocus(rows, 's:a', -1)).toBe('s:a')
    expect(moveFocus(rows, 'j:j1', 1)).toBe('j:j1')
    expect(moveFocus(rows, 'gone', 1)).toBe('s:a')
    expect(moveFocus([], 's:a', 1)).toBeNull()
  })
})

describe('railModel', () => {
  it('draws the list in order: squares, a capsule holding its members, the shared squares, exited runs dashed and loose exited folded into +N', () => {
    const list = [
      session({ id: 'a', name: 'alone', createdAt: '2026-10-01T09:05:00Z' }),
      session({ id: 'b', name: 'core', crew: { runId: 'r1', crewId: 'api-sweep', member: 'core' }, createdAt: '2026-10-01T09:04:00Z' }),
      session({ id: 'c', name: 'waits', attention: { state: 'needs_input', since: '2026-10-01T09:06:00Z', message: 'Allow?' } }),
      session({ id: 'd', name: 'lead', crew: { runId: 'r1', crewId: 'api-sweep', member: 'lead' }, attention: { state: 'needs_input', since: '2026-10-01T09:07:00Z', message: 'Trust this folder?' }, createdAt: '2026-10-01T09:03:00Z' }),
      session({ id: 'e', name: 'other', crew: { runId: 'r2', crewId: 'docs', member: 'w' }, status: 'exited', endedAt: '2026-10-01T09:07:00Z' }),
      session({ id: 'h', name: 'laptop one', kind: 'hosted', hostName: 'laptop', status: 'starting' }),
      session({ id: 'x', name: 'old', status: 'exited', endedAt: '2026-10-01T09:01:00Z' }),
      session({ id: 'y', name: 'older', status: 'stopped', endedAt: '2026-10-01T09:00:00Z' }),
    ]
    const marks = { a: { type: 'done' as const, at: '2026-10-01T09:08:00Z', label: 'done', color: 'success' as const, detail: '' }, x: { type: 'error' as const, at: '2026-10-01T09:08:00Z', label: 'error', color: 'error' as const, detail: '' } }
    const shared = [{ id: 'j1', token: 't', server: '', kind: 'session' as const, name: 'api-review', role: 'view' as const, host: 'switchyard.example.net', agentId: 'codex', addedAt: '2026-10-01T09:00:00Z' }]
    const rail = railModel(sidebarModel(list, (id) => (id === 'r1' ? run({ startedAt: '2026-10-01T08:31:00Z', members: [member('lead'), member('core')] }) : undefined)), shared, marks)
    expect(rail.needs).toBe(2)
    expect(rail.news).toBe(2)
    expect(rail.exitedFolded).toBe(2)
    expect(rail.items.map((i) => `${i.shape}:${i.id}:${i.state}${i.dashed ? ':dashed' : ''}${i.tile ? ':' + i.tile : ''}${i.news ? ':news' + i.news : ''}`)).toEqual([
      'square:c:needs',
      'capsule:run:r1:needs',
      'square:a:running:news1',
      'square:h:idle:machine',
      'square:j:j1:idle:share',
      'capsule:run:r2:exited:dashed',
    ])
    const capsule = rail.items[1]!
    expect(capsule.amber).toBe(true)
    expect(capsule.members!.map((m) => `${m.id}:${m.state}`)).toEqual(['d:needs', 'b:running'])
    expect(capsule.label).toMatch(/^API sweep · run started \d\d:\d\d · lead needs you · core running$/)
    expect(rail.items[0]!.label).toBe('waits · needs you · Allow?')
    expect(rail.items[2]!.label).toBe('alone · running · done')
    expect(rail.items[3]!.label).toBe('laptop one · idle · on laptop')
    expect(rail.items[4]!.label).toBe('api-review · view only · through switchyard.example.net')
    expect(rail.items[5]!.members![0]!.dashed).toBe(true)
    // Unread chat joins the bottom-right number and the words; a run counts its own chat beside its members'.
    const withChat = railModel(sidebarModel(list, (id) => (id === 'r1' ? run({ startedAt: '2026-10-01T08:31:00Z', members: [member('lead'), member('core')] }) : undefined)), shared, marks, { 'session:a': 3, 'session:b': 1, 'run:r1': 2 })
    expect(withChat.news).toBe(4)
    expect(withChat.items[2]!.news).toBe(4)
    expect(withChat.items[2]!.label).toBe('alone · running · done · 3 unread in chat')
    expect(withChat.items[1]!.news).toBe(3)
    expect(withChat.items[1]!.chat).toBe(2)
    expect(withChat.items[1]!.label).toMatch(/· core running · 2 unread in chat$/)
    expect(withChat.items[1]!.members![1]!.news).toBe(1)
  })
})
