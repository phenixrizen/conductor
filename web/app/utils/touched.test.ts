import { describe, expect, it } from 'vitest'
import { opIcon, touchedAbs, touchedPaths, touchedRows, touchedWords } from './touched'

describe('the Touched section', () => {
  const entries = [
    { at: '2026-10-08T08:31:22Z', type: 'file' as const, op: 'delete' as const, path: 'internal/api/users_old.go', tool: 'Bash', byName: 'codex' },
    { at: '2026-10-08T08:32:05Z', type: 'tool_use' as const, tool: 'Read' },
    { at: '2026-10-08T08:32:05Z', type: 'file' as const, op: 'read' as const, path: '/r/internal/api/router.go', tool: 'Read', byName: 'codex' },
    { at: '2026-10-08T08:33:12Z', type: 'file' as const, op: 'edit' as const, path: './internal/api/users.go', tool: 'Edit', byName: 'codex' },
    { at: '2026-10-08T08:33:40Z', type: 'file' as const, op: 'edit' as const, path: '', tool: 'Edit' },
  ]
  it('lists the file events newest first, with the folder, the name, the tool and the agent, paths made absolute', () => {
    const rows = touchedRows(entries, '/r/')
    expect(rows.map((r) => r.abs)).toEqual(['/r/internal/api/users.go', '/r/internal/api/router.go', '/r/internal/api/users_old.go'])
    expect(rows[0]).toMatchObject({ dir: 'internal/api/', name: 'users.go', op: 'edit', tool: 'Edit', by: 'codex' })
    expect(touchedPaths(rows).has('/r/internal/api/router.go')).toBe(true)
    expect(touchedPaths(rows).has('/r/README.md')).toBe(false)
  })
  it('names the op by an icon and the row by its tool, agent and time', () => {
    expect(opIcon('read')).toBe('i-lucide-eye')
    expect(opIcon('edit')).toBe('i-lucide-pencil')
    expect(opIcon('write')).toBe('i-lucide-file-plus')
    expect(opIcon('delete')).toBe('i-lucide-trash-2')
    const row = touchedRows(entries, '/r')[0]!
    expect(touchedWords(row)).toMatch(/^Edit · codex · \d{2}:\d{2}:\d{2}$/)
    expect(touchedWords({ ...row, tool: '', by: '' })).toMatch(/^edit · \d{2}:\d{2}:\d{2}$/)
    expect(touchedWords({ ...row, count: 4 })).toMatch(/^Edit · codex · \d{2}:\d{2}:\d{2} · 4 times$/)
  })
})

describe('Touched, one row per file for the whole session (round 13, G1)', () => {
  it('makes paths absolute the way the server keys them', () => {
    expect(touchedAbs('./internal/../README.md', '/r/')).toBe('/r/README.md')
    expect(touchedAbs('/r//a/./b.go', '/x')).toBe('/r/a/b.go')
  })
  it('groups the touches of a file: the latest op, tool and agent, every op seen, the count, the first and latest times', () => {
    const rows = touchedRows(
      [
        { at: '2026-10-09T08:00:00Z', type: 'file', op: 'read', path: 'a.go', tool: 'Read', byName: 'claude' },
        { at: '2026-10-09T08:00:05Z', type: 'file', op: 'edit', path: '/r/a.go', tool: 'Edit', byName: 'claude' },
        { at: '2026-10-09T08:00:07Z', type: 'file', op: 'write', path: '/tmp/out.txt', tool: 'Write', byName: 'claude' },
      ],
      '/r',
    )
    expect(rows).toHaveLength(2)
    expect(rows[0]).toMatchObject({ abs: '/tmp/out.txt', dir: '/tmp/', name: 'out.txt', count: 1 })
    expect(rows[1]).toMatchObject({ abs: '/r/a.go', dir: '', name: 'a.go', op: 'edit', ops: ['read', 'edit'], count: 2, tool: 'Edit', first: '2026-10-09T08:00:00Z', at: '2026-10-09T08:00:05Z' })
  })
  it("starts from the session's index and counts only what came after it", () => {
    const server = [
      { path: '/r/a.go', op: 'edit' as const, ops: ['read' as const, 'edit' as const], count: 3, tool: 'Edit', by: 'codex', first: '2026-10-09T07:00:00Z', last: '2026-10-09T08:00:00.5Z' },
      { path: '/r/old.go', op: 'read' as const, count: 1, first: '2026-10-09T06:00:00Z', last: '2026-10-09T06:00:00Z' },
    ]
    const rows = touchedRows(
      [
        // The replay: in the index already (no newer than its latest touch).
        { at: '2026-10-09T08:00:00.5Z', type: 'file', op: 'edit', path: 'a.go', tool: 'Edit', byName: 'codex' },
        // After the index answered.
        { at: '2026-10-09T08:01:00Z', type: 'file', op: 'delete', path: 'a.go', tool: 'Bash', byName: 'codex' },
      ],
      '/r',
      server,
    )
    expect(rows.map((r) => r.abs)).toEqual(['/r/a.go', '/r/old.go'])
    expect(rows[0]).toMatchObject({ op: 'delete', ops: ['read', 'edit', 'delete'], count: 4, tool: 'Bash', first: '2026-10-09T07:00:00Z', at: '2026-10-09T08:01:00Z' })
    expect(rows[1]).toMatchObject({ ops: ['read'], count: 1, tool: '', by: '' })
  })
})
