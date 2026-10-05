import { describe, expect, it } from 'vitest'
import { clampZoom, nextZoom, zoomKey, ZOOM_MAX, ZOOM_MIN, type ZoomInput } from '../src/zoom'

const key = (k: string, code: string, mods: Partial<ZoomInput> = {}): ZoomInput => ({ type: 'keyDown', key: k, code, control: false, meta: false, alt: false, ...mods })

describe('zoom keys', () => {
  it('takes Ctrl+= on Windows and Linux, with or without Shift, and the numpad', () => {
    for (const p of ['win32', 'linux'] as const) {
      expect(zoomKey(key('=', 'Equal', { control: true }), p)).toBe('in')
      expect(zoomKey(key('+', 'Equal', { control: true }), p)).toBe('in')
      expect(zoomKey(key('+', 'NumpadAdd', { control: true }), p)).toBe('in')
      expect(zoomKey(key('-', 'Minus', { control: true }), p)).toBe('out')
      expect(zoomKey(key('-', 'NumpadSubtract', { control: true }), p)).toBe('out')
      expect(zoomKey(key('0', 'Digit0', { control: true }), p)).toBe('reset')
      expect(zoomKey(key('0', 'Numpad0', { control: true }), p)).toBe('reset')
    }
  })
  it('takes Cmd on macOS and leaves Ctrl to the terminal there', () => {
    expect(zoomKey(key('=', 'Equal', { meta: true }), 'darwin')).toBe('in')
    expect(zoomKey(key('-', 'Minus', { control: true }), 'darwin')).toBeNull()
    expect(zoomKey(key('=', 'Equal', { meta: true }), 'win32')).toBeNull()
  })
  it('leaves everything else to the page', () => {
    expect(zoomKey(key('=', 'Equal'), 'win32')).toBeNull()
    expect(zoomKey(key('=', 'Equal', { control: true, alt: true }), 'win32')).toBeNull()
    expect(zoomKey({ ...key('=', 'Equal', { control: true }), type: 'keyUp' }, 'win32')).toBeNull()
    expect(zoomKey(key('c', 'KeyC', { control: true }), 'win32')).toBeNull()
    expect(zoomKey(key('1', 'Digit1', { control: true }), 'win32')).toBeNull()
  })
})

describe('zoom level', () => {
  it('steps by half a level within its bounds, and resets to 0', () => {
    expect(nextZoom(0, 'in')).toBe(0.5)
    expect(nextZoom(0, 'out')).toBe(-0.5)
    expect(nextZoom(ZOOM_MAX, 'in')).toBe(ZOOM_MAX)
    expect(nextZoom(ZOOM_MIN, 'out')).toBe(ZOOM_MIN)
    expect(nextZoom(2, 'reset')).toBe(0)
  })
  it('clamps what a settings file holds', () => {
    expect(clampZoom(undefined)).toBe(0)
    expect(clampZoom('2')).toBe(0)
    expect(clampZoom(Number.NaN)).toBe(0)
    expect(clampZoom(99)).toBe(ZOOM_MAX)
    expect(clampZoom(-99)).toBe(ZOOM_MIN)
    expect(clampZoom(1.2)).toBe(1)
  })
})
