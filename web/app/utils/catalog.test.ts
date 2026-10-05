import { describe, expect, it } from 'vitest'
import { removalOf, removalText } from './catalog'

describe('removalOf', () => {
  it('hides what comes from the built-ins or the config', () => {
    expect(removalOf({ source: 'built-in' })).toBe('hide')
    expect(removalOf({ source: 'config' })).toBe('hide')
    expect(removalOf({})).toBe('hide')
  })
  it('reverts a saved change and deletes a saved addition', () => {
    expect(removalOf({ source: 'saved', replaces: 'built-in' })).toBe('revert')
    expect(removalOf({ source: 'saved', replaces: 'config' })).toBe('revert')
    expect(removalOf({ source: 'saved' })).toBe('delete')
  })
})

describe('removalText', () => {
  it('says what each removal does', () => {
    expect(removalText('hide', 'Aider', 'aider')).toMatchObject({ button: 'Hide', toast: { title: 'Hidden' } })
    expect(removalText('revert', 'Claude Code', 'claude').toast.description).toBe('claude is back to its original definition.')
    expect(removalText('delete', 'Mine', 'mine')).toMatchObject({ button: 'Delete', title: 'Delete Mine?', toast: { title: 'Removed', description: 'Mine' } })
  })
})
