import { describe, expect, it } from 'vitest'
import { copyText, cutBytes, makeQuote, quoteLocation, quotePath, rangeWords, relocate, relocationWords } from './quote'

const file = ['package api', '', 'func A() {}', 'func B() {}', 'func C() {}', '']

describe('a quote of lines', () => {
  it('takes the range, in either order, bounded', () => {
    expect(makeQuote('a.go', file, 3, 4)).toEqual({ path: 'a.go', from: 3, to: 4, lines: ['func A() {}', 'func B() {}'] })
    expect(makeQuote('a.go', file, 4, 3).from).toBe(3)
    const many = Array.from({ length: 20 }, (_, i) => `line ${i}`)
    const q = makeQuote('a.go', many, 1, 20)
    expect(q.lines).toHaveLength(12)
    expect(q.cut).toBe(true)
    expect(q.to).toBe(20)
    expect(makeQuote('a.go', ['é'.repeat(150)], 1, 1).lines[0]!.length).toBe(100)
  })
  it('names the path from the working directory and words the place', () => {
    expect(quotePath('/r/repo/internal/api/users.go', '/r/repo/')).toBe('internal/api/users.go')
    expect(quotePath('/elsewhere/x.go', '/r/repo')).toBe('/elsewhere/x.go')
    expect(quoteLocation({ path: 'a.go', from: 14, to: 16 })).toBe('a.go:14–16')
    expect(quoteLocation({ path: 'a.go', from: 14, to: 14 })).toBe('a.go:14')
    expect(rangeWords(14, 16)).toBe('lines 14–16')
    expect(rangeWords(3, 3)).toBe('line 3')
    expect(copyText({ path: 'a.go', from: 3, to: 4, lines: ['x', 'y'] })).toBe('a.go:3-4\nx\ny')
    expect(cutBytes('abc', 2)).toBe('ab')
  })
})

describe('where the lines are now', () => {
  const q = makeQuote('a.go', file, 3, 4)
  it('finds them where they were', () => {
    expect(relocate(q, file)).toEqual({ state: 'same' })
    expect(relocationWords(relocate(q, file))).toBe('')
  })
  it('finds them moved, the nearest place', () => {
    const now = ['package api', '', '// a new comment', '// and another', 'func A() {}', 'func B() {}', 'func C() {}']
    expect(relocate(q, now)).toEqual({ state: 'moved', from: 5, to: 6 })
    expect(relocationWords(relocate(q, now))).toBe('Lines moved since · now 5–6')
  })
  it('says when they are gone', () => {
    expect(relocate(q, ['package api', 'func Z() {}'])).toEqual({ state: 'changed' })
    expect(relocationWords({ state: 'changed' })).toBe('Lines changed since')
  })
})
