import { describe, expect, it, vi } from 'vitest'
import type { AgentInfo, CrewInfo, CrewMember, CrewStart, RunInfo, RunMember, SessionInfo } from '~/composables/useSessions'
import type { FeedEntry } from './events'
import {
  argsFrom,
  broadcastByName,
  broadcastSummary,
  crewFeed,
  crewKey,
  defaultCrew,
  holdViewLink,
  joinTileStatus,
  memberNameError,
  memberNameFrom,
  memberStatus,
  RUN_NAME_RETRY_MS,
  RunNameAsks,
  runActive,
  runCounts,
  sidebarRunFor,
  startFrom,
  startValue,
  takeViewLink,
  toCrewInput,
  toCrewMember,
  toDraft,
} from './crews'

const lead: CrewMember = { name: 'lead', agentId: 'claude', prompt: 'Own the plan for $GOAL.', args: ['--model', 'x'], start: { when: 'immediately' } }
const tests: CrewMember = { name: 'tests', agentId: 'shell', prompt: '', start: { when: 'after', member: 'lead' } }

const info: CrewInfo = {
  id: 'api-sweep',
  name: 'API sweep',
  goal: 'ship /v1/users',
  cwd: '/srv/api',
  where: 'server',
  isolation: 'worktree',
  openAfterLaunch: true,
  viewLinkTtlSeconds: 28800,
  members: [lead, tests],
  createdAt: '2026-09-29T10:00:00Z',
  updatedAt: '2026-09-29T11:00:00Z',
}

/** What goes over the wire: undefined fields are dropped as JSON drops them. */
const wire = (v: unknown) => JSON.parse(JSON.stringify(v))

describe('toCrewInput', () => {
  it('leaves out what the server sets, so a listed crew can be saved as it is', () => {
    expect(wire(toCrewInput(info))).toEqual({
      name: 'API sweep',
      goal: 'ship /v1/users',
      cwd: '/srv/api',
      where: 'server',
      isolation: 'worktree',
      openAfterLaunch: true,
      viewLinkTtlSeconds: 28800,
      members: [
        { name: 'lead', agentId: 'claude', prompt: 'Own the plan for $GOAL.', args: ['--model', 'x'], start: { when: 'immediately' } },
        { name: 'tests', agentId: 'shell', prompt: '', start: { when: 'after', member: 'lead' } },
      ],
    })
  })

  it('keeps only the known fields of members and their start', () => {
    const edited = { ...lead, key: 7, start: { when: 'manual' as const, expanded: true } }
    expect(wire(toCrewInput({ ...info, members: [edited] })).members).toEqual([
      { name: 'lead', agentId: 'claude', prompt: 'Own the plan for $GOAL.', args: ['--model', 'x'], start: { when: 'manual' } },
    ])
  })
})

describe('toCrewMember', () => {
  it('keeps a member to the fields the server accepts, for adding it to a run', () => {
    const withState = { ...tests, key: 7, start: { when: 'after' as const, member: 'lead', open: true } }
    expect(wire(toCrewMember(withState))).toEqual({ name: 'tests', agentId: 'shell', prompt: '', start: { when: 'after', member: 'lead' } })
  })
})

const session = (p: Partial<SessionInfo>): SessionInfo => ({
  id: 's1',
  name: 'lead',
  kind: 'server',
  agentId: 'claude',
  command: ['claude'],
  cwd: '/srv/api',
  status: 'running',
  cols: 80,
  rows: 24,
  viewers: 0,
  createdAt: '2026-09-29T10:00:00Z',
  ...p,
})

const member = (p: Partial<RunMember>): RunMember => ({ name: 'lead', agentId: 'claude', start: { when: 'immediately' }, status: 'running', ...p })

const run = (members: RunMember[]): RunInfo => ({
  id: 'api-sweep-1a2b3c4d',
  crewId: 'api-sweep',
  name: 'API sweep',
  goal: 'ship /v1/users',
  cwd: '/srv/api',
  isolation: 'worktree',
  startedAt: '2026-09-29T10:00:00Z',
  members,
  log: [],
})

