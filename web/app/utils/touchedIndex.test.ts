import { describe, expect, it } from 'vitest'
import { absUnder, mergeTouched, relUnder, TOUCHED_ROWS_MAX, type FeedFile, type TouchedSession } from './touchedIndex'

describe('the Touched index across sessions (#304)', () => {
  const index: TouchedSession[] = [
    {
      id: 'a',
      name: 'api sweep',
      agentId: 'claude',
      kind: 'server',
      status: 'running',
      cwd: '/r/api',
      touched: [
        { path: '/r/api/internal/users.go', op: 'edit', ops: ['read', 'edit'], count: 3, tool: 'Edit', by: 'claude', first: '2026-10-22T08:00:00Z', last: '2026-10-22T08:10:00Z' },
        { path: '/r/api/README.md', op: 'read', count: 1, tool: 'Read', by: 'claude', first: '2026-10-22T08:01:00Z', last: '2026-10-22T08:01:00Z' },
      ],
    },
    {
      id: 'b',
      name: 'docs',
      agentId: 'codex',
      kind: 'hosted',
      status: 'running',
      cwd: '/r/docs/',
      touched: [{ path: '/r/docs/guide.md', op: 'write', count: 1, tool: 'Write', by: 'codex', first: '2026-10-22T08:05:00Z', last: '2026-10-22T08:05:00Z' }],
    },
  ]

  it('lists one row per session and file, the most recently touched first, the path from the working directory', () => {
    const rows = mergeTouched(index, [])
    expect(rows.map((r) => `${r.sessionName}:${r.rel}`)).toEqual(['api sweep:internal/users.go', 'docs:guide.md', 'api sweep:README.md'])
    expect(rows[0]).toMatchObject({ sessionId: 'a', path: '/r/api/internal/users.go', op: 'edit', tool: 'Edit', by: 'claude', count: 3, at: '2026-10-22T08:10:00Z' })
  })

  it('lays the feed over the index: a newer entry moves a row and counts, an older one is already in it, an unknown file is a row of its own', () => {
    const feed: FeedFile[] = [
      // Older than the index's latest touch of the file: already counted.
      { sessionId: 'a', sessionName: 'api sweep', cwd: '/r/api', path: 'internal/users.go', op: 'read', tool: 'Read', by: 'claude', at: '2026-10-22T08:09:00Z' },
      // Newer: the row moves to it.
      { sessionId: 'a', sessionName: 'api sweep', cwd: '/r/api', path: './internal/users.go', op: 'write', tool: 'Write', by: 'claude', at: '2026-10-22T08:20:00Z' },
      // A file the index lacks (its session's working directory from the index when the entry has none).
      { sessionId: 'b', sessionName: 'docs', path: 'intro.md', op: 'edit', tool: 'Edit', by: 'codex', at: '2026-10-22T08:15:00Z' },
      // Not a file op, or no path: nothing.
      { sessionId: 'b', sessionName: 'docs', path: 'x', op: 'typed', at: '2026-10-22T08:30:00Z' },
      { sessionId: 'b', sessionName: 'docs', path: '', op: 'edit', at: '2026-10-22T08:30:00Z' },
    ]
    const rows = mergeTouched(index, feed)
    expect(rows.map((r) => `${r.sessionId}:${r.rel}@${r.at}`)).toEqual(['a:internal/users.go@2026-10-22T08:20:00Z', 'b:intro.md@2026-10-22T08:15:00Z', 'b:guide.md@2026-10-22T08:05:00Z', 'a:README.md@2026-10-22T08:01:00Z'])
    expect(rows[0]).toMatchObject({ op: 'write', tool: 'Write', count: 4 })
    expect(rows[1]).toMatchObject({ path: '/r/docs/intro.md', cwd: '/r/docs/', count: 1 })
  })

  it('a session the index does not know: the entry stands alone, its path as named', () => {
    const rows = mergeTouched([], [{ sessionId: 'z', sessionName: 'new', path: 'a.go', op: 'edit', at: '2026-10-22T08:00:00Z' }])
    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({ sessionId: 'z', path: 'a.go', rel: 'a.go', cwd: '' })
  })

  it('is bounded', () => {
    const many: TouchedSession = { id: 'm', name: 'many', agentId: 'x', kind: 'server', status: 'running', cwd: '/m', touched: [] }
    for (let i = 0; i < TOUCHED_ROWS_MAX + 5; i++) many.touched.push({ path: `/m/f${i}.go`, op: 'read', count: 1, first: '2026-10-22T08:00:00Z', last: `2026-10-22T08:00:${String(i % 60).padStart(2, '0')}Z` })
    expect(mergeTouched([many], [])).toHaveLength(TOUCHED_ROWS_MAX)
    expect(mergeTouched([many], [], 3)).toHaveLength(3)
  })

  it('keeps a Windows working directory’s own separator (a hosted session on another machine)', () => {
    expect(absUnder('C:\\Users\\me\\proj', 'src/a.go')).toBe('C:\\Users\\me\\proj\\src\\a.go')
    expect(absUnder('C:\\Users\\me\\proj', 'D:\\other\\b.go')).toBe('D:\\other\\b.go')
    expect(relUnder('C:\\Users\\me\\proj', 'C:\\Users\\me\\proj\\src\\a.go')).toBe('src\\a.go')
    expect(relUnder('/r/api', '/elsewhere/x.go')).toBe('/elsewhere/x.go')
    expect(absUnder('/r/api/', '../shared/./x.go')).toBe('/r/shared/x.go')
  })
})
