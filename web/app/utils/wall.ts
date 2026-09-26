export interface GridLayout {
  cols: number
  rows: number
}

/**
 * Picks the column count that gives `n` equally sized tiles the largest
 * possible size inside a `width` × `height` box, given a gap between tiles
 * and the tiles' preferred aspect ratio (width / height). Every tile stays
 * visible: the grid never scrolls, tiles shrink as sessions are added.
 */
export function bestGrid(n: number, width: number, height: number, gap = 8, aspect = 1.6): GridLayout {
  if (n <= 0) return { cols: 1, rows: 1 }
  if (width <= 0 || height <= 0) return { cols: Math.ceil(Math.sqrt(n)), rows: Math.ceil(n / Math.ceil(Math.sqrt(n))) }
  let best: GridLayout & { size: number } = { cols: 1, rows: n, size: -1 }
  for (let cols = 1; cols <= n; cols++) {
    const rows = Math.ceil(n / cols)
    const cellW = (width - gap * (cols - 1)) / cols
    const cellH = (height - gap * (rows - 1)) / rows
    const tileW = Math.min(cellW, cellH * aspect)
    if (tileW > best.size + 0.5) best = { cols, rows, size: tileW }
  }
  return { cols: best.cols, rows: best.rows }
}