describe('memberStatus', () => {
  const r = run([])
  const crew = { runId: r.id, crewId: r.crewId, member: 'lead' }

  it('is the run state while nothing live says otherwise', () => {
    expect(memberStatus(r, member({ status: 'pending' }), [])).toBe('pending')
    expect(memberStatus(r, member({ status: 'starting', sessionId: 's1' }), [session({ crew })])).toBe('starting')
    expect(memberStatus(r, member({ status: 'running', sessionId: 's1' }), [session({ crew })])).toBe('running')
    expect(memberStatus(r, member({ status: 'ended' }), [])).toBe('ended')
  })

  it('is needs_input when a running member waits on a prompt', () => {
    const waiting = session({ crew, attention: { state: 'needs_input', message: 'Allow edit?' } })
    expect(memberStatus(r, member({ status: 'running', sessionId: 's1' }), [waiting])).toBe('needs_input')
  })

  it('does not call a starting member needs_input: its prompt is still to be typed', () => {
    const waiting = session({ crew, attention: { state: 'needs_input' } })
    expect(memberStatus(r, member({ status: 'starting', sessionId: 's1' }), [waiting])).toBe('starting')
  })

  it('finds the session by its crew tag before the run is read again', () => {
    const waiting = session({ id: 's9', crew, attention: { state: 'needs_input' } })
    expect(memberStatus(r, member({ status: 'running' }), [waiting])).toBe('needs_input')
    expect(memberStatus(r, member({ status: 'pending' }), [session({ id: 's9', crew })])).toBe('starting')
  })

  it('ignores a session of another run or member', () => {
    const other = session({ crew: { ...crew, runId: 'other-00000000' }, attention: { state: 'needs_input' } })
    const sibling = session({ id: 's2', crew: { ...crew, member: 'core' }, attention: { state: 'needs_input' } })
    expect(memberStatus(r, member({ status: 'running' }), [other, sibling])).toBe('running')
  })

  it('is ended once its session ended, even when the run was read before', () => {
    expect(memberStatus(r, member({ status: 'running', sessionId: 's1' }), [session({ crew, status: 'exited', exitCode: 0 })])).toBe('ended')
    expect(memberStatus(r, member({ status: 'starting', sessionId: 's1' }), [session({ crew, status: 'stopped' })])).toBe('ended')
  })
})

describe('runCounts', () => {
  it('counts members waiting on a prompt and the others running', () => {
    const r = run([
      member({ name: 'lead', sessionId: 'a' }),
      member({ name: 'core', sessionId: 'b' }),
      member({ name: 'web', sessionId: 'c' }),
      member({ name: 'tests', status: 'pending', start: { when: 'after', member: 'core' } }),
      member({ name: 'logs', status: 'starting', sessionId: 'e' }),
      member({ name: 'old', status: 'ended' }),
    ])
    const tag = (m: string) => ({ runId: r.id, crewId: r.crewId, member: m })
    const live = [
      session({ id: 'a', crew: tag('lead'), attention: { state: 'needs_input' } }),
      session({ id: 'b', crew: tag('core'), attention: { state: 'working' } }),
      session({ id: 'c', crew: tag('web') }),
      session({ id: 'e', crew: tag('logs'), attention: { state: 'needs_input' } }),
    ]
    expect(runCounts(r, live)).toEqual({ needs: 1, running: 2 })
  })

  it('is zero for a run with nothing running', () => {
    expect(runCounts(run([member({ status: 'ended' })]), [])).toEqual({ needs: 0, running: 0 })
  })
})

describe('runActive', () => {
  it('is true while the run is not stopped and a member has not ended', () => {
    expect(runActive(run([member({ status: 'ended' }), member({ name: 'b', status: 'pending' })]))).toBe(true)
    expect(runActive(run([member({ status: 'ended' })]))).toBe(false)
    expect(runActive({ ...run([member({ status: 'pending' })]), stoppedAt: '2026-09-29T11:00:00Z' })).toBe(false)
  })
})

