import { describe, expect, it } from 'vitest'
import type { FileHeader } from './protocol'
import { commitAgainst, commitAgo, commitChangeRows, commitRows, commitsHead } from './commits'

const log: FileHeader = {
  reqId: 'r', path: '/r/repo', kind: 'log', exists: true, branch: 'main', since: '2026-10-09T08:31:00Z',
  commits: [
    { sha: 'a'.repeat(40), short: 'aaaaaaa', subject: 'users: tests', author: 'Ada', at: '2026-10-09T08:40:00Z', parent: 'b'.repeat(40) },
    { sha: 'b'.repeat(40), short: '', subject: '', at: 'not a time' },
  ],
}

describe('the Commits section', () => {
  it('lists the commits newest first as the log gave them', () => {
    const rows = commitRows(log)
    expect(rows.map((r) => r.short)).toEqual(['aaaaaaa', 'bbbbbbb'])
    expect(rows[0]).toMatchObject({ subject: 'users: tests', author: 'Ada', parent: 'b'.repeat(40), atMs: Date.parse('2026-10-09T08:40:00Z') })
    expect(rows[1]).toMatchObject({ subject: '(no subject)', author: '', atMs: 0, parent: '' })
    expect(commitRows(null)).toEqual([])
    expect(commitRows({ ...log, kind: 'status' })).toEqual([])
  })
  it('words the head', () => {
    expect(commitsHead(2, log)).toMatch(/^2 commits on main since \d{2}:\d{2}$/)
    expect(commitsHead(1, { ...log, since: undefined })).toBe('1 commit on main since the session started')
    expect(commitsHead(0, { ...log, branch: 'crew/core', base: '3f2a1c4' }, 'main')).toBe('no commits on crew/core since main @ 3f2a1c4')
  })
  it('says how long ago', () => {
    const now = Date.parse('2026-10-09T12:00:00Z')
    expect(commitAgo(now - 10_000, now)).toBe('just now')
    expect(commitAgo(now - 5 * 60_000, now)).toBe('5m ago')
    expect(commitAgo(now - 3 * 3600_000, now)).toBe('3h ago')
    expect(commitAgo(now - 72 * 3600_000, now)).toBe('3d ago')
  })
  it('lists a commit\'s files with a rename\'s old path', () => {
    const rows = commitChangeRows({ reqId: 'r', path: '/r/repo', kind: 'commit', exists: true, changes: [
      { path: 'internal/api/users.go', status: 'M', added: 2, removed: 1 },
      { path: 'docs/new.md', from: 'docs/old.md', status: 'R' },
    ] })
    expect(rows[0]).toMatchObject({ abs: '/r/repo/internal/api/users.go', dir: 'internal/api/', name: 'users.go', added: 2, removed: 1 })
    expect(rows[0]!.fromAbs).toBeUndefined()
    expect(rows[1]).toMatchObject({ abs: '/r/repo/docs/new.md', fromAbs: '/r/repo/docs/old.md', status: 'R' })
  })
  it('words what a commit\'s diff is against', () => {
    expect(commitAgainst('aaaaaaa', 'b'.repeat(40))).toBe('aaaaaaa vs its parent bbbbbbb')
    expect(commitAgainst('aaaaaaa', '')).toBe('aaaaaaa, the first commit')
  })
})
