import { describe, expect, it } from 'vitest'
import { closedByQuit, lostWords } from './nvimLost'

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
