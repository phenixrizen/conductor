import { describe, expect, it } from 'vitest'
import { AVATAR_TONES, avatarTone } from './avatar'

describe('avatarTone', () => {
  it('gives a name the same tone every time, from the brand palette', () => {
    for (const name of ['Nate', 'Priya Shah', 'Jane', '', '🚂']) {
      const tone = avatarTone(name)
      expect(AVATAR_TONES).toContain(tone)
      expect(avatarTone(name)).toBe(tone)
    }
  })
  it('spreads names over the tones', () => {
    const names = ['Nate', 'Priya', 'Jane', 'Omar', 'Lee', 'Sam', 'Ada', 'Kim', 'Noor', 'Ivy', 'Max', 'Zoe']
    expect(new Set(names.map(avatarTone)).size).toBeGreaterThan(1)
  })
})
