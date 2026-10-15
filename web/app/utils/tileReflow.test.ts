import { Terminal } from '@xterm/xterm'
import { describe, expect, it } from 'vitest'
import { TILE_SCROLLBACK } from './tile'

// #221: xterm.js 6.0 (the workbench's), without a DOM, driven through the size histories that broke a wall tile. Narrowing a terminal
// that keeps no scrollback while its cursor is above the last row and longer lines are above it, the reflow moves the screen one row
// past the end of its buffer: the screen is drawn one row off and xterm keeps its input box off the cursor's row. A tile keeps
// TILE_SCROLLBACK rows instead, and the cursor is drawn on its row.

type Size = [cols: number, rows: number]

/** A terminal at a size, with a scrollback, and its writes awaited. */
function terminal(size: Size, scrollback: number) {
  const term = new Terminal({ cols: size[0], rows: size[1], scrollback, allowProposedApi: true })
  const write = (data: string) => new Promise<void>((done) => term.write(data, done))
  return { term, write }
}

/**
 * Where the cursor is drawn against where the buffer has it. The screen shows the buffer from its viewport, which the browser's scroll
 * keeps inside the buffer (no further down than its length less the rows); the cursor's line is the buffer's line at its base plus the
 * cursor's row, drawn that many rows below the top of what shows. In the broken buffer that line is one past the end, and the buffer,
 * a ring, paints it on the top row; xterm keeps its input box on the cursor's row only while the two agree.
 */
function cursor(term: Terminal) {
  const b = term.buffer.active
  const top = Math.max(0, Math.min(b.viewportY, b.length - term.rows))
  return { row: b.cursorY, drawn: b.baseY + b.cursorY - top, screenInBuffer: b.baseY + term.rows <= b.length }
}

// CI's wall tile (#219's traces): a 55-column prompt, redrawn by bash after each resize ("\r\x1b[K" and the prompt).
const PROMPT = 'runner@runnervmk1d70:/tmp/conductor-e2e-6HLuOS/wall-7$ '
const REDRAW = '\r\x1b[K' + PROMPT

/** Runs CI's history: the session at 80 × 24, then a, then b, then CI's 42 × 6 with the prompt redrawn, then 36 × 5. */
async function ciHistory(a: Size, b: Size, scrollback: number) {
  const { term, write } = terminal([80, 24], scrollback)
  await write(PROMPT)
  for (const size of [a, b, [42, 6] as Size]) {
    term.resize(...size)
    await write(REDRAW)
  }
  const at42x6 = { row: term.buffer.active.cursorY, col: term.buffer.active.cursorX }
  term.resize(36, 5)
  const after = cursor(term)
  term.dispose()
  return { at42x6, after }
}

const sizes = (cols: number[], rows: number[]): Size[] => cols.flatMap((c) => rows.map((r): Size => [c, r]))
const ROWS = [2, 3, 4, 5, 6, 24]
// Of the 2,450 histories through CI's sizes (80, 55, 50, 46 and 42 columns by 1 to 6 and 24 rows), the 150 that left the buffer broken.
const BREAKING: Array<[Size, Size]> = [
  ...sizes([50, 46], ROWS).flatMap((a) => sizes([42], [3, 4, 5, 6, 24]).map((b): [Size, Size] => [a, b])),
  ...sizes([42], ROWS).flatMap((a) => sizes([50, 46, 42], [3, 4, 5, 6, 24]).map((b): [Size, Size] => [a, b])),
]

describe("a tile's terminal narrowed with its cursor above the last row (#221)", () => {
  it('three 38-column lines above the cursor on the fourth row of six, narrowed to 36 × 5: the cursor is drawn on its row', async () => {
    // The Playwright spec's screen (terminal-height.spec.ts): a 30-column prompt and a command, three times, then the prompt.
    const lines = 'tile-221-prompt-012345678901$ touch a1\r\n'.repeat(3) + 'tile-221-prompt-012345678901$ '
    for (const [scrollback, broken] of [
      [0, true],
      [TILE_SCROLLBACK, false],
    ] as const) {
      const { term, write } = terminal([42, 6], scrollback)
      await write(lines)
      expect(term.buffer.active.cursorY).toBe(3)
      term.resize(36, 5)
      const c = cursor(term)
      term.dispose()
      if (broken) expect(c, 'with no scrollback the screen moves one row past the buffer (xterm.js 6.0)').toEqual({ row: 4, drawn: 5, screenInBuffer: false })
      else expect(c, `with a scrollback of ${scrollback}`).toEqual({ row: 4, drawn: 4, screenInBuffer: true })
    }
  })

  it(`CI's ${BREAKING.length} breaking histories: broken with no scrollback, the cursor drawn on its row with TILE_SCROLLBACK`, async () => {
    expect(TILE_SCROLLBACK).toBeGreaterThan(0)
    const without: string[] = []
    const withScrollback: string[] = []
    for (const [a, b] of BREAKING) {
      const name = `${a.join('x')} -> ${b.join('x')} -> 42x6 -> 36x5`
      const bare = await ciHistory(a, b, 0)
      // Each reaches CI's state at 42 × 6: the cursor on the fourth row, at column 13 of the wrapped prompt.
      expect(bare.at42x6, name).toEqual({ row: 3, col: 13 })
      if (bare.after.drawn !== bare.after.row && !bare.after.screenInBuffer) without.push(name)
      const kept = await ciHistory(a, b, TILE_SCROLLBACK)
      if (kept.after.drawn !== kept.after.row || !kept.after.screenInBuffer) withScrollback.push(`${name}: ${JSON.stringify(kept.after)}`)
    }
    // The bug this works around: should an xterm.js upgrade fix it, this half fails, and the tiles' scrollback is a choice again.
    expect(without, 'with no scrollback every history breaks (xterm.js 6.0)').toHaveLength(BREAKING.length)
    expect(withScrollback, 'with TILE_SCROLLBACK none does').toEqual([])
  })
})
