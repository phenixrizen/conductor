import { describe, expect, it } from 'vitest'
import { planRows } from './plans'
import type { FileHeader } from './protocol'

const reply = (over: Partial<FileHeader>): FileHeader => ({ reqId: 'p', path: '/home/dev/.claude/plans', kind: 'plans', exists: true, ...over })

describe('planRows', () => {
  it('names each plan by its whole path under the plans directory, in the reply order', () => {
    const rows = planRows(reply({ entries: [{ name: 'plan-creating-a-file.md', dir: false, size: 40 }, { name: 'nested/later.md', dir: false, size: 3 }] }))
    expect(rows).toEqual([
      { abs: '/home/dev/.claude/plans/plan-creating-a-file.md', name: 'plan-creating-a-file.md', size: 40 },
      { abs: '/home/dev/.claude/plans/nested/later.md', name: 'nested/later.md', size: 3 },
    ])
  })

  it('joins a Windows plans directory with its own separator', () => {
    const rows = planRows(reply({ path: 'C:\\Users\\dev\\.claude\\plans\\', entries: [{ name: 'a/b.md', dir: false, size: 1 }] }))
    expect(rows).toEqual([{ abs: 'C:\\Users\\dev\\.claude\\plans\\a\\b.md', name: 'a/b.md', size: 1 }])
  })

  it('is empty for another reply, no entries, or a session that keeps no plans', () => {
    expect(planRows(null)).toEqual([])
    expect(planRows(reply({ kind: 'touched' }))).toEqual([])
    expect(planRows(reply({ exists: false }))).toEqual([])
    expect(planRows(reply({ entries: [{ name: 'dir', dir: true, size: 0 }] }))).toEqual([])
  })
})
