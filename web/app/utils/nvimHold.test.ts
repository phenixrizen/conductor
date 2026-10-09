import { describe, expect, it } from 'vitest'
import { NvimHolds } from './nvimHold'
import type { NvimViewState } from './nvimSwap'

const state: NvimViewState = { mode: 'n', cmdline: '', message: '', messageKind: '', swap: null, recovered: false, modified: true }

describe('NvimHolds', () => {
  it('keeps a Neovim for its path until taken back', () => {
    const h = new NvimHolds()
    h.hold('/w/a.txt', { id: 'e1', state, cursor: { line: 3, col: 2 } })
    expect(h.peek('/w/a.txt')?.id).toBe('e1')
    expect(h.size).toBe(1)
    expect(h.take('/w/a.txt')).toEqual({ id: 'e1', state, cursor: { line: 3, col: 2 } })
    expect(h.take('/w/a.txt')).toBeUndefined()
    expect(h.size).toBe(0)
  })

  it('forgets a Neovim that ended on its own', () => {
    const h = new NvimHolds()
    h.hold('/w/a.txt', { id: 'e1', state, cursor: { line: 1, col: 1 } })
    h.hold('/w/b.txt', { id: 'e2', state, cursor: { line: 1, col: 1 } })
    h.ended('e1')
    expect(h.peek('/w/a.txt')).toBeUndefined()
    expect(h.peek('/w/b.txt')?.id).toBe('e2')
  })

  it('hands back the Neovims whose tab closed, and only those', () => {
    const h = new NvimHolds()
    h.hold('/w/a.txt', { id: 'e1', state, cursor: { line: 1, col: 1 } })
    h.hold('/w/b.txt', { id: 'e2', state, cursor: { line: 1, col: 1 } })
    expect(h.orphans(['/w/b.txt', '/w/c.txt'])).toEqual([{ path: '/w/a.txt', id: 'e1' }])
    expect(h.peek('/w/a.txt')).toBeUndefined()
    expect(h.orphans(['/w/b.txt'])).toEqual([])
    expect(h.orphans([])).toEqual([{ path: '/w/b.txt', id: 'e2' }])
    expect(h.size).toBe(0)
  })

  it('clears', () => {
    const h = new NvimHolds()
    h.hold('/w/a.txt', { id: 'e1', state, cursor: { line: 1, col: 1 } })
    h.clear()
    expect(h.size).toBe(0)
  })
})
