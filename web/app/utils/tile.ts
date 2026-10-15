import { FOLLOW_SIZE } from './protocol'

/** A tile's terminal font size, in pixels: fixed, so a tile fits more columns than a scaled view of the PTY's grid. */
export const TILE_FONT_SIZE = 11

/**
 * A tile's scrollback, in rows (#221). Never none: narrowing a terminal that keeps no scrollback while its cursor is above the last row,
 * xterm.js 6.0's reflow moves the screen one row past the end of its buffer, so the rows are drawn one off (the cursor's line on top)
 * and the input box stays where it was. With any scrollback the buffer has the row to move into. The wheel scrolls a tile's history.
 */
export const TILE_SCROLLBACK = 1000

/** How long a terminal waits after its own pane last changed size before it fits and resizes the session (the full view uses the same). */
export const FIT_DEBOUNCE_MS = 100

/** The largest terminal dimension the protocol takes (proto.MaxTerminalDimension). */
export const MAX_DIMENSION = 500

/**
 * The size a terminal's hello carries: the size it fitted to its pane when it sizes the session (`sizes`: a full view or a tile with
 * control), else `{0, 0}`, which the server reads as "take the session's size": a scaled view, a view-only link, or a pane not laid out yet
 * (no column or no row fits), so that no viewer resets a session by accident. Never more than 500 a side.
 */
export function helloSize(sizes: boolean, cols: number, rows: number): { cols: number; rows: number } {
  if (!sizes || !(cols >= 1) || !(rows >= 1)) return { ...FOLLOW_SIZE }
  return { cols: Math.min(Math.floor(cols), MAX_DIMENSION), rows: Math.min(Math.floor(rows), MAX_DIMENSION) }
}

export interface Box {
  width: number
  height: number
}

/**
 * The columns and rows a tile's terminal fits in its pane, measured as the fit addon measures them (the pane less the terminal's
 * padding, over the cell; at least 2 × 1) but with no gutter kept for a scrollbar: the addon keeps 14 px for one whenever the terminal
 * has a scrollback, every pane hides it, and a tile would leave a blank strip beside its grid. Undefined while the cell has no size (not
 * rendered yet) or a box is not a number.
 */
export function tileFit(pane: Box, padding: Box, cell: Box): { cols: number; rows: number } | undefined {
  if (![pane.width, pane.height, padding.width, padding.height].every(Number.isFinite) || !(cell.width > 0) || !(cell.height > 0)) return undefined
  return {
    cols: Math.max(2, Math.floor(Math.max(0, pane.width - padding.width) / cell.width)),
    rows: Math.max(1, Math.floor((pane.height - padding.height) / cell.height)),
  }
}

/**
 * The factor a tile shrinks its terminal by to show the whole screen when another viewer has sized the session larger than the tile (latest
 * controller wins): 1 when the screen, with its padding, fits the pane; never more than 1, and 1 when either box is not laid out.
 */
export function tileScale(pane: Box, screen: Box): number {
  if (!(pane.width > 0) || !(pane.height > 0) || !(screen.width > 0) || !(screen.height > 0)) return 1
  return Math.min(1, pane.width / screen.width, pane.height / screen.height)
}
