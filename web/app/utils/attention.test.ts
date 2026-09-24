import { describe, expect, it } from 'vitest'
import type { SessionInfo } from '~/composables/useSessions'
import { isActive, needingInput, newlyNeedingInput, titleWithCount } from './attention'

function s(id: string, state: '' | 'needs_input' | 'working' = '', since = '', status: SessionInfo['status'] = 'running'): SessionInfo {
  return {
    id,
    name: id,
    kind: 'server',
    agentId: 'shell',
    command: ['sh'],
    cwd: '/',
    status,
    cols: 80,
    rows: 24,
    viewers: 0,
    attention: { state, since: since || undefined },
    createdAt: '2026-09-24T00:00:00Z',
  }
}

describe('attention helpers', () => {
  it('lists sessions needing input, newest first, ignoring ended ones', () => {
    const list = needingInput([s('a', 'needs_input', '2026-01-01T00:00:01Z'), s('b', 'needs_input', '2026-01-01T00:00:05Z'), s('c', 'needs_input', '2026-01-01T00:00:09Z', 'exited'), s('d', 'working')])
    expect(list.map((x) => x.id)).toEqual(['b', 'a'])
  })

  it('detects new requests exactly once per signal', () => {
    const prev = new Map([['a', s('a', 'needs_input', 't1')], ['b', s('b')]])
    const next = new Map([['a', s('a', 'needs_input', 't1')], ['b', s('b', 'needs_input', 't2')]])
    expect(newlyNeedingInput(prev, next).map((x) => x.id)).toEqual(['b'])
    // a new signal on the same session (different since) counts again
    const next2 = new Map([['a', s('a', 'needs_input', 't3')]])
    expect(newlyNeedingInput(next, next2).map((x) => x.id)).toEqual(['a'])
    // first snapshot fires for everything already waiting
    expect(newlyNeedingInput(new Map(), next).map((x) => x.id)).toEqual(['a', 'b'])
  })

  it('formats the title and active state', () => {
    expect(titleWithCount('Conductor', 0)).toBe('Conductor')
    expect(titleWithCount('Conductor', 3)).toBe('(3) Conductor')
    expect(isActive(s('a'))).toBe(true)
    expect(isActive(s('a', '', '', 'stopped'))).toBe(false)
  })
})