describe('defaultCrew', () => {
  const agents: AgentInfo[] = [
    { id: 'claude', name: 'Claude Code', command: ['claude'], allowArgs: true },
    { id: 'shell', name: 'Shell', command: ['bash', '-l'], allowArgs: false },
  ]

  it('is a new crew on the server with a worktree per agent and one member on the first agent', () => {
    expect(wire(defaultCrew(agents))).toEqual({
      name: 'New crew',
      goal: '',
      cwd: '',
      where: 'server',
      isolation: 'worktree',
      openAfterLaunch: true,
      members: [{ name: 'lead', agentId: 'claude', prompt: '', start: { when: 'immediately' } }],
    })
  })

  it('has no member without an agent in the catalog', () => {
    expect(defaultCrew([]).members).toEqual([])
  })

  it('passes its own member names', () => {
    expect(memberNameError(defaultCrew(agents).members[0]!.name)).toBe('')
  })
})

describe('memberNameError', () => {
  it('accepts what the server accepts', () => {
    for (const n of ['lead', 'a', 'core-2', 'web_ui', 'v1.2', '0day', 'a'.repeat(40)]) expect(memberNameError(n), n).toBe('')
  })

  it('refuses names outside the pattern', () => {
    for (const n of ['', 'Lead', '-lead', '.lead', '_lead', 'le ad', 'le/ad', 'a'.repeat(41), 'é']) expect(memberNameError(n), n).not.toBe('')
  })

  it('refuses what git refuses in a branch name', () => {
    for (const n of ['a..b', 'lead.', 'lead.lock']) expect(memberNameError(n), n).toMatch(/git/)
  })

  it('refuses a name another member has', () => {
    expect(memberNameError('core', ['lead', 'core'])).toMatch(/another member/i)
    expect(memberNameError('core', ['lead'])).toBe('')
  })
})

describe('memberNameFrom', () => {
  it('makes a valid name from free text', () => {
    expect(memberNameFrom('Auth Refactor')).toBe('auth-refactor')
    expect(memberNameFrom('--weird..name.lock')).toBe('weird.name')
    expect(memberNameFrom('...')).toBe('agent')
    expect(memberNameFrom('x'.repeat(60))).toBe('x'.repeat(40))
  })

  it('avoids the names taken', () => {
    expect(memberNameFrom('claude', ['claude'])).toBe('claude-2')
    expect(memberNameFrom('claude', ['claude', 'claude-2'])).toBe('claude-3')
    const long = 'y'.repeat(40)
    const next = memberNameFrom(long, [long])
    expect(next).toBe(`${'y'.repeat(38)}-2`)
    expect(memberNameError(next)).toBe('')
  })
})

describe('startValue and startFrom', () => {
  it('round-trips a start condition through one select value', () => {
    const starts: CrewStart[] = [{ when: 'immediately' }, { when: 'manual' }, { when: 'after', member: 'core' }]
    for (const s of starts) expect(startFrom(startValue(s))).toEqual(s)
    expect(startValue({ when: 'after', member: 'core' })).toBe('after:core')
  })
})

