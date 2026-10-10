import { describe, expect, it } from 'vitest'
import { ALT_PASSTHROUGH_CODES, CAROUSEL_SHORTCUTS, EDITOR_SHORTCUTS, GLOBAL_SHORTCUTS, SIDEBAR_SHORTCUTS, WALL_SHORTCUTS, type ShortcutGroup } from './useShortcuts'

const fRows = (g: ShortcutGroup) => g.rows.filter((r) => r.keys.length === 1 && r.keys[0] === 'F')

describe('the fullscreen shortcut', () => {
  it('is listed once, under Everywhere, and no longer by the wall or the carousel', () => {
    expect(fRows(GLOBAL_SHORTCUTS)).toHaveLength(1)
    expect(fRows(GLOBAL_SHORTCUTS)[0]!.label).toBe('Toggle fullscreen')
    expect(fRows(GLOBAL_SHORTCUTS)[0]!.terminal).toEqual(['alt', 'F'])
    expect(fRows(WALL_SHORTCUTS)).toHaveLength(0)
    expect(fRows(CAROUSEL_SHORTCUTS)).toHaveLength(0)
  })
})

describe('the sidebar shortcut', () => {
  it('says it toggles the rail', () => {
    expect(GLOBAL_SHORTCUTS.rows.find((r) => r.keys.join('+') === 'meta+B')?.label).toBe('Collapse the sidebar to the rail, or expand it')
  })
})

describe('the sidebar shortcuts', () => {
  it('list the filter and every row key, after Everywhere, and take / with them', () => {
    expect(GLOBAL_SHORTCUTS.rows.some((r) => r.keys.join('') === '/')).toBe(false)
    expect(SIDEBAR_SHORTCUTS.title).toBe('The sidebar')
    expect(SIDEBAR_SHORTCUTS.rows.map((r) => r.keys.join('+'))).toEqual(['/', 'arrowdown', 'J', 'K', 'enter', '1', 'R', 'S', 'X', 'escape'])
    expect(SIDEBAR_SHORTCUTS.rows.find((r) => r.keys[0] === 'X')?.label).toBe('Stop it: the row asks; Enter stops, Escape cancels')
  })
})

describe('the page chords', () => {
  const chord = (second: string) => GLOBAL_SHORTCUTS.rows.find((r) => r.keys[0] === 'G' && r.keys[1] === second)
  it("go by each page's initial: Y the Yard, R the Roundhouse, C Crews, A Agents, E Events, each with its Alt twin in a terminal", () => {
    expect(chord('Y')).toEqual({ keys: ['G', 'Y'], label: 'Go to the Yard', terminal: ['alt', 'Y'] })
    expect(chord('R')).toEqual({ keys: ['G', 'R'], label: 'Go to the Roundhouse', terminal: ['alt', 'R'] })
    expect(chord('C')).toEqual({ keys: ['G', 'C'], label: 'Go to Crews', terminal: ['alt', 'C'] })
    expect(chord('A')?.terminal).toEqual(['alt', 'A'])
    expect(chord('E')?.terminal).toEqual(['alt', 'E'])
    expect(chord('W')).toBeUndefined()
  })
  it('name no Alt twin for what needs the list or the page focused, and no label says "in a terminal" any more', () => {
    for (const g of [GLOBAL_SHORTCUTS, SIDEBAR_SHORTCUTS, WALL_SHORTCUTS, CAROUSEL_SHORTCUTS]) for (const r of g.rows) expect(r.label).not.toMatch(/in a terminal/)
    expect(SIDEBAR_SHORTCUTS.rows.find((r) => r.keys[0] === 'J')?.terminal).toBeUndefined()
    expect(WALL_SHORTCUTS.rows.find((r) => r.keys[0] === 'J')?.terminal).toEqual(['alt', 'J'])
  })
  it('hand their Alt twins back from a terminal, and Alt+W no longer', () => {
    for (const code of ['KeyY', 'KeyR', 'KeyC', 'KeyA', 'KeyE']) expect(ALT_PASSTHROUGH_CODES.has(code)).toBe(true)
    expect(ALT_PASSTHROUGH_CODES.has('KeyW')).toBe(false)
  })
})

describe("the editor's keys", () => {
  it('fold with T and its Alt twin, switch with Ctrl+Tab, close with Ctrl+W, find and go to a line', () => {
    const fold = EDITOR_SHORTCUTS.rows.find((r) => r.keys.join('') === 'T')
    expect(fold?.terminal).toEqual(['alt', 'T'])
    expect(ALT_PASSTHROUGH_CODES.has('KeyT')).toBe(true)
    expect(EDITOR_SHORTCUTS.rows.map((r) => r.keys.join('+'))).toEqual(['T', 'ctrl+tab', 'ctrl+W', 'ctrl+F', 'ctrl+G'])
    for (const r of EDITOR_SHORTCUTS.rows) expect(r.label).not.toMatch(/in a terminal/)
  })
})

describe('the terminal group', () => {
  it('lists copy and paste with their keys in a terminal, none outside one', async () => {
    const { TERMINAL_SHORTCUTS } = await import('./useShortcuts')
    expect(TERMINAL_SHORTCUTS.rows.map((r) => r.terminal?.join('+') ?? '')).toEqual(['ctrl+shift+C', 'ctrl+C', 'ctrl+shift+V', ''])
    expect(TERMINAL_SHORTCUTS.rows.every((r) => r.keys.length === 0)).toBe(true)
  })
})
