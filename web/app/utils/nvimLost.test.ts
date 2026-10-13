import { describe, expect, it } from 'vitest'
import { closedByQuit, lostHoldsChanges, lostWords } from './nvimLost'

describe('lostHoldsChanges', () => {
  it('holds changes when Neovim said so, a guess shows, or the text is not the file as read', () => {
    expect(lostHoldsChanges(true, false, 'a', 'a')).toBe(true)
    expect(lostHoldsChanges(false, true, 'a', 'a')).toBe(true)
    expect(lostHoldsChanges(false, false, 'ax', 'a')).toBe(true)
    expect(lostHoldsChanges(false, false, 'a', 'a')).toBe(false)
  })
})

describe('closedByQuit', () => {
  it('is the person’s own quit, or no reason at all', () => {
    expect(closedByQuit('quit')).toBe(true)
    expect(closedByQuit('')).toBe(true)
    expect(closedByQuit(undefined)).toBe(true)
  })
  it('is not an end Neovim was not asked for', () => {
    expect(closedByQuit('error: msgpack: cannot convert Nil to int64')).toBe(false)
    expect(closedByQuit('error: neovim did not answer in time')).toBe(false)
    expect(closedByQuit('error: msgpack/rpc: session closed')).toBe(false)
  })
})

describe('lostWords', () => {
  it('says why without the error prefix, and what Reopen does', () => {
    const w = lostWords('error: msgpack: cannot convert Nil to int64')
    expect(w).toContain('Neovim ended unexpectedly (msgpack: cannot convert Nil to int64)')
    expect(w).toContain('Reopen')
    expect(w).toContain('swap file')
  })
  it('reads without a reason', () => {
    expect(lostWords('error:')).toMatch(/^Neovim ended unexpectedly\. /)
  })
})
