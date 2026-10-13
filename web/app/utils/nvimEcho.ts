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
  /** In the ledger: keys refused on their way, which no settle covers. */
  refused?: boolean
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

/**
 * Where line (1-based) of Neovim's text is after a lines event: moved by the lines it inserted or removed above it; within the lines it
 * replaced, at the same place among the new ones (the last of them when fewer came), the text there then telling whether it is still
 * the line the guesses were laid on.
 */
export function shiftedLine(line: number, ev: EchoLines): number {
  const at = line - 1
  if (at < ev.first) return line
  if (ev.last >= 0 && at >= ev.last) return line + ev.lines.length - (ev.last - ev.first)
  return ev.first + Math.min(at - ev.first, Math.max(0, ev.lines.length - 1)) + 1
}

export class NvimEcho {
  /** The last number given to keys. */
  seq = 0
  /** The last number Neovim acknowledged, and the last it said it has done (settled). */
  private acked = 0
  private settledSeq = 0
  /** The numbers of keys refused on their way (noteRefused). */
  private refused = new Set<number>()
  /** The most guesses Neovim's line has been seen to hold since they were laid (tookBack). */
  private most = 0
  /** Neovim is gone (markLost). */
  private lost = false
  /**
   * The keys sent that Neovim has not said it has done (settled): every key, whatever the mode the page last knew (keys typed before
   * Neovim's word on the mode came are not classified wrongly), kept apart from what shows, so none goes without a trace. Only that
   * word retires them: a line that holds what was typed may have been made of fewer keys (a mapping that expands one).
   */
  private typed: Guess[] = []

  /**
   * Once Neovim is gone: the keys sent that it never confirmed, in Neovim's notation, which its text may not hold (it died before
   * them, a mapping took some). Shown to the person beside the text, never laid into it, since what they would have done is unknown.
   */
  get unconfirmed(): string {
    return this.lost ? this.typed.map((g) => g.text).join('') : ''
  }


  /** Neovim is gone: what it may never have had is kept aside from here on (unconfirmed). */
  markLost(): void {
    this.lost = true
  }
  private mode = 'n'
  private paused = false // a guess went wrong: none until insert mode is left
  private barrier = 0 // keys that end the guesses: nothing guessed until they are acknowledged
  private guesses: Guess[] = []
  private base: { line: number; col: number; text: string } | null = null // Neovim's line the guesses lie over, and where
  private held: EchoLines[] = []

  constructor(private doc: EchoDoc) {}

  /** The echo goes on in another editor over the same text (its tab shown again): what it shows and reconciles goes there. */
  rebind(doc: EchoDoc): void {
    this.doc = doc
  }

  /** Applies a lines event of Neovim's under the guesses (unshown), and follows their line through it. */
  private apply(ev: EchoLines, base: { line: number; col: number; text: string } | null) {
    this.doc.applyLines(ev)
    return base ? { ...base, line: shiftedLine(base.line, ev) } : null
  }

  /** Whether guesses show (the editor marks them, and keeps its cursor after them). */
  get showing(): boolean {
    return this.guesses.length > 0
  }

  /** Whether line changes wait for an acknowledgement. */
  get holding(): boolean {
    return this.held.length > 0
  }

  /** Whether keys were sent that Neovim has not acknowledged. */
  get waiting(): boolean {
    return this.seq > this.acked
  }

  /**
   * Whether keys were sent that Neovim has not said it has done (`settled`): their changes, and its word on them, may still be on their
   * way. Their acknowledgement is no sign of it: Neovim answers in the middle of a key (a plugin's vim.wait).
   */
  get unsettled(): boolean {
    for (let s = this.settledSeq + 1; s <= this.seq; s++) if (!this.refused.has(s)) return true
    return false
  }

  /**
   * The keys numbered seq did not reach Neovim (refused: too many at once, its input full): they will never be done, and its later
   * word that keys are done does not cover them; a guess of theirs comes off the text, since Neovim never had it. Or only their end,
   * rest, did not: what came before it reached Neovim and waits for its word like any key. Returns those that did not, for the person.
   */
  noteRefused(seq: number, rest?: string): string {
    const i = this.typed.findIndex((t) => t.seq === seq)
    if (i < 0 && rest) {
      // Its entry already gone (a settle that came first): the refused end is kept all the same.
      this.typed.push({ seq, text: rest, refused: true })
      this.typed.sort((a, b) => a.seq - b.seq)
      return rest
    }
    const sent = i >= 0 ? this.typed[i]!.text : ''
    if (rest && rest.length < sent.length && sent.endsWith(rest)) {
      this.typed.splice(i, 1, { seq, text: sent.slice(0, sent.length - rest.length) }, { seq, text: rest, refused: true })
      return rest
    }
    this.refused.add(seq)
    if (i >= 0) this.typed[i]!.refused = true
    if (this.guesses.some((g) => g.seq === seq)) {
      this.unshow()
      this.guesses = this.guesses.filter((g) => g.seq !== seq)
      if (this.guesses.length) this.show()
      else this.base = null
    }
    return sent || rest || ''
  }

