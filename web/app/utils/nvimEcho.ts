/**
 * The local echo under the editor's Neovim keymap (round 13, G5). Every
 * key goes to the real Neovim on the session's machine, which is behind a
 * slow link at times; in insert mode a plain character the person types is
 * shown at once, a guess, laid over Neovim's text at the cursor. Each key
 * carries a number; Neovim's side acknowledges a number once it has
 * handled those keys, after the line changes they made (they ride one
 * channel). A line change that comes while guesses show is held until its
 * acknowledgement, so the two are settled at once: Neovim's line is taken
 * as the guesses' start with as many of them in it as it holds (an
 * acknowledgement can run early, when Neovim answers requests in the middle
 * of a key, or cover later keys too), those are dropped and the rest laid
 * over the new line at Neovim's cursor. When Neovim's line is not the guess (an
 * autopair, an abbreviation, a mapping), Neovim's text wins and guessing
 * stops until insert mode is left. Anything not a plain character (an
 * arrow, Backspace, Enter, a chord) ends the guesses: once its keys are
 * acknowledged Neovim's text and cursor are shown as they are. Pure: the
 * editor is reached through EchoDoc.
 */

/** 1-based line and UTF-16 column, Monaco's. */
export interface EchoPos {
  line: number
  col: number
}

/** A `lines` event: the buffer lines [first, last) (0-based, last -1 to the end) become lines. */
export interface EchoLines {
  first: number
  last: number
  lines: string[]
}

/** The editor, as the echo reaches it. */
export interface EchoDoc {
  /** The text of line n (1-based) as shown. */
  line(n: number): string
  /** Shows text as line n. */
  setLine(n: number, text: string): void
  /** Applies a `lines` event as the editor does. */
  applyLines(ev: EchoLines): void
}

/** How long a held line change waits for its acknowledgement before it is shown as it is. */
export const ECHO_HOLD_MS = 250

interface Guess {
  seq: number
  text: string
}

const isInsert = (mode: string) => mode === 'i' || mode === 'insert' || mode === 'ic' || mode === 'ix'

/** The printable character keys stand for, when they are one (`<lt>` is `<`, `<Space>` a space), else null. */
export function guessable(keys: string): string | null {
  if (keys === '<lt>') return '<'
  // The space bar comes as <Space> (keyToNvim): a plain character in insert mode like any other, and between every two words, so a
  // space not guessed held every word after it until Neovim's round trip (seen over the switchyard: letters 9 ms, spaces 40 ms).
  if (keys === '<Space>') return ' '
  if ([...keys].length !== 1 || keys < ' ' || keys === '\x7f') return null
  return keys
}

export class NvimEcho {
  /** The last number given to keys. */
  seq = 0
  private mode = 'n'
  private paused = false // a guess went wrong: none until insert mode is left
  private barrier = 0 // keys that end the guesses: nothing guessed until they are acknowledged
  private guesses: Guess[] = []
  private base: { line: number; col: number; text: string } | null = null // Neovim's line the guesses lie over, and where
  private held: EchoLines[] = []

  constructor(private doc: EchoDoc) {}

  /** Whether guesses show (the editor marks them, and keeps its cursor after them). */
  get showing(): boolean {
    return this.guesses.length > 0
  }

  /** Whether line changes wait for an acknowledgement. */
  get holding(): boolean {
    return this.held.length > 0
  }

  /** The guesses' range on screen, for the editor's marking. */
  range(): { line: number; from: number; to: number } | null {
    if (!this.base || !this.guesses.length) return null
    const n = this.guesses.reduce((a, g) => a + g.text.length, 0)
    return { line: this.base.line, from: this.base.col, to: this.base.col + n }
  }

  /**
   * Keys are being sent with the cursor shown at `at`: their number, and when they are shown at once, where the cursor goes.
   */
  send(keys: string, at: EchoPos): { seq: number; cursor?: EchoPos } {
    const seq = ++this.seq
    const ch = guessable(keys)
    if (!ch || !isInsert(this.mode) || this.paused || this.barrier) {
      if (this.guesses.length || ch === null) this.barrier = seq
      return { seq }
    }
    if (!this.base) this.base = { line: at.line, col: at.col, text: this.doc.line(at.line) }
    this.guesses.push({ seq, text: ch })
    this.show()
    const r = this.range()!
    return { seq, cursor: { line: r.line, col: r.to } }
  }

