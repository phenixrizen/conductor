import { describe, expect, it } from 'vitest'
import { bestGrid } from './wall'

describe('bestGrid', () => {
  it('handles empty and degenerate boxes', () => {
    expect(bestGrid(0, 1000, 800)).toEqual({ cols: 1, rows: 1 })
    expect(bestGrid(4, 0, 0)).toEqual({ cols: 2, rows: 2 })
  })

  it('uses one row while the tiles stay wide enough', () => {
    expect(bestGrid(1, 1600, 900)).toEqual({ cols: 1, rows: 1 })
    expect(bestGrid(2, 1600, 900)).toEqual({ cols: 2, rows: 1 })
  })

  it('prefers bigger tiles with an empty cell over a cramped row', () => {
    // Three tiles: 2×2 gives 796×446 cells, a single row only 528 px wide.
    expect(bestGrid(3, 1600, 900)).toEqual({ cols: 2, rows: 2 })
  })

  it('wraps into rows on a widescreen box', () => {
    expect(bestGrid(4, 1600, 900)).toEqual({ cols: 2, rows: 2 })
    expect(bestGrid(6, 1600, 900)).toEqual({ cols: 3, rows: 2 })
    expect(bestGrid(9, 1600, 900)).toEqual({ cols: 3, rows: 3 })
    expect(bestGrid(12, 1600, 900)).toEqual({ cols: 4, rows: 3 })
    expect(bestGrid(20, 1600, 900)).toEqual({ cols: 5, rows: 4 })
  })

  it('stacks vertically on a portrait box', () => {
    expect(bestGrid(3, 600, 1200)).toEqual({ cols: 1, rows: 3 })
    expect(bestGrid(4, 600, 1200)).toEqual({ cols: 1, rows: 4 })
    expect(bestGrid(8, 600, 1200)).toEqual({ cols: 2, rows: 4 })
  })

  it('grows the tile size monotonically as the box grows', () => {
    // Same count, wider box: never fewer columns.
    expect(bestGrid(6, 1000, 900).cols).toBeLessThanOrEqual(bestGrid(6, 2000, 900).cols)
  })

  it('never leaves a tile outside the grid', () => {
    for (let n = 1; n <= 40; n++) {
      const { cols, rows } = bestGrid(n, 1440, 800)
      expect(cols * rows).toBeGreaterThanOrEqual(n)
      expect(cols).toBeLessThanOrEqual(n)
    }
  })
})
