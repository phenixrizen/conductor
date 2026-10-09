import { describe, expect, it } from 'vitest'
import { swapChoices, swapWords } from './nvimSwap'

describe('the swap file banner', () => {
  it('names a running writer and offers only Edit anyway', () => {
    const s = { file: '/s/x.swp', pid: 4121, running: true, user: 'nate', host: 'box' }
    expect(swapWords(s)).toBe('Another Vim (process 4121, nate on box, still running) has this file open. Opened read-only.')
    expect(swapChoices(s).map((c) => c.choice)).toEqual(['edit'])
  })
  it('offers Recover for unsaved changes and Delete once the writer is gone', () => {
    const s = { file: '/s/x.swp', pid: 77, running: false, modified: true }
    expect(swapWords(s)).toBe('A swap file from a Vim that ended (process 77, no longer running, with changes not written) was found. Opened read-only.')
    expect(swapChoices(s).map((c) => c.label)).toEqual(['Edit anyway', 'Recover', 'Delete the swap file'])
    expect(swapChoices({ ...s, modified: false }).map((c) => c.choice)).toEqual(['edit', 'delete'])
  })
  it('after a recovery, says to write and offers deleting the swap file', () => {
    const s = { file: '/s/x.swp', running: false, modified: true }
    expect(swapWords(s, true)).toMatch(/write the file \(:w\)/)
    expect(swapChoices(s, true).map((c) => c.choice)).toEqual(['delete'])
  })
})
