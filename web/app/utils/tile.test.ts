import { describe, expect, it } from 'vitest'
import { MAX_DIMENSION, helloSize, tileFit, tileScale } from './tile'

describe('helloSize', () => {
  it('carries the fitted size of a view that sizes the session', () => {
    expect(helloSize(true, 111, 31)).toEqual({ cols: 111, rows: 31 })
  })

  it('is 0×0, follow the session, for a view that does not size it', () => {
    expect(helloSize(false, 111, 31)).toEqual({ cols: 0, rows: 0 })
  })

  it('is 0×0 for a pane not laid out yet, never a default 80×24', () => {
    expect(helloSize(true, 0, 24)).toEqual({ cols: 0, rows: 0 })
    expect(helloSize(true, 80, 0)).toEqual({ cols: 0, rows: 0 })
    expect(helloSize(true, Number.NaN, 24)).toEqual({ cols: 0, rows: 0 })
  })

  it('never asks for more than the protocol takes', () => {
    expect(helloSize(true, 1536, 700)).toEqual({ cols: MAX_DIMENSION, rows: MAX_DIMENSION })
  })
})

describe('tileScale', () => {
  it('is 1 while the screen fits the pane', () => {
    expect(tileScale({ width: 570, height: 346 }, { width: 568, height: 344 })).toBe(1)
  })

  it('shrinks a screen another viewer made larger, by the tighter side', () => {
    expect(tileScale({ width: 570, height: 346 }, { width: 1140, height: 346 })).toBe(0.5)
    expect(tileScale({ width: 570, height: 346 }, { width: 570, height: 692 })).toBe(0.5)
  })

  it('is 1 for a box not laid out', () => {
    expect(tileScale({ width: 0, height: 346 }, { width: 1140, height: 346 })).toBe(1)
    expect(tileScale({ width: 570, height: 346 }, { width: 0, height: 0 })).toBe(1)
  })
})

describe('tileFit', () => {
  // An 11 px cell of JetBrains Mono, as a tile draws it; 0.25 rem of padding a side.
  const cell = { width: 6.6, height: 13 }
  const padding = { width: 8, height: 8 }

  it('fills the pane less the padding: no gutter kept for a scrollbar, so less than a cell is left beside the grid', () => {
    // The fit addon keeps 14 px for a scrollbar once the terminal has a scrollback: floor((300 - 8 - 14) / 6.6) = 42.
    expect(tileFit({ width: 300, height: 100 }, padding, cell)).toEqual({ cols: 44, rows: 7 })
    const { cols } = tileFit({ width: 300, height: 100 }, padding, cell)!
    expect(300 - 8 - cols * cell.width).toBeLessThan(cell.width)
  })

  it('keeps at least 2 × 1, as the fit addon does', () => {
    expect(tileFit({ width: 4, height: 4 }, padding, cell)).toEqual({ cols: 2, rows: 1 })
  })

  it('is undefined before the cell is measured or with a box that is not a number', () => {
    expect(tileFit({ width: 300, height: 100 }, padding, { width: 0, height: 13 })).toBeUndefined()
    expect(tileFit({ width: 300, height: 100 }, padding, { width: 6.6, height: Number.NaN })).toBeUndefined()
    expect(tileFit({ width: Number.NaN, height: 100 }, padding, cell)).toBeUndefined()
  })
})
