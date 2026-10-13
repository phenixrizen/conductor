import { describe, expect, it } from 'vitest'
import { NvimEcho } from './nvimEcho'
import { NvimHolds } from './nvimHold'
import type { NvimViewState } from './nvimSwap'

const state: NvimViewState = { mode: 'n', cmdline: '', message: '', messageKind: '', swap: null, recovered: false, modified: true }

/** A follower's stop, counting its calls. */
function stopper() {
  const s = { calls: 0, stop: () => void s.calls++ }
  return s
}

describe('NvimHolds', () => {
  it('keeps a Neovim for its path until taken back, and stops following it then', () => {
    const h = new NvimHolds()
    const f = stopper()
    h.hold('/w/a.txt', { id: 'e1', state, cursor: { line: 3, col: 2 }, stop: f.stop })
    expect(h.peek('/w/a.txt')?.id).toBe('e1')
    expect(h.size).toBe(1)
    expect(f.calls).toBe(0)
    expect(h.take('/w/a.txt')).toEqual({ id: 'e1', state, cursor: { line: 3, col: 2 } })
    expect(f.calls).toBe(1)
    expect(h.take('/w/a.txt')).toBeUndefined()
    expect(h.size).toBe(0)
  })

  it('forgets a Neovim that ended on its own', () => {
    const h = new NvimHolds()
    const f = stopper()
    h.hold('/w/a.txt', { id: 'e1', state, cursor: { line: 1, col: 1 }, stop: f.stop })
    h.hold('/w/b.txt', { id: 'e2', state, cursor: { line: 1, col: 1 } })
    h.ended('e1')
    expect(h.peek('/w/a.txt')).toBeUndefined()
    expect(f.calls).toBe(1)
    expect(h.peek('/w/b.txt')?.id).toBe('e2')
  })

  it('keeps a Neovim lost, not quit, as lost: its state with the reason, still followed for the changes it sent before its end', () => {
    const h = new NvimHolds()
    const f = stopper()
    h.hold('/w/a.txt', { id: 'e1', state: { ...state, cmdline: ':w' }, cursor: { line: 2, col: 3 }, stop: f.stop })
    h.ended('e1', 'error: neovim ended')
    expect(h.peek('/w/a.txt')).toMatchObject({ id: 'e1', state: { ...state, cmdline: '', lost: 'error: neovim ended' }, cursor: { line: 2, col: 3 } })
    expect(f.calls).toBe(0)
    // A second end, or a quit, changes nothing: it is lost.
    h.ended('e1', 'quit')
    expect(h.peek('/w/a.txt')?.state.lost).toBe('error: neovim ended')
    // Its tab closed: handed back as lost (nothing to end), no longer followed.
    expect(h.orphans([])).toEqual([{ path: '/w/a.txt', id: 'e1', lost: true, modified: true }])
    expect(f.calls).toBe(1)
  })

  it('counts the characters a lost Neovim never had among its changes not written', () => {
    const h = new NvimHolds()
    const text = ['alpha']
    const echo = new NvimEcho({ line: (n) => text[n - 1] ?? '', setLine: (n, t) => void (text[n - 1] = t), applyLines: () => {} })
    echo.cursor({ line: 1, col: 6, mode: 'i', ack: 0 }, (_l, c) => c)
    echo.send('x', { line: 1, col: 6 })
    echo.finish()
    h.hold('/w/a.txt', { id: 'e1', state: { ...state, modified: false }, cursor: { line: 1, col: 7 }, echo })
    h.ended('e1', 'error: neovim ended')
    expect(h.peek('/w/a.txt')?.state.modified).toBe(true)
  })

  it('forgets a Neovim that quit', () => {
    const h = new NvimHolds()
    h.hold('/w/a.txt', { id: 'e1', state, cursor: { line: 1, col: 1 } })
    h.ended('e1', 'quit')
    expect(h.peek('/w/a.txt')).toBeUndefined()
  })

  it('hands back the Neovims whose tab closed, and only those', () => {
    const h = new NvimHolds()
    h.hold('/w/a.txt', { id: 'e1', state, cursor: { line: 1, col: 1 } })
    h.hold('/w/b.txt', { id: 'e2', state, cursor: { line: 1, col: 1 } })
    h.hold('/w/c.txt', { id: 'e3', state: { ...state, modified: false }, cursor: { line: 1, col: 1 } })
    expect(h.orphans(['/w/b.txt', '/w/c.txt'])).toEqual([{ path: '/w/a.txt', id: 'e1', lost: false, modified: true }])
    expect(h.peek('/w/a.txt')).toBeUndefined()
    expect(h.orphans(['/w/b.txt', '/w/c.txt'])).toEqual([])
    // One kept for keys on their way, with no change yet: its Neovim ends keeping what it may have (not dropped).
    expect(h.orphans([])).toEqual([
      { path: '/w/b.txt', id: 'e2', lost: false, modified: true },
      { path: '/w/c.txt', id: 'e3', lost: false, modified: false },
    ])
    expect(h.size).toBe(0)
  })

  it('tells its listeners of each hold made, changed, ended and let go', () => {
    const h = new NvimHolds()
    const told: Array<[string, boolean | undefined]> = []
    const off = h.listen((path, held) => told.push([path, held?.state.modified]))
    const s = { ...state, modified: false }
    h.hold('/w/a.txt', { id: 'e1', state: s, cursor: { line: 1, col: 1 } })
    s.modified = true
    h.changed('/w/a.txt')
    h.changed('/w/none.txt')
    h.ended('e1', 'error: neovim ended')
    expect(h.release('/w/a.txt')?.id).toBe('e1')
    off()
    h.hold('/w/b.txt', { id: 'e2', state, cursor: { line: 1, col: 1 } })
    expect(told).toEqual([
      ['/w/a.txt', false],
      ['/w/a.txt', true],
      ['/w/a.txt', true],
      ['/w/a.txt', undefined],
    ])
  })

  it('stops following what it clears, and what another hold of the same path replaces', () => {
    const h = new NvimHolds()
    const a = stopper()
    const b = stopper()
    h.hold('/w/a.txt', { id: 'e1', state, cursor: { line: 1, col: 1 }, stop: a.stop })
    h.hold('/w/a.txt', { id: 'e2', state, cursor: { line: 1, col: 1 }, stop: b.stop })
    expect(a.calls).toBe(1)
    h.clear()
    expect(b.calls).toBe(1)
    expect(h.size).toBe(0)
  })
})
