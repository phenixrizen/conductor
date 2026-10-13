import { describe, expect, it } from 'vitest'
import { guessable, NvimEcho, type EchoDoc, type EchoLines } from './nvimEcho'

/** A fake editor: lines, and a lines event applied as the editor applies it. */
function doc(...lines: string[]): EchoDoc & { text: string[] } {
  const d = {
    text: [...lines],
    line: (n: number) => d.text[n - 1] ?? '',
    setLine: (n: number, t: string) => {
      d.text[n - 1] = t
    },
    applyLines: (ev: EchoLines) => {
      const last = ev.last < 0 ? d.text.length : ev.last
      d.text.splice(ev.first, last - ev.first, ...ev.lines)
    },
  }
  return d
}
const ascii = (_line: number, byteCol: number) => byteCol

/** Neovim in insert mode at the start of line 1. */
function inInsert(d: EchoDoc) {
  const e = new NvimEcho(d)
  e.cursor({ line: 1, col: 1, mode: 'i', ack: 0 }, ascii)
  return e
}

describe('the local echo', () => {
  it('shows plain characters at once in insert mode, and drops each guess once Neovim has it', () => {
    const d = doc('end')
    const e = inInsert(d)
    expect(e.send('a', { line: 1, col: 1 })).toEqual({ seq: 1, cursor: { line: 1, col: 2 } })
    expect(e.send('b', { line: 1, col: 2 })).toEqual({ seq: 2, cursor: { line: 1, col: 3 } })
    expect(d.text).toEqual(['abend'])
    expect(e.range()).toEqual({ line: 1, from: 1, to: 3 })
    // Neovim handled a: its line, held, then its acknowledgement; b still shows, after Neovim's a.
    expect(e.lines({ first: 0, last: 1, lines: ['aend'] })).toBe(true)
    expect(d.text).toEqual(['abend'])
    expect(e.cursor({ line: 1, col: 2, mode: 'i', ack: 1 }, ascii)).toEqual({ line: 1, col: 3 })
    expect(d.text).toEqual(['abend'])
    expect(e.range()).toEqual({ line: 1, from: 2, to: 3 })
    // Then b: nothing left to guess, Neovim's text and cursor.
    e.lines({ first: 0, last: 1, lines: ['abend'] })
    expect(e.cursor({ line: 1, col: 3, mode: 'i', ack: 2 }, ascii)).toEqual({ line: 1, col: 3 })
    expect(d.text).toEqual(['abend'])
    expect(e.showing).toBe(false)
  })

  it('guesses nothing outside insert mode, nor for a key that is not a plain character', () => {
    const d = doc('end')
    const e = new NvimEcho(d)
    expect(e.send('x', { line: 1, col: 1 })).toEqual({ seq: 1 })
    e.cursor({ line: 1, col: 1, mode: 'i', ack: 1 }, ascii)
    expect(e.send('<BS>', { line: 1, col: 1 })).toEqual({ seq: 2 })
    expect(e.send('a', { line: 1, col: 1 })).toEqual({ seq: 3 }) // Backspace not acknowledged yet: no guess
    expect(d.text).toEqual(['end'])
    expect(guessable('<lt>')).toBe('<')
    expect(guessable('<Space>')).toBe(' ')
    expect(guessable('<C-Space>')).toBe(null)
    expect(guessable('<CR>')).toBe(null)
    expect(guessable('é')).toBe('é')
  })

  it("takes Neovim's text when it is not the guess, and stops guessing until insert mode is left", () => {
    const d = doc('')
    const e = inInsert(d)
    e.send('f', { line: 1, col: 1 })
    e.send('(', { line: 1, col: 2 })
    expect(d.text).toEqual(['f('])
    // An autopair: Neovim's line has the closing parenthesis too.
    e.lines({ first: 0, last: 1, lines: ['f()'] })
    expect(e.cursor({ line: 1, col: 3, mode: 'i', ack: 2 }, ascii)).toEqual({ line: 1, col: 3 })
    expect(d.text).toEqual(['f()'])
    expect(e.send('x', { line: 1, col: 3 }).cursor).toBeUndefined()
    // Escape, and insert again: guessing again.
    e.cursor({ line: 1, col: 2, mode: 'n', ack: 3 }, ascii)
    e.cursor({ line: 1, col: 3, mode: 'i', ack: 4 }, ascii)
    expect(e.send('y', { line: 1, col: 3 }).cursor).toEqual({ line: 1, col: 4 })
  })

  it('ends the guesses for a key that is not a plain character, and shows what Neovim made of it', () => {
    const d = doc('end')
    const e = inInsert(d)
    e.send('a', { line: 1, col: 1 })
    e.send('<BS>', { line: 1, col: 2 })
    e.lines({ first: 0, last: 1, lines: ['aend'] })
    e.lines({ first: 0, last: 1, lines: ['end'] })
    expect(e.cursor({ line: 1, col: 1, mode: 'i', ack: 2 }, ascii)).toEqual({ line: 1, col: 1 })
    expect(d.text).toEqual(['end'])
    expect(e.showing).toBe(false)
    expect(e.send('b', { line: 1, col: 1 }).cursor).toEqual({ line: 1, col: 2 })
  })

  it('guesses a space like a letter, so the word after it shows at once too', () => {
    const d = doc('')
    const e = inInsert(d)
    e.send('a', { line: 1, col: 1 })
    expect(e.send('<Space>', { line: 1, col: 2 })).toEqual({ seq: 2, cursor: { line: 1, col: 3 } })
    expect(e.send('b', { line: 1, col: 3 })).toEqual({ seq: 3, cursor: { line: 1, col: 4 } })
    expect(d.text).toEqual(['a b'])
    // Neovim's own text arrives and settles all three.
    e.lines({ first: 0, last: 1, lines: ['a b'] })
    expect(e.cursor({ line: 1, col: 4, mode: 'i', ack: 3 }, ascii)).toEqual({ line: 1, col: 4 })
    expect(d.text).toEqual(['a b'])
    expect(e.showing).toBe(false)
  })

  it('shows a held change as it is when its acknowledgement does not come', () => {
    const d = doc('end')
    const e = inInsert(d)
    e.send('j', { line: 1, col: 1 })
    expect(e.lines({ first: 0, last: 1, lines: ['jend'] })).toBe(true)
    e.timeout()
    expect(d.text).toEqual(['jend'])
    expect(e.showing).toBe(false)
    expect(e.holding).toBe(false)
  })

  it('takes an acknowledgement that runs early (Neovim answered mid-key) or covers later keys, by what the line holds', () => {
    const d = doc('end')
    const e = inInsert(d)
    e.send('a', { line: 1, col: 1 })
    e.send('b', { line: 1, col: 2 })
    // Early: acknowledged before a is in the line. Both guesses stay.
    expect(e.cursor({ line: 1, col: 1, mode: 'i', ack: 1 }, ascii)).toEqual({ line: 1, col: 3 })
    expect(d.text).toEqual(['abend'])
    expect(e.range()).toEqual({ line: 1, from: 1, to: 3 })
    // Late: the next acknowledgement finds a and b both handled.
    e.send('c', { line: 1, col: 3 })
    e.lines({ first: 0, last: 1, lines: ['abend'] })
    expect(e.cursor({ line: 1, col: 3, mode: 'i', ack: 2 }, ascii)).toEqual({ line: 1, col: 4 })
    expect(d.text).toEqual(['abcend'])
    expect(e.range()).toEqual({ line: 1, from: 3, to: 4 })
  })

  it('keeps the cursor after the guesses when Neovim reports a move of its own', () => {
    const d = doc('end')
    const e = inInsert(d)
    e.send('a', { line: 1, col: 1 })
    expect(e.cursor({ line: 1, col: 1, mode: 'i' }, ascii)).toBe(null)
    expect(e.lines({ first: 1, last: 1, lines: ['new'] })).toBe(true) // another line's change waits too, in order
    e.lines({ first: 0, last: 1, lines: ['aend'] })
    e.cursor({ line: 1, col: 2, mode: 'i', ack: 1 }, ascii)
    expect(d.text).toEqual(['aend', 'new'])
  })
})

