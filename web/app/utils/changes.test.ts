import { describe, expect, it } from 'vitest'
import { agoWords, changeMarks, changeRows, changesTitle, diffAgainst, statusLetter, statusTone } from './changes'

describe('the Changes section', () => {
  const changes = [
    { path: 'internal/api/users.go', status: 'M' as const, added: 42, removed: 6 },
    { path: 'internal/api/users_test.go', status: '?' as const, added: 118 },
    { path: 'docs/old.md', status: 'D' as const, removed: 9 },
    { path: 'go.mod', status: 'R' as const, added: 1 },
  ]
  it('lists each change with its folder, name and lines, absolute under the top', () => {
    const rows = changeRows('/r/', changes)
    expect(rows[0]).toEqual({ path: 'internal/api/users.go', abs: '/r/internal/api/users.go', dir: 'internal/api/', name: 'users.go', status: 'M', added: 42, removed: 6, binary: false })
    expect(rows[3]).toMatchObject({ dir: '', name: 'go.mod', abs: '/r/go.mod', added: 1, removed: 0 })
  })
  it('marks the Explorer with the status letter, a rename as modified', () => {
    const marks = changeMarks('/r', changes)
    expect(marks.get('/r/internal/api/users.go')).toBe('M')
    expect(marks.get('/r/go.mod')).toBe('M')
    expect(marks.get('/r/docs/old.md')).toBe('D')
    expect(marks.has('/r/README.md')).toBe(false)
  })
  it('colours and letters the statuses, untracked files as additions', () => {
    expect(statusTone('M')).toBe('warning')
    expect(statusTone('A')).toBe('success')
    expect(statusTone('D')).toBe('error')
    expect(statusTone('?')).toBe('neutral')
    expect(statusLetter('?')).toBe('A')
    expect(statusLetter('R')).toBe('R')
  })
  it('says how many and how long ago, and what a diff is against', () => {
    expect(changesTitle(0)).toBe('no changes')
    expect(changesTitle(1)).toBe('1 file')
    expect(changesTitle(4)).toBe('4 files')
    expect(agoWords(500)).toBe('just now')
    expect(agoWords(8000)).toBe('8s ago')
    expect(agoWords(130_000)).toBe('2m ago')
    expect(agoWords(7_200_000)).toBe('2h ago')
    expect(diffAgainst({ branch: 'main', base: '3f2a1c' })).toBe('working directory vs HEAD')
    expect(diffAgainst({ branch: 'crew/users-api/core', base: '3f2a1c' }, 'main')).toBe('crew/users-api/core vs main @ 3f2a1c')
  })
})
