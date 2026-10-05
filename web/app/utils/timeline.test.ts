import { describe, expect, it } from 'vitest'
import type { RunInfo, SessionInfo } from '~/composables/useSessions'
import type { FeedEntry } from './events'
import { durationLabel, scaleX, tickLabel, ticks, timelineOf, waitsOf } from './timeline'

const T0 = Date.parse('2026-10-03T10:00:00Z')
const iso = (offsetS: number) => new Date(T0 + offsetS * 1000).toISOString()

function entry(sessionId: string, event: FeedEntry['event'], offsetS: number, detail = ''): FeedEntry {
  return { at: iso(offsetS), type: 'attention', sessionId, event, sessionName: sessionId, seq: offsetS, time: '', detail, link: null }
}

const run: RunInfo = {
  id: 'team-1a2b3c4d',
  crewId: 'team',
  name: 'team',
  goal: 'g',
  cwd: '/w',
  isolation: 'none',
  startedAt: iso(0),
  members: [
    { name: 'lead', agentId: 'claude', start: { when: 'immediately' }, status: 'running', sessionId: 's-lead', startedAt: iso(1) },
    { name: 'tests', agentId: 'codex', start: { when: 'after', member: 'lead' }, status: 'ended', sessionId: 's-tests', startedAt: iso(60), endedAt: iso(200), error: 'exited (exit 1)' },
    { name: 'docs', agentId: 'claude', start: { when: 'manual' }, status: 'pending' },
  ],
  log: [{ at: iso(120), type: 'status', message: 'handoff delivered from lead to tests', byName: 'lead', to: 'tests' }],
  state: 'running',
  needsInput: 0,
  yolo: false,
}

const sessions: SessionInfo[] = []

describe('waitsOf', () => {
  it('opens a segment on needs_input and closes it on working or done; an open one ends now', () => {
    const now = T0 + 300_000
    const waits = waitsOf([entry('s', 'needs_input', 10, 'Trust this folder?'), entry('s', 'working', 40), entry('s', 'done', 50), entry('s', 'needs_input', 100)], T0, now)
    expect(waits).toEqual([
      { from: T0 + 10_000, to: T0 + 40_000, reason: 'Trust this folder?', open: false },
      { from: T0 + 100_000, to: now, reason: undefined, open: true },
    ])
  })

  it('ignores entries before the member started and a second needs_input while one is open', () => {
    const waits = waitsOf([entry('s', 'needs_input', 1), entry('s', 'needs_input', 20), entry('s', 'needs_input', 25), entry('s', 'done', 30)], T0 + 10_000, T0 + 60_000)
    expect(waits).toEqual([{ from: T0 + 20_000, to: T0 + 30_000, reason: undefined, open: false }])
  })
})