describe('the local echo when Neovim is lost', () => {
  it('shows the line changes it held, and keeps the guesses Neovim never acknowledged laid over them', () => {
    const d = doc('alpha', 'end')
    const e = inInsert(d)
    e.send('x', { line: 1, col: 6 })
    e.send('y', { line: 1, col: 7 })
    expect(d.text).toEqual(['alphaxy', 'end'])
    // Neovim had x, and changed line 2 too; both held for an acknowledgement that never comes.
    expect(e.lines({ first: 0, last: 1, lines: ['alphax'] })).toBe(true)
    expect(e.lines({ first: 1, last: 2, lines: ['end -- from Neovim'] })).toBe(true)
    expect(e.finish()).toBe(true)
    expect(d.text).toEqual(['alphaxy', 'end -- from Neovim'])
    // y is still a guess, for the line changes that may come after; nothing is held.
    expect(e.range()).toEqual({ line: 1, from: 7, to: 8 })
    expect(e.holding).toBe(false)
  })

  it('keeps no guess Neovim had, and none over a line that is no longer the one they were laid on', () => {
    const had = doc('alpha')
    const e1 = inInsert(had)
    e1.send('x', { line: 1, col: 6 })
    e1.lines({ first: 0, last: 1, lines: ['alphax'] })
    expect(e1.finish()).toBe(false)
    expect(had.text).toEqual(['alphax'])

    const other = doc('alpha')
    const e2 = inInsert(other)
    e2.send('x', { line: 1, col: 6 })
    e2.lines({ first: 0, last: 1, lines: ['something else'] })
    expect(e2.finish()).toBe(false)
    expect(other.text).toEqual(['something else'])
  })

  it('keeps no guess a later key may have undone: Neovim deleted x with Backspace, its text stands', () => {
    const d = doc('alpha')
    const e = inInsert(d)
    e.send('x', { line: 1, col: 6 })
    e.send('<BS>', { line: 1, col: 7 })
    e.lines({ first: 0, last: 1, lines: ['alphax'] })
    e.lines({ first: 0, last: 1, lines: ['alpha'] })
    expect(e.finish()).toBe(false)
    expect(d.text).toEqual(['alpha'])
  })

  it('reconciles the guesses it kept with line changes that come after the end', () => {
    const d = doc('alpha')
    const e = inInsert(d)
    e.send('x', { line: 1, col: 6 })
    e.send('y', { line: 1, col: 7 })
    // The end comes before any line: both guesses stay.
    expect(e.finish()).toBe(true)
    expect(d.text).toEqual(['alphaxy'])
    // Neovim's line for x comes after: x is Neovim's now, y stays laid after it.
    expect(e.lateLines({ first: 0, last: 1, lines: ['alphax'] })).toBe(true)
    expect(d.text).toEqual(['alphaxy'])
    // Its line for y: nothing left to guess.
    expect(e.lateLines({ first: 0, last: 1, lines: ['alphaxy'] })).toBe(false)
    expect(d.text).toEqual(['alphaxy'])
    // After that, a late change is Neovim's text as it is.
    expect(e.lateLines({ first: 0, last: 1, lines: ['alphaxyz'] })).toBe(false)
    expect(d.text).toEqual(['alphaxyz'])
  })

  it('lays no kept guess over a late line that is not the one they were laid on', () => {
    const d = doc('alpha')
    const e = inInsert(d)
    e.send('x', { line: 1, col: 6 })
    expect(e.finish()).toBe(true)
    expect(e.lateLines({ first: 0, last: 1, lines: ['something else'] })).toBe(false)
    expect(d.text).toEqual(['something else'])
  })

  it('follows the guesses’ line through lines inserted or removed above it, among lines that read the same', () => {
    const d = doc('alpha', 'alpha')
    const e = new NvimEcho(d)
    e.cursor({ line: 2, col: 6, mode: 'i', ack: 0 }, ascii)
    e.send('x', { line: 2, col: 6 })
    expect(e.finish()).toBe(true)
    expect(d.text).toEqual(['alpha', 'alphax'])
    // A line inserted above: x goes down with its line.
    expect(e.lateLines({ first: 0, last: 0, lines: ['alpha'] })).toBe(true)
    expect(d.text).toEqual(['alpha', 'alpha', 'alphax'])
    // Neovim's line for x, now the third: nothing left to guess, nothing doubled.
    expect(e.lateLines({ first: 2, last: 3, lines: ['alphax'] })).toBe(false)
    expect(d.text).toEqual(['alpha', 'alpha', 'alphax'])
  })

  it('follows the guesses’ line up when a line above goes, and while Neovim still answers', () => {
    const d = doc('top', 'alpha')
    const e = new NvimEcho(d)
    e.cursor({ line: 2, col: 6, mode: 'i', ack: 0 }, ascii)
    e.send('x', { line: 2, col: 6 })
    e.send('y', { line: 2, col: 7 })
    expect(d.text).toEqual(['top', 'alphaxy'])
    // Held for x's acknowledgement: the first line removed, then x's line (now the first).
    expect(e.lines({ first: 0, last: 1, lines: [] })).toBe(true)
    expect(e.lines({ first: 0, last: 1, lines: ['alphax'] })).toBe(true)
    expect(e.cursor({ line: 1, col: 7, mode: 'i', ack: 1 }, ascii)).toEqual({ line: 1, col: 8 })
    expect(d.text).toEqual(['alphaxy'])
  })

  it('says keys are waiting until Neovim acknowledges the last of them', () => {
    const e = inInsert(doc('alpha'))
    expect(e.waiting).toBe(false)
    e.send('x', { line: 1, col: 6 })
    e.send('<Esc>', { line: 1, col: 7 })
    expect(e.waiting).toBe(true)
    e.cursor({ line: 1, col: 7, mode: 'i', ack: 1 }, ascii)
    expect(e.waiting).toBe(true)
    e.cursor({ line: 1, col: 6, mode: 'n', ack: 2 }, ascii)
    expect(e.waiting).toBe(false)
  })

  it('notes an acknowledgement that comes while its tab is not in front, and drops the guesses keys ending them undid', () => {
    const d = doc('alpha')
    const e = inInsert(d)
    e.send('x', { line: 1, col: 6 })
    e.send('<BS>', { line: 1, col: 7 })
    e.finish()
    expect(d.text).toEqual(['alpha'])
    expect(e.waiting).toBe(true)
    e.noteAck(1)
    expect(e.waiting).toBe(true)
    e.noteAck(2)
    expect(e.waiting).toBe(false)
    // With no barrier, a kept guess stays through an acknowledgement: its line comes as a late one.
    const d2 = doc('alpha')
    const e2 = inInsert(d2)
    e2.send('x', { line: 1, col: 6 })
    e2.finish()
    e2.noteAck(1)
    expect(d2.text).toEqual(['alphax'])
    expect(e2.lateLines({ first: 0, last: 1, lines: ['alphax'] })).toBe(false)
    expect(d2.text).toEqual(['alphax'])
  })

  it('goes on in another editor over the same text, its guesses kept', () => {
    const d = doc('alpha')
    const e = inInsert(d)
    e.send('x', { line: 1, col: 6 })
    e.send('y', { line: 1, col: 7 })
    expect(e.finish()).toBe(true)
    // The tab shown again: a new editor over the same text, which Neovim's lines reach.
    const again = doc(...d.text)
    e.rebind(again)
    expect(e.lines({ first: 0, last: 1, lines: ['alphax'] })).toBe(true)
    expect(e.cursor({ line: 1, col: 7, mode: 'i', ack: 1 }, ascii)).toEqual({ line: 1, col: 8 })
    expect(again.text).toEqual(['alphaxy'])
    // And if Neovim is lost then, y stays.
    expect(e.finish()).toBe(true)
    expect(again.text).toEqual(['alphaxy'])
  })

  it('does nothing with no guesses and nothing held', () => {
    const d = doc('alpha')
    const e = inInsert(d)
    expect(e.finish()).toBe(false)
    expect(d.text).toEqual(['alpha'])
  })
})
