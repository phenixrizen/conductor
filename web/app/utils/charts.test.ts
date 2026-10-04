import { describe, expect, it } from 'vitest'
import type { RunInfo, SessionInfo } from '~/composables/useSessions'
import { attentionSlices, enough, roleColors, runLength } from './charts'

const T0 = Date.parse('2026-10-03T10:30:00Z')
const iso = (offsetS: number) => new Date(T0 + offsetS * 1000).toISOString()

describe('enough', () => {
  it('needs two points', () => {
    expect(enough(0)).toBe(false)
    expect(enough(1)).toBe(false)
    expect(enough(2)).toBe(true)
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

describe('runLength', () => {
  it('measures a run to its stop, to its last member\'s end, or to now while it goes', () => {
    const now = T0 + 600_000
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