describe('timelineOf', () => {
  it('draws a bar per member, waits from the feed, handoff marks and the open end at now', () => {
    const now = T0 + 300_000
    const feed = [entry('s-lead', 'needs_input', 30, 'Which database?'), entry('s-lead', 'working', 45), entry('s-tests', 'needs_input', 70), entry('other', 'needs_input', 80)]
    const tl = timelineOf(run, sessions, feed, now)
    expect(tl.from).toBe(T0)
    expect(tl.to).toBe(now)
    expect(tl.stoppedAt).toBeUndefined()
    const [lead, tests, docs] = tl.rows
    expect(lead).toMatchObject({ name: 'lead', start: T0 + 1000, end: undefined, status: 'running' })
    expect(lead!.waits).toEqual([{ from: T0 + 30_000, to: T0 + 45_000, reason: 'Which database?', open: false }])
    expect(lead!.handoffs).toEqual([{ at: T0 + 120_000, to: 'tests', message: undefined }])
    // tests ended with an error; its open wait is cut at its end.
    expect(tests).toMatchObject({ name: 'tests', start: T0 + 60_000, end: T0 + 200_000, status: 'ended', error: 'exited (exit 1)' })
    expect(tests!.waits).toEqual([{ from: T0 + 70_000, to: T0 + 200_000, reason: undefined, open: true }])
    expect(docs).toMatchObject({ name: 'docs', start: undefined, end: undefined, status: 'pending', waits: [], handoffs: [] })
  })

  it('ends at the stop line when the run was stopped', () => {
    const stopped: RunInfo = { ...run, stoppedAt: iso(250), state: 'stopped', members: run.members.map((m) => (m.name === 'lead' ? { ...m, status: 'ended' as const } : m)) }
    const tl = timelineOf(stopped, sessions, [], T0 + 900_000)
    expect(tl.stoppedAt).toBe(T0 + 250_000)
    expect(tl.to).toBe(T0 + 250_000)
    expect(tl.rows[0]!.end).toBe(T0 + 250_000)
  })

  it('draws the wait a live session is in now from its attention state, when the feed missed its start', () => {
    const now = T0 + 300_000
    const live: SessionInfo[] = [{ id: 's-lead', name: 'lead', agentId: 'claude', kind: 'server', status: 'running', createdAt: iso(1), cwd: '/w', viewers: 0, crew: { runId: run.id, crewId: 'team', member: 'lead' }, attention: { state: 'needs_input', since: iso(200), message: 'Trust this folder?', source: 'trust' } } as unknown as SessionInfo]
    const tl = timelineOf(run, live, [], now)
    expect(tl.rows[0]!.waits).toEqual([{ from: T0 + 200_000, to: now, reason: 'Trust this folder?', open: true }])
    // With the feed's own open wait, nothing is added twice.
    const withFeed = timelineOf(run, live, [entry('s-lead', 'needs_input', 150, 'Trust this folder?')], now)
    expect(withFeed.rows[0]!.waits).toHaveLength(1)
    expect(withFeed.rows[0]!.waits[0]!.from).toBe(T0 + 150_000)
  })

  it('reads a member session that ended from the live store', () => {
    const live: SessionInfo[] = [{ id: 's-lead', name: 'lead', agentId: 'claude', kind: 'server', status: 'exited', createdAt: iso(1), endedAt: iso(90), cwd: '/w', viewers: 0, crew: { runId: run.id, crewId: 'team', member: 'lead' } } as unknown as SessionInfo]
    const tl = timelineOf(run, live, [], T0 + 300_000)
    expect(tl.rows[0]).toMatchObject({ end: T0 + 90_000, status: 'ended' })
  })
})

describe('scale, ticks and labels', () => {
  it('scales within the width and clamps', () => {
    expect(scaleX(T0 + 50_000, T0, T0 + 100_000, 200)).toBe(100)
    expect(scaleX(T0 - 1, T0, T0 + 100_000, 200)).toBe(0)
    expect(scaleX(T0 + 200_000, T0, T0 + 100_000, 200)).toBe(200)
    expect(scaleX(T0, T0, T0, 200)).toBe(0)
  })

  it('picks a step that gives at most eight ticks', () => {
    expect(ticks(T0, T0 + 4 * 60_000)).toHaveLength(5)
    expect(ticks(T0, T0 + 30 * 60_000).length).toBeLessThanOrEqual(8)
    expect(ticks(T0, T0 + 5 * 3_600_000).length).toBeLessThanOrEqual(8)
    expect(ticks(T0, T0 + 10 * 24 * 3_600_000).length).toBeGreaterThan(0)
  })

  it('labels moments and durations', () => {
    expect(tickLabel(T0 + 65_000, 60_000)).toMatch(/^\d\d:\d\d:05$/)
    expect(tickLabel(T0, 3_600_000)).toMatch(/^\d\d:\d\d$/)
    expect(durationLabel(12_000)).toBe('12s')
    expect(durationLabel(245_000)).toBe('4m 05s')
    expect(durationLabel(4_320_000)).toBe('1h 12m')
  })
})
