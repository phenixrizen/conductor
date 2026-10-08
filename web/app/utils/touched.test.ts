import { describe, expect, it } from 'vitest'
import { opIcon, touchedPaths, touchedRows, touchedWords } from './touched'

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
  })
})