  /** A `lines` event: true when it is held for the acknowledgement on its way (the editor does not apply it). */
  lines(ev: EchoLines): boolean {
    if (!this.guesses.length && !this.held.length) return false
    this.held.push(ev)
    return true
  }

  /**
   * A `cursor` event; toUtf16 turns Neovim's byte column on a line into the editor's. Returns the cursor to show, or null to keep the
   * one shown (after the guesses).
   */
  cursor(ev: { line: number; col: number; mode: string; ack?: number }, toUtf16: (line: number, byteCol: number) => number): EchoPos | null {
    this.mode = ev.mode || this.mode
    if (!isInsert(this.mode)) this.paused = false
    if (!ev.ack) {
      // A move Neovim reports on its own: while guesses show, the cursor stays after them.
      return this.guesses.length ? null : { line: ev.line, col: toUtf16(ev.line, ev.col) }
    }
    this.settle(ev.ack)
    if (this.barrier && ev.ack >= this.barrier) this.barrier = 0
    if (!isInsert(this.mode)) this.drop()
    const r = this.range()
    if (r) return { line: r.line, col: r.to }
    return { line: ev.line, col: toUtf16(ev.line, ev.col) }
  }

  /** Neovim's mode changed (a `mode` event): out of insert mode, guessing may start again at the next insert. */
  setMode(mode: string): void {
    this.mode = mode
    if (!isInsert(mode)) this.paused = false
  }

  /** A held change waited too long: Neovim's text as it is, the guesses gone. */
  timeout(): void {
    this.unshow()
    for (const ev of this.held.splice(0)) this.doc.applyLines(ev)
    this.guesses = []
    this.base = null
  }

  /** Forgets everything (the editor closes, the keymap changes). */
  reset(): void {
    this.guesses = []
    this.base = null
    this.held = []
    this.barrier = 0
    this.paused = false
  }

  private settle(ack: number) {
    const base = this.base
    this.unshow()
    for (const ev of this.held.splice(0)) this.doc.applyLines(ev)
    if (!base) return
    if (this.barrier && ack >= this.barrier) {
      // Keys that end the guesses are handled: Neovim's text and cursor stand.
      this.guesses = []
      this.base = null
      return
    }
    const now = this.doc.line(base.line)
    // How many guesses Neovim's line holds: most often those acknowledged, but it may hold fewer or more.
    let held = -1
    for (let k = this.guesses.length; k >= 0; k--) {
      const text = this.guesses
        .slice(0, k)
        .map((g) => g.text)
        .join('')
      if (now === base.text.slice(0, base.col - 1) + text + base.text.slice(base.col - 1)) {
        held = k
        break
      }
    }
    if (held < 0) {
      // Not what was guessed (an autopair, an abbreviation, a mapping): Neovim's text, and no more guessing in this insert.
      this.guesses = []
      this.base = null
      this.paused = true
      return
    }
    const doneText = this.guesses
      .slice(0, held)
      .map((g) => g.text)
      .join('')
    this.guesses = this.guesses.slice(held)
    if (!this.guesses.length) {
      this.base = null
      return
    }
    this.base = { line: base.line, col: base.col + doneText.length, text: now }
    this.show()
  }

  private drop() {
    this.unshow()
    this.guesses = []
    this.base = null
  }

  /** Lays the guesses over Neovim's line. */
  private show() {
    if (!this.base) return
    const { line, col, text } = this.base
    this.doc.setLine(line, text.slice(0, col - 1) + this.guesses.map((g) => g.text).join('') + text.slice(col - 1))
  }

  /** Neovim's line again, without the guesses. */
  private unshow() {
    if (this.base && this.guesses.length) this.doc.setLine(this.base.line, this.base.text)
  }
}
