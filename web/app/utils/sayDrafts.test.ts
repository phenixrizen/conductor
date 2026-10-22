import { describe, expect, it } from 'vitest'
import { SayDrafts } from './sayDrafts'

describe('the refused lines kept for the rows (#322)', () => {
  it('keeps a refused line and drops a typed one, per session', () => {
    const d = new SayDrafts()
    const a = d.begin('s1')
    d.settle('s1', a, true, 'first')
    expect(d.get('s1')).toBe('first')
    const b = d.begin('s2')
    d.settle('s2', b, false, 'other')
    expect(d.get('s2')).toBeUndefined()
    expect(d.get('s1')).toBe('first')
    const c = d.begin('s1')
    d.settle('s1', c, false, 'first')
    expect(d.get('s1')).toBeUndefined()
  })

  it("an earlier send's late word never erases a newer refused line (whole-batch round)", () => {
    const d = new SayDrafts()
    const first = d.begin('s1') // on its way
    const second = d.begin('s1') // refused at once: its line waits
    d.settle('s1', second, true, 'the newer line')
    d.settle('s1', first, false, 'the older line') // typed, or not confirmed: too late to touch the draft
    expect(d.get('s1')).toBe('the newer line')
    d.settle('s1', first, true, 'the older line')
    expect(d.get('s1')).toBe('the newer line')
    // The newer send's own later outcome still settles it.
    const third = d.begin('s1')
    d.settle('s1', third, false, 'x')
    expect(d.get('s1')).toBeUndefined()
  })
})