describe('crewFeed', () => {
  const fe = (p: Partial<FeedEntry>): FeedEntry => ({
    at: '2026-09-29T10:05:00Z',
    type: 'progress',
    sessionId: 'a',
    event: 'progress',
    sessionName: 'lead',
    seq: 1,
    time: '10:05:00',
    detail: '4/7 handlers',
    link: null,
    ...p,
  })

  it('merges the run members events and the run log, newest first', () => {
    const items = crewFeed(
      [
        fe({ seq: 1, at: '2026-09-29T10:05:00.5Z' }),
        fe({ seq: 2, sessionId: 'zzz', sessionName: 'elsewhere' }),
        fe({ seq: 3, sessionId: 'b', sessionName: 'core', at: '2026-09-29T10:06:00Z', type: 'artifact', event: 'artifact', detail: '', url: 'https://x.test/pr/1', link: 'https://x.test/pr/1' }),
      ],
      [
        { at: '2026-09-29T10:00:00Z', type: 'status', message: 'launched API sweep: 2 members, 2 starting now' },
        { at: '2026-09-29T10:05:00.25Z', type: 'error', message: 'handoff to unknown member "x" from lead' },
      ],
      new Map([
        ['a', 'lead'],
        ['b', 'core'],
      ]),
    )
    expect(items.map((i) => [i.who, i.what])).toEqual([
      ['core', 'artifact'],
      ['lead', 'progress: 4/7 handlers'],
      ['', 'handoff to unknown member "x" from lead'],
      ['', 'launched API sweep: 2 members, 2 starting now'],
    ])
    expect(items[0]!.link).toBe('https://x.test/pr/1')
    expect(items[0]!.url).toBe('https://x.test/pr/1')
    expect(items[2]!.color).toBe('error')
    expect(items[3]!.color).toBe('neutral')
    expect(new Set(items.map((i) => i.key)).size).toBe(items.length)
  })

  it('keeps the newest entries up to the limit', () => {
    const log = Array.from({ length: 5 }, (_, i) => ({ at: `2026-09-29T10:0${i}:00Z`, type: 'status' as const, message: `m${i}` }))
    expect(crewFeed([], log, new Map(), 2).map((i) => i.what)).toEqual(['m4', 'm3'])
  })
})

describe('broadcastSummary', () => {
  it('names who got the line and who was skipped, and why', () => {
    expect(broadcastSummary({ sent: ['core', 'web'], skipped: [{ member: 'lead', reason: 'needs_input' }, { member: 'tests', reason: 'not_running' }] })).toEqual({
      title: 'Sent to 2 of 4',
      description: 'Sent to core, web. Skipped lead (waiting on a prompt), tests (not running).',
      color: 'warning',
    })
    expect(broadcastSummary({ sent: ['core'], skipped: [] })).toEqual({ title: 'Sent to 1 of 1', description: 'Sent to core.', color: 'success' })
    expect(broadcastSummary({ sent: [], skipped: [{ member: 'x', reason: 'unknown' }] })).toEqual({
      title: 'Sent to 0 of 1',
      description: 'Skipped x (not a member).',
      color: 'error',
    })
  })
})

describe('broadcastByName', () => {
  it('is the display name, without asking the server', async () => {
    let asked = 0
    const whoami = async () => {
      asked++
      return { user: 'nater' }
    }
    expect(await broadcastByName('  Nate  ', whoami)).toBe('Nate')
    expect(asked).toBe(0)
  })
  it("falls back to the server's OS user, as the display name defaults to", async () => {
    expect(await broadcastByName('', async () => ({ user: 'nater' }))).toBe('nater')
    expect(await broadcastByName('   ', async () => ({ user: ' nater ' }))).toBe('nater')
  })
  it('is undefined, recorded as guest, only when both are empty', async () => {
    expect(await broadcastByName('', async () => ({ user: '' }))).toBeUndefined()
    expect(
      await broadcastByName('', async () => {
        throw new Error('401')
      }),
    ).toBeUndefined()
  })
})

describe('toDraft and crewKey', () => {
  it('a draft of a saved crew is not a change until something is edited', () => {
    const d = toDraft(info)
    expect(d.id).toBe('api-sweep')
    expect(d.members.map((m) => m.argsText)).toEqual(['--model x', ''])
    expect(new Set(d.members.map((m) => m.key)).size).toBe(2)
    expect(crewKey(d)).toBe(crewKey(info))
    d.members[1]!.args = argsFrom('--verbose "two words"')
    expect(d.members[1]!.args).toEqual(['--verbose', 'two words'])
    expect(crewKey(d)).not.toBe(crewKey(info))
    d.members[1]!.args = argsFrom('  ')
    expect(crewKey(d)).toBe(crewKey(info))
  })
})

