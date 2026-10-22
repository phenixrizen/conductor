import { describe, expect, it } from 'vitest'
import type { SessionInfo } from '~/composables/useSessions'
import { nextNeeding, sameQuestions } from './needsCursor'

function asking(id: string, message: string, labels: string[], extra: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id,
    name: id,
    kind: 'server',
    agentId: 'claude',
    command: [],
    cwd: '/w',
    status: 'running',
    cols: 80,
    rows: 24,
    viewers: 0,
    createdAt: '2026-10-21T00:00:00Z',
    attention: { state: 'needs_input', message, options: labels.map((l) => ({ label: l, input: `${l}\r` })) },
    ...extra,
  } as SessionInfo
}

describe('the needs-you cursor', () => {
  it('starts at the first (or the last going back), steps from the page’s session, wraps, and is -1 with none', () => {
    expect(nextNeeding([], null, 1)).toBe(-1)
    expect(nextNeeding(['a', 'b', 'c'], null, 1)).toBe(0)
    expect(nextNeeding(['a', 'b', 'c'], null, -1)).toBe(2)
    expect(nextNeeding(['a', 'b', 'c'], 'b', 1)).toBe(2)
    expect(nextNeeding(['a', 'b', 'c'], 'c', 1)).toBe(0)
    expect(nextNeeding(['a', 'b', 'c'], 'a', -1)).toBe(2)
    expect(nextNeeding(['a', 'b', 'c'], 'zzz', 1)).toBe(0)
  })
})

describe('answer all', () => {
  it('groups the same question asked by several sessions, by its message and choices, and leaves single or free-text ones', () => {
    const list = [
      asking('a', 'Allow Bash?', ['Yes', 'No']),
      asking('b', 'Allow Bash?', ['Yes', 'No']),
      asking('c', 'Allow Bash?', ['Yes', 'Always', 'No']),
      asking('d', 'Which database?', ['Postgres', 'SQLite']),
      asking('e', 'Name it', []),
      asking('f', 'Name it', []),
      asking('g', 'Allow Bash?', ['Yes', 'No'], { attention: { state: 'needs_input', message: 'Allow Bash?', options: [{ label: 'Yes', input: 'Yes\r' }, { label: 'No', input: 'No\r' }], answered: 'terminal' } }),
    ]
    const groups = sameQuestions(list)
    expect(groups).toHaveLength(1)
    expect(groups[0]!.message).toBe('Allow Bash?')
    expect(groups[0]!.sessions.map((s) => s.id)).toEqual(['a', 'b'])
    expect(groups[0]!.choices.map((o) => o.label)).toEqual(['Yes', 'No'])
  })
})
