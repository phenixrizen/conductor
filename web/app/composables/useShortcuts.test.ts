import { describe, expect, it } from 'vitest'
import { CAROUSEL_SHORTCUTS, GLOBAL_SHORTCUTS, WALL_SHORTCUTS, type ShortcutGroup } from './useShortcuts'

const fRows = (g: ShortcutGroup) => g.rows.filter((r) => r.keys.length === 1 && r.keys[0] === 'F')

describe('the fullscreen shortcut', () => {
  it('is listed once, under Everywhere, and no longer by the wall or the carousel', () => {
    expect(fRows(GLOBAL_SHORTCUTS)).toHaveLength(1)
    expect(fRows(GLOBAL_SHORTCUTS)[0]!.label).toBe('Toggle fullscreen (Alt+F in a terminal)')
    expect(fRows(WALL_SHORTCUTS)).toHaveLength(0)
    expect(fRows(CAROUSEL_SHORTCUTS)).toHaveLength(0)
  })
})

describe('the sidebar shortcut', () => {
  it('says it toggles the rail', () => {
    expect(GLOBAL_SHORTCUTS.rows.find((r) => r.keys.join('+') === 'meta+B')?.label).toBe('Collapse the sidebar to the rail, or expand it (Alt+B in a terminal)')
  })
})
