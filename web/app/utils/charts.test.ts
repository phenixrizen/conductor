import { describe, expect, it } from 'vitest'
import type { RunInfo, SessionInfo } from '~/composables/useSessions'
import { activityBuckets, activityTotal, attentionSlices, enough, outcomeOf, roleColors, runBars, runBarSeries, runLength } from './charts'

const T0 = Date.parse('2026-10-03T10:30:00Z')
const iso = (offsetS: number) => new Date(T0 + offsetS * 1000).toISOString()

describe('enough', () => {
  it('needs two points', () => {
    expect(enough(0)).toBe(false)
    expect(enough(1)).toBe(false)
    expect(enough(2)).toBe(true)
  })
})

describe('activityBuckets', () => {
  it('counts the feed per minute into groups over the window, oldest first, with empty minutes kept', () => {
    const now = T0 + 30_000 // 10:30:30
    const buckets = activityBuckets(
      [
        { at: iso(5), event: 'needs_input' },
        { at: iso(10), event: 'done' },
        { at: iso(-70), event: 'tool_use' },
        { at: iso(-65), event: 'tool_denied' },
        { at: iso(-65), event: 'handoff' },
        { at: iso(-100), event: 'error' },
        { at: iso(-100), event: 'exit_nonzero' },
        { at: iso(-100), event: 'progress' },
        { at: iso(-100), event: 'artifact' },
        { at: iso(-125), event: 'error' },
        { at: iso(-61 * 60), event: 'error' },
        { at: iso(90), event: 'error' },
        { at: 'nope', event: 'error' },
      ],
      now,
      3,
    )
    // The window: 10:28, 10:29 and 10:30 (now is 10:30:30); 10:27:55 and the hour before are out, and so is 10:31:30.
    expect(buckets.map((b) => b.minute)).toEqual([T0 - 120_000, T0 - 60_000, T0])
    expect(buckets[2]).toEqual({ minute: T0, attention: 2, reports: 0, handoff: 0, tool: 0, error: 0 })
    expect(buckets[1]).toEqual({ minute: T0 - 60_000, attention: 0, reports: 0, handoff: 0, tool: 0, error: 0 })
    expect(buckets[0]).toEqual({ minute: T0 - 120_000, attention: 0, reports: 2, handoff: 1, tool: 2, error: 2 })
    expect(activityTotal(buckets)).toBe(9)
    expect(activityBuckets([], now).length).toBe(60)
  })
})

const run = (id: string, state: RunInfo['state'], startS: number, extra: Partial<RunInfo> = {}): RunInfo => ({
  id,
  crewId: 'team',
  name: 'team',
  goal: 'g',
  cwd: '/w',
  isolation: 'none',
  startedAt: iso(startS),
  members: [],
  log: [],
  state,
  needsInput: 0,
  yolo: false,
  ...extra,
})

describe('run bars', () => {
  it('reads the outcome and the length of a run', () => {
    const now = T0 + 600_000
    expect(outcomeOf(run('a', 'running', 0))).toBe('running')
    expect(outcomeOf(run('a', 'needs_input', 0))).toBe('running')
    expect(outcomeOf(run('a', 'stopped', 0, { stoppedAt: iso(100) }))).toBe('stopped')
    expect(outcomeOf(run('a', 'finished', 0))).toBe('finished')
    expect(runLength(run('a', 'running', 0), now)).toBe(600_000)
    expect(runLength(run('a', 'stopped', 0, { stoppedAt: iso(100) }), now)).toBe(100_000)
    const finished = run('a', 'finished', 0, {
      members: [
        { name: 'x', agentId: 'c', start: { when: 'immediately' }, status: 'ended', endedAt: iso(50) },
        { name: 'y', agentId: 'c', start: { when: 'immediately' }, status: 'ended', endedAt: iso(80) },
      ],
    })
    expect(runLength(finished, now)).toBe(80_000)
    expect(runLength({ ...finished, startedAt: 'nope' }, now)).toBe(0)
  })

  it('takes the last twenty runs, oldest first, with a series per outcome', () => {
    const now = T0 + 3_600_000
    const runs = Array.from({ length: 25 }, (_, i) => run(`r${i}`, i === 0 ? 'running' : i % 2 ? 'finished' : 'stopped', -i * 60, i === 0 ? {} : { stoppedAt: i % 2 ? undefined : iso(-i * 60 + 30), needsInput: i }))
    const bars = runBars(runs, now)
    expect(bars).toHaveLength(20)
    expect(bars[0]!.id).toBe('r19')
    expect(bars.at(-1)!).toMatchObject({ id: 'r0', outcome: 'running', minutes: 60 })
    const series = runBarSeries(bars)
    expect(series.at(-1)).toMatchObject({ id: 'r0', running: 60, finished: 0, stopped: 0 })
    // A finished run without its members' ends is measured to now.
    expect(series[0]).toMatchObject({ id: 'r19', finished: 79, stopped: 0, running: 0 })
    const stopped = series.find((s) => s.id === 'r2')!
    expect(stopped.stopped).toBe(0.5)
    expect(stopped.needsInput).toBe(2)
  })
})

describe('attentionSlices', () => {
  it('counts active sessions by state', () => {
    const s = (id: string, state?: string, ended = false) => ({ id, status: ended ? 'exited' : 'running', attention: state ? { state } : undefined }) as unknown as SessionInfo
    const slices = attentionSlices([s('a', 'needs_input'), s('b', 'working'), s('c', 'done'), s('d'), s('e', 'needs_input', true)], (x) => x.status === 'running')
    expect(slices).toEqual({ needs_input: 1, working: 1, done: 1, idle: 1 })
  })
})

describe('roleColors', () => {
  it('reads the theme variables and falls back to the brand anchors', () => {
    const colors = roleColors((n) => ({ '--ui-warning': ' #ffaa00 ', '--ui-info': 'var(--color-harbor-500)' })[n] ?? '')
    expect(colors.warning).toBe('#ffaa00')
    expect(colors.info).toBe('#245d85')
    expect(colors.primary).toBe('#4a6a5a')
  })
})
