import { describe, expect, it } from 'vitest'
import { closedByQuit, CONNECTION_LOST, lostHoldsChanges, lostWords, NvimLive } from './nvimLost'

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

describe('NvimLive', () => {
  it("names a connection's editors when it closes, those it opened and did not close", () => {
    const live = new NvimLive()
    live.welcome('s1')
    live.note({ kind: 'opened', id: 'a' })
    live.note({ kind: 'opened', id: 'b' })
    live.note({ kind: 'lines', id: 'b' })
    live.note({ kind: 'closed', id: 'b' })
    live.note({ kind: 'opened' })
    expect(live.ended()).toEqual(['a'])
    // Told once: the next close names none.
    expect(live.ended()).toEqual([])
  })

  it("names the last subscription's editors at a welcome of another, a close it did not see", () => {
    const live = new NvimLive()
    expect(live.welcome('s1')).toEqual([])
    live.note({ kind: 'opened', id: 'a' })
    // A welcome naming no subscriber (a signaling one), or the same: nothing ended.
    expect(live.welcome(undefined)).toEqual([])
    expect(live.welcome('s1')).toEqual([])
    expect(live.welcome('s2')).toEqual(['a'])
    live.note({ kind: 'opened', id: 'c' })
    // The page's terminal changed: nothing carried over.
    live.clear()
    expect(live.welcome('s3')).toEqual([])
    expect(live.ended()).toEqual([])
  })

  it('says why: a loss, not a quit, its tab kept', () => {
    expect(closedByQuit(CONNECTION_LOST)).toBe(false)
    expect(lostWords(CONNECTION_LOST)).toMatch(/^Neovim ended unexpectedly \(the connection to the machine was lost\)\. /)
  })
})