  /** Neovim has done the keys numbered up to seq (the `settled` event). */
  noteSettled(seq: number): void {
    this.settledSeq = Math.max(this.settledSeq, seq)
    this.typed = this.typed.filter((g) => g.seq > seq || g.refused)
    if (!this.base || !this.guesses.length || this.guesses[0]!.seq > seq) return
    // Neovim has done the keys of these guesses and sent every change they made: what its text holds of them it holds, and those it
    // does not it made nothing of (a mapping that took the key). The guesses after them stay, laid after what it holds.
    let base: { line: number; col: number; text: string } | null = this.base
    this.unshow()
    for (const ev of this.held.splice(0)) base = this.apply(ev, base)
    const now = base ? this.doc.line(base.line) : ''
    const head = base ? base.text.slice(0, base.col - 1) : ''
    const tail = base ? base.text.slice(base.col - 1) : ''
    let inserted: string | null = base && now.startsWith(head) && now.endsWith(tail) && now.length >= head.length + tail.length ? now.slice(head.length, now.length - tail.length) : null
    const rest: Guess[] = []
    const held: Guess[] = []
    let done = ''
    for (const g of this.guesses) {
      if (inserted === null) break
      if (rest.length === 0 && inserted.startsWith(g.text)) {
        // In Neovim's text now.
        done += g.text
        held.push(g)
        inserted = inserted.slice(g.text.length)
      } else if (g.seq <= seq && rest.length === 0) {
        // Done, and nothing came of it.
      } else rest.push(g)
    }
    if (!base || inserted === null || inserted !== '') {
      // Not what the guesses could make (an autopair, a change elsewhere on the line): Neovim's text stands.
      this.guesses = []
      this.base = null
      this.most = 0
      return
    }
    this.guesses = rest
    this.most = 0
    if (!rest.length) {
      this.base = null
      return
    }
    this.base = { line: base.line, col: base.col + done.length, text: now }
    this.show()
  }

  /**
   * An acknowledgement that comes while the echo's tab is not in front (finish, then its follower): only its number is noted. It is no
   * sign the keys are done (Neovim answers in the middle of a key); what Neovim made of them shows in their late line changes
   * (lateLines) and once it says it has done them (noteSettled).
   */
  noteAck(ack: number): void {
    this.acked = Math.max(this.acked, ack)
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
    this.typed.push({ seq, text: keys })
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
    this.acked = Math.max(this.acked, ev.ack)
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
    // Nothing held (settled showed it meanwhile): nothing waited too long, and the guesses since stand.
    if (!this.held.length) return
    this.unshow()
    for (const ev of this.held.splice(0)) this.doc.applyLines(ev)
    this.guesses = []
    this.base = null
  }

  /**
   * Neovim will acknowledge nothing more (lost, not quit; or its tab is no longer in front): every line change it sent and the echo
   * held is shown, and the guesses its line does not hold stay laid over it (the person typed them; Neovim may never have had them), as
   * long as its line is still the one they were laid on and no key that is not a plain character followed them (a Backspace, an
   * Escape: what Neovim made of those keys is its text, which stands). Returns whether any guess stays; those that do are reconciled
   * again with each line change that comes after (lateLines).
   */
  finish(): boolean {
    let base = this.base
    this.unshow()
    // Each change it sent is weighed as it came: one that holds fewer of the guesses than an earlier one took some back (a Backspace).
    let stands = false
    for (const ev of this.held.splice(0)) {
      base = this.apply(ev, base)
      stands ||= this.tookBack(base)
    }
    return this.relay(base, stands)
  }

  /**
   * A line change that comes after finish (a lost Neovim's last ones can come after its end): shown, and the guesses that stay laid
   * again over Neovim's new line, less those it now holds. Returns whether any guess stays.
   */
  lateLines(ev: EchoLines): boolean {
    let base = this.base
    this.unshow()
    base = this.apply(ev, base)
    return this.relay(base, this.tookBack(base))
  }

  /** How many of the guesses Neovim's line holds, laid where they were (-1: it is not the line they were laid on as it was). */
  private holds(base: { line: number; col: number; text: string }): number {
    const now = this.doc.line(base.line)
    for (let k = this.guesses.length; k >= 0; k--) {
      const done = this.guesses
        .slice(0, k)
        .map((g) => g.text)
        .join('')
      if (now === base.text.slice(0, base.col - 1) + done + base.text.slice(base.col - 1)) return k
    }
    return -1
  }

  /**
   * Whether Neovim's line, after a change, shows it did something with the guesses beyond typing them: it is not their line any more,
   * or it holds fewer of them than it once did. Neovim's text then stands. Notes the most it has held.
   */
  private tookBack(base: { line: number; col: number; text: string } | null): boolean {
    if (!base || !this.guesses.length) return false
    const k = this.holds(base)
    if (k < 0 || k < this.most) return true
    this.most = k
    return false
  }

  /**
   * Neovim's text changed under the guesses (finish, lateLines): those its line holds go, the rest is laid again over it, unless
   * Neovim's text stands (tookBack). Keys that end the guesses (an Escape) do not end them here: whatever came after them in order, so
   * did they, and Neovim's line says how far it got.
   */
  private relay(base: { line: number; col: number; text: string } | null, stands: boolean): boolean {
    if (base && this.guesses.length && !stands) {
      const k = this.holds(base)
      const rest = k < 0 ? [] : this.guesses.slice(k)
      if (rest.length) {
        const done = this.guesses
          .slice(0, k)
          .map((g) => g.text)
          .join('')
        this.base = { line: base.line, col: base.col + done.length, text: this.doc.line(base.line) }
        this.guesses = rest
        this.most = 0
        this.held = []
        this.show()
        return true
      }
    }
    this.reset()
    return false
  }

  /** Forgets everything (the editor closes, the keymap changes). */
  reset(): void {
    this.guesses = []
    this.base = null
    this.held = []
    this.barrier = 0
    this.paused = false
    this.most = 0
  }

  private settle(ack: number) {
    let base = this.base
    this.unshow()
    for (const ev of this.held.splice(0)) base = this.apply(ev, base)
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
