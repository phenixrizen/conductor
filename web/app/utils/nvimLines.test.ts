import { describe, expect, it } from 'vitest'
import { applyLines, linesEdit, type LinesEvent } from './nvimLines'

/** Applies linesEdit to a text the way Monaco applies an edit, to check the edit against applyLines. */
function editText(text: string, ev: LinesEvent): string {
  const lines = text.split('\n')
  const { range, text: t } = linesEdit(ev, lines.length, (n) => lines[n - 1]!.length)
  const offset = (line: number, col: number) => lines.slice(0, line - 1).reduce((a, l) => a + l.length + 1, 0) + col - 1
  const a = offset(range.startLineNumber, range.startColumn)
  const b = offset(range.endLineNumber, range.endColumn)
  return text.slice(0, a) + t + text.slice(b)
}

const cases: Array<[string, string, LinesEvent]> = [
  ['replace a middle line', 'one\ntwo\nthree', { first: 1, last: 2, lines: ['TWO'] }],
  ['replace the first line', 'one\ntwo\nthree', { first: 0, last: 1, lines: ['ONE'] }],
  ['replace the last line', 'one\ntwo\nthree', { first: 2, last: 3, lines: ['THREE'] }],
  ['delete a middle line', 'one\ntwo\nthree', { first: 1, last: 2, lines: [] }],
  ['delete the first line', 'one\ntwo\nthree', { first: 0, last: 1, lines: [] }],
  ['delete the last line', 'one\ntwo\nthree', { first: 2, last: 3, lines: [] }],
  ['delete everything', 'one\ntwo\nthree', { first: 0, last: 3, lines: [] }],
  ['insert before the first line', 'one\ntwo', { first: 0, last: 0, lines: ['zero'] }],
  ['insert in the middle', 'one\ntwo', { first: 1, last: 1, lines: ['a', 'b'] }],
  ['insert after the last line', 'one\ntwo', { first: 2, last: 2, lines: ['three'] }],
  ['replace two lines with three', 'one\ntwo\nthree\nfour', { first: 1, last: 3, lines: ['x', 'y', 'z'] }],
  ['the whole buffer', 'old', { first: 0, last: -1, lines: ['new', 'text'] }],
  ['an empty buffer gains a line', '', { first: 0, last: 0, lines: ['first'] }],
  ['a line becomes empty', 'one\ntwo', { first: 0, last: 1, lines: [''] }],
]

describe('linesEdit', () => {
  for (const [name, text, ev] of cases) {
    it(name, () => {
      const want = applyLines(text.split('\n'), ev).join('\n')
      expect(editText(text, ev)).toBe(want)
    })
  }
  it('clamps a range past the end', () => {
    expect(applyLines(['a', 'b'], { first: 5, last: 9, lines: ['c'] })).toEqual(['a', 'b', 'c'])
    expect(editText('a\nb', { first: 5, last: 9, lines: ['c'] })).toBe('a\nb\nc')
  })
})