describe('sidebarRunFor', () => {
  const tag = { runId: 'api-sweep-1a2b3c4d', crewId: 'api-sweep', member: 'core' }
  const live = [session({ id: 's1', crew: tag }), session({ id: 's2' })]

  it('is the run of a crew view', () => {
    expect(sidebarRunFor('/runs/api-sweep-1a2b3c4d', live)).toBe('api-sweep-1a2b3c4d')
    expect(sidebarRunFor('/runs/a%20b', [])).toBe('a b')
  })

  it('is the run of a member session however it was reached', () => {
    expect(sidebarRunFor('/sessions/s1', live)).toBe('api-sweep-1a2b3c4d')
  })

  it('is nothing for a session outside a crew, one the store lacks, and every other page', () => {
    expect(sidebarRunFor('/sessions/s2', live)).toBeUndefined()
    expect(sidebarRunFor('/sessions/nope', live)).toBeUndefined()
    for (const p of ['/', '/wall', '/crews', '/crews/api-sweep', '/runs/', '/runs/x/y', '/sessions/s1/files']) expect(sidebarRunFor(p, live), p).toBeUndefined()
  })
})

describe('holdViewLink and takeViewLink', () => {
  it('hands a launch view link to its crew view once, and keeps no timer holding it once taken', () => {
    vi.useFakeTimers()
    try {
      holdViewLink('r1', 'https://x.test/join/t', 28800, 1000)
      expect(vi.getTimerCount()).toBe(1)
      expect(takeViewLink('r1', 2000)).toEqual({ url: 'https://x.test/join/t', ttlSeconds: 28800 })
      expect(vi.getTimerCount()).toBe(0)
      expect(takeViewLink('r1', 2000)).toBeNull()
    } finally {
      vi.useRealTimers()
    }
  })

  it('drops it when another run asks, or when nobody took it within a minute', () => {
    holdViewLink('r1', 'https://x.test/join/t', 28800, 1000)
    expect(takeViewLink('r2', 1500)).toBeNull()
    expect(takeViewLink('r1', 1500)).toBeNull()
    holdViewLink('r1', 'https://x.test/join/t', 28800, 1000)
    expect(takeViewLink('r1', 62_000)).toBeNull()
  })
})

describe('holdViewLink', () => {
  it('drops a link nobody took within a minute, without waiting for a take', () => {
    vi.useFakeTimers()
    try {
      holdViewLink('r1', 'https://x/join/t', 3600, 0)
      vi.advanceTimersByTime(60_001)
      // Taken "at" 0, within the hold: only the timer can have dropped it.
      expect(takeViewLink('r1', 0)).toBeNull()
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('RunNameAsks', () => {
  it('asks once, and again after a failure once the retry time has passed', () => {
    const asks = new RunNameAsks()
    expect(asks.shouldAsk('r1', 0)).toBe(true)
    expect(asks.shouldAsk('r1', 1)).toBe(false)
    asks.failed('r1', 10)
    expect(asks.shouldAsk('r1', 10 + RUN_NAME_RETRY_MS - 1)).toBe(false)
    expect(asks.shouldAsk('r1', 10 + RUN_NAME_RETRY_MS)).toBe(true)
    expect(asks.shouldAsk('r2', 0)).toBe(true)
  })

  it('never asks again for a run the server does not have, whatever fails later', () => {
    vi.useFakeTimers()
    try {
      const asks = new RunNameAsks()
      expect(asks.shouldAsk('r1')).toBe(true)
      asks.failed('r1')
      vi.advanceTimersByTime(RUN_NAME_RETRY_MS)
      expect(asks.shouldAsk('r1')).toBe(true)
      asks.gone('r1')
      asks.failed('r1')
      vi.advanceTimersByTime(RUN_NAME_RETRY_MS * 100)
      expect(asks.shouldAsk('r1')).toBe(false)
      expect(asks.shouldAsk('r2')).toBe(true)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('joinTileStatus', () => {
  it("prefers what the tile's terminal reported, and flags a prompt", () => {
    expect(joinTileStatus({ status: 'running' }, { attention: 'needs_input' })).toMatchObject({ label: 'Needs input' })
    expect(joinTileStatus({ status: 'starting' }, { status: 'running' })).toMatchObject({ label: 'Running' })
    expect(joinTileStatus({ status: 'ended' }, { attention: 'needs_input' })).toMatchObject({ label: 'ended' })
    expect(joinTileStatus({ status: 'pending' })).toMatchObject({ label: 'pending', cls: 'text-muted' })
  })
})
