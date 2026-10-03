import { describe, expect, it } from 'vitest'
import { updateChannel } from '../src/updater'

describe('update channel', () => {
  it('updates itself on macOS, Windows and AppImage, points at the releases for deb and rpm, and never in development', () => {
    expect(updateChannel('darwin', false, true)).toBe('auto')
    expect(updateChannel('win32', false, true)).toBe('auto')
    expect(updateChannel('linux', true, true)).toBe('auto')
    expect(updateChannel('linux', false, true)).toBe('link')
    expect(updateChannel('linux', true, false)).toBe('none')
  })
})
