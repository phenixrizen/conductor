import { FOLLOW_SIZE } from './protocol'

/** A tile's terminal font size, in pixels: fixed, so a tile fits more columns than a scaled view of the PTY's grid. */
export const TILE_FONT_SIZE = 11

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
 * The factor a tile shrinks its terminal by to show the whole screen when another viewer has sized the session larger than the tile (latest
 * controller wins): 1 when the screen, with its padding, fits the pane; never more than 1, and 1 when either box is not laid out.
 */
export function tileScale(pane: Box, screen: Box): number {
  if (!(pane.width > 0) || !(pane.height > 0) || !(screen.width > 0) || !(screen.height > 0)) return 1
  return Math.min(1, pane.width / screen.width, pane.height / screen.height)
}
