import { describe, expect, it } from 'vitest'
import type { SessionInfo } from '~/composables/useSessions'
import { liveAttention, liveSession, ATTENTION_DOT_COLOR, isActive, needingInput, newlyNeedingInput, titleWithCount, waitingHeading, waitsForTerminal } from './attention'

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

describe('attention dot colour', () => {
  it('is the amber status colour, never the brand terracotta', () => {
    expect(ATTENTION_DOT_COLOR).toBe('#c98a1b')
    expect(ATTENTION_DOT_COLOR).not.toBe('#d26b3f')
  })
})

describe('a turn held for the terminal', () => {
  it('is a needs_input of source submit alone', () => {
    expect(waitsForTerminal({ state: 'needs_input', source: 'submit' })).toBe(true)
    expect(waitsForTerminal({ state: 'needs_input', source: 'api' })).toBe(false)
    expect(waitsForTerminal({ state: 'needs_input' })).toBe(false)
    expect(waitsForTerminal({ state: 'done', source: 'submit' })).toBe(false)
    expect(waitsForTerminal({ state: '', source: 'submit' })).toBe(false)
    expect(waitsForTerminal(undefined)).toBe(false)
  })

  it('heads the reply bar with the terminal, else with the agent', () => {
    expect(waitingHeading({ state: 'needs_input', source: 'submit' }, 'claude')).toBe('Type in the terminal to go on')
    expect(waitingHeading({ state: 'needs_input', source: 'api' }, 'claude')).toBe('claude is waiting on you')
    expect(waitingHeading(undefined, 'codex')).toBe('codex is waiting on you')
  })
})

describe('a question answered in the terminal (#258)', () => {
  it('reads as no question to offer, the mark kept, and every other attention as itself', () => {
    const answered = { state: 'needs_input' as const, message: 'Allow?', source: 'question', id: 'q1', options: [{ label: 'Yes', input: 'y' }], answered: 'terminal' as const }
    expect(liveAttention(answered)).toEqual({ state: '', source: 'input', answered: 'terminal' })
    const open = { state: 'needs_input' as const, message: 'Allow?', source: 'question', id: 'q1' }
    expect(liveAttention(open)).toBe(open)
    const working = { state: 'working' as const, source: 'title' }
    expect(liveAttention(working)).toBe(working)
    expect(liveAttention(undefined)).toBeUndefined()
    const s = { id: 'a', name: 'a', agentId: 'codex', status: 'running', createdAt: '', kind: 'server', attention: answered } as unknown as SessionInfo
    expect(liveSession(s).attention).toEqual({ state: '', source: 'input', answered: 'terminal' })
    const t = { ...s, attention: open } as unknown as SessionInfo
    expect(liveSession(t)).toBe(t)
  })
})
