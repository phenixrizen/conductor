import { describe, expect, it } from 'vitest'
import { crumbs, parentDir, pickerStart, underRoots } from './dirPicker'

describe('the folder picker', () => {
  it('goes up one directory, stopping at the root', () => {
    expect(parentDir('/home/me/code')).toBe('/home/me')
    expect(parentDir('/home/me/')).toBe('/home')
    expect(parentDir('/home')).toBe('/')
    expect(parentDir('/')).toBe('/')
  })
  it('makes a breadcrumb from the root down', () => {
    expect(crumbs('/home/me')).toEqual([
      { name: '/', path: '/' },
      { name: 'home', path: '/home' },
      { name: 'me', path: '/home/me' },
    ])
    expect(crumbs('/')).toEqual([{ name: '/', path: '/' }])
  })
  it('tells a directory under the roots from one outside, a prefix of a name not counting', () => {
    expect(underRoots('/home/me/code', ['/home/me'])).toBe(true)
    expect(underRoots('/home/me', ['/home/me/'])).toBe(true)
    expect(underRoots('/home/meow', ['/home/me'])).toBe(false)
    expect(underRoots('/srv', ['/home/me', '/'])).toBe(true)
  })
  it('opens on the field it fills', () => {
    const form = { allowedRoots: ['/home/me'], defaultCwd: '/home/me/code', dataDir: '/home/me/.local/share/conductor/data' }
    expect(pickerStart('root', form)).toBe('/home/me')
    expect(pickerStart('root', { ...form, allowedRoots: [] })).toBe('/home/me/code')
    expect(pickerStart('defaultCwd', form)).toBe('/home/me/code')
    expect(pickerStart('dataDir', form)).toBe(form.dataDir)
  })
})
