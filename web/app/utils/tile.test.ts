import { describe, expect, it } from 'vitest'
import { MAX_DIMENSION, helloSize, tileScale } from './tile'

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
