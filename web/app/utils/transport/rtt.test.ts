import { describe, expect, it } from 'vitest'
import { rttFromPong } from './rtt'

describe('rttFromPong', () => {
  it('returns the elapsed time and rejects nonsense', () => {
    expect(rttFromPong(1000, 1014)).toBe(14)
    expect(rttFromPong(1000, 999)).toBeNull()
    expect(rttFromPong(0, 70000)).toBeNull()
  })
})
