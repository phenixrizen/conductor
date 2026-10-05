import { describe, expect, it } from 'vitest'
import { copyrightLine, CREDIT, versionOn } from './about'

describe('about', () => {
  it('says the credit in the kit words', () => {
    expect(CREDIT).toBe('Sponsored and maintained by RockSolid Labs')
  })
  it('dates the copyright from the first release', () => {
    expect(copyrightLine(2026)).toBe('© 2026 the Conductor authors')
    expect(copyrightLine(2028)).toBe('© 2026–2028 the Conductor authors')
    expect(copyrightLine(2020)).toBe('© 2026 the Conductor authors')
  })
  it('joins a version and its machine', () => {
    expect(versionOn('0.6.0', 'box')).toBe('0.6.0 · box')
    expect(versionOn('', 'box')).toBe('box')
    expect(versionOn('0.6.0', '')).toBe('0.6.0')
  })
})
