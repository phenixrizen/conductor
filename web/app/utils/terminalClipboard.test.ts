import { describe, expect, it } from 'vitest'
import { clipboardKey, readRightClickPastes, rightClick, RIGHT_CLICK_KEY, writeRightClickPastes, type KeyLike } from './terminalClipboard'

const key = (code: string, mods: Partial<KeyLike> = {}): KeyLike => ({ type: 'keydown', code, ctrlKey: false, shiftKey: false, altKey: false, metaKey: false, ...mods })
const ctx = (o: Partial<{ hasSelection: boolean; canPaste: boolean; mac: boolean }> = {}) => ({ hasSelection: false, canPaste: true, mac: false, ...o })

describe('the terminal clipboard keys', () => {
  it('copies with Ctrl+Shift+C and Ctrl+Insert, and takes Ctrl+Shift+C from the browser even with nothing selected', () => {
    expect(clipboardKey(key('KeyC', { ctrlKey: true, shiftKey: true }), ctx({ hasSelection: true }))).toBe('copy')
    expect(clipboardKey(key('KeyC', { ctrlKey: true, shiftKey: true }), ctx())).toBe('swallow')
    expect(clipboardKey(key('Insert', { ctrlKey: true }), ctx({ hasSelection: true }))).toBe('copy')
    expect(clipboardKey(key('Insert', { ctrlKey: true }), ctx())).toBe(null)
  })
  it('copies and clears with Ctrl+C on a selection; without one Ctrl+C stays the interrupt', () => {
    expect(clipboardKey(key('KeyC', { ctrlKey: true }), ctx({ hasSelection: true }))).toBe('copy-and-clear')
    expect(clipboardKey(key('KeyC', { ctrlKey: true }), ctx())).toBe(null)
  })
  it("pastes with Ctrl+Shift+V and Shift+Insert through the browser's own paste", () => {
    expect(clipboardKey(key('KeyV', { ctrlKey: true, shiftKey: true }), ctx())).toBe('paste')
    expect(clipboardKey(key('Insert', { shiftKey: true }), ctx())).toBe('paste')
    expect(clipboardKey(key('KeyV', { ctrlKey: true }), ctx())).toBe(null)
  })
  it('pastes nothing on a connection that may not type, and still copies there', () => {
    const view = ctx({ canPaste: false, hasSelection: true })
    expect(clipboardKey(key('KeyV', { ctrlKey: true, shiftKey: true }), view)).toBe('swallow')
    expect(clipboardKey(key('Insert', { shiftKey: true }), view)).toBe('swallow')
    expect(clipboardKey(key('KeyV', { ctrlKey: true }), view)).toBe('swallow')
    expect(clipboardKey(key('KeyC', { ctrlKey: true, shiftKey: true }), view)).toBe('copy')
  })
  it('leaves macOS, the ⌘ keys, the Alt chords and every other key alone', () => {
    expect(clipboardKey(key('KeyC', { ctrlKey: true, shiftKey: true }), ctx({ mac: true, hasSelection: true }))).toBe(null)
    expect(clipboardKey(key('KeyC', { metaKey: true }), ctx({ hasSelection: true }))).toBe(null)
    expect(clipboardKey(key('KeyC', { altKey: true, ctrlKey: true }), ctx({ hasSelection: true }))).toBe(null)
    expect(clipboardKey(key('KeyA', { ctrlKey: true }), ctx({ hasSelection: true }))).toBe(null)
  })
})

describe('the terminal right-click', () => {
  const base = { hasSelection: false, canPaste: true, pastes: true, shift: false }
  it('copies a selection, else pastes', () => {
    expect(rightClick({ ...base, hasSelection: true })).toBe('copy')
    expect(rightClick(base)).toBe('paste')
  })
  it('opens the menu with Shift, when turned off, or when it may not paste', () => {
    expect(rightClick({ ...base, shift: true, hasSelection: true })).toBe('menu')
    expect(rightClick({ ...base, pastes: false })).toBe('menu')
    expect(rightClick({ ...base, canPaste: false })).toBe('menu')
    expect(rightClick({ ...base, canPaste: false, hasSelection: true })).toBe('copy')
  })
  it('keeps the setting per browser, on by default', () => {
    const m = new Map<string, string>()
    const store = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) }
    expect(readRightClickPastes(store)).toBe(true)
    writeRightClickPastes(store, false)
    expect(m.get(RIGHT_CLICK_KEY)).toBe('false')
    expect(readRightClickPastes(store)).toBe(false)
    expect(readRightClickPastes(null)).toBe(true)
  })
})
