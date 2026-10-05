import { describe, expect, it, vi } from 'vitest'

vi.mock('electron', () => ({ BrowserWindow: class {}, shell: {} }))
const { buildLine, copyrightLine, CREDIT, splashQuery, SPLASH_STEPS } = await import('../src/splash')

describe('splash', () => {
  it('says the build', () => {
    expect(buildLine('0.6.0-rc.4', 'win32', 'x64', '44.5.1')).toBe('0.6.0-rc.4 · windows x64 · electron 44.5.1')
    expect(buildLine('0.6.0', 'darwin', 'arm64', '')).toBe('0.6.0 · macos arm64')
    expect(buildLine('0.6.0', 'linux', 'x64', '44.5.1')).toBe('0.6.0 · linux x64 · electron 44.5.1')
  })
  it('dates the copyright from the first release', () => {
    expect(copyrightLine(2026)).toBe('© 2026 the Conductor authors')
    expect(copyrightLine(2027)).toBe('© 2026–2027 the Conductor authors')
  })
  it('carries the kit credit and the build to the page', () => {
    expect(CREDIT).toBe('Sponsored and maintained by RockSolid Labs')
    expect(splashQuery({ version: '1.0.0', build: 'b', year: 2026 })).toEqual({ version: '1.0.0', build: 'b', copyright: '© 2026 the Conductor authors', credit: CREDIT })
  })
  it('names each step', () => {
    expect(Object.values(SPLASH_STEPS).every((s) => s.endsWith('…'))).toBe(true)
  })
})
