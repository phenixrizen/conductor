import { describe, expect, it } from 'vitest'
import { clipboardKey, forcesSelection, menuFromKeyboard, menuPress, readRightClickPastes, rightClick, RightPress, RIGHT_CLICK_KEY, withholdsMove, writeRightClickPastes, type ClipboardKeyEvent, type MouseTracking } from './terminalClipboard'

const key = (code: string, mods: Partial<ClipboardKeyEvent> = {}): ClipboardKeyEvent => ({ type: 'keydown', code, ctrlKey: false, shiftKey: false, altKey: false, metaKey: false, ...mods })
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
  it('leaves the click to a program that asked for the mouse, unless the forcing key is held or the connection may not type', () => {
    expect(rightClick({ ...base, appMouse: true })).toBe('app')
    expect(rightClick({ ...base, appMouse: true, hasSelection: true })).toBe('app')
    expect(rightClick({ ...base, appMouse: true, pastes: false })).toBe('app')
    expect(rightClick({ ...base, appMouse: true, shift: true, force: true })).toBe('menu')
    // On a Mac Shift is not the forcing key: xterm reports a Shift+right-click, so it stays the program's.
    expect(rightClick({ ...base, appMouse: true, shift: true, force: false })).toBe('app')
    expect(rightClick({ ...base, appMouse: true, canPaste: false })).toBe('menu')
    expect(rightClick({ ...base, appMouse: true, canPaste: false, hasSelection: true })).toBe('copy')
  })
  it('keeps what the press held for one menu event; a menu with no press held nothing', () => {
    const r = new RightPress()
    const click = { button: 2, pointerType: 'mouse' }
    r.press({ appMouse: true, force: true })
    expect(r.take(click)).toEqual({ appMouse: true, force: true, keyboard: false })
    expect(r.take(click)).toEqual({ appMouse: false, force: false, keyboard: false })
    r.press({ appMouse: true, force: false })
    r.clear()
    expect(r.take(click)).toEqual({ appMouse: false, force: false, keyboard: false })
  })
  it('takes the menu key for the keyboard\'s even when its event looks like a click (Firefox on Windows), after an abandoned press', () => {
    const r = new RightPress()
    const firefox = { button: 0, pointerType: 'mouse' }
    r.press({ appMouse: true, force: false })
    r.menuKey()
    expect(r.take(firefox)).toEqual({ appMouse: false, force: false, keyboard: true })
    r.menuKey()
    r.press({ appMouse: true, force: false })
    expect(r.take({ button: 2, pointerType: 'mouse' })).toEqual({ appMouse: true, force: false, keyboard: false })
    r.menuKey()
    r.clear()
    expect(r.take(firefox).keyboard).toBe(false)
    expect(r.take({ button: -1, pointerType: 'mouse' }).keyboard).toBe(true)
  })
  it('tells a menu from the keyboard (no button, or no pointer type) from a pointer\'s', () => {
    expect(menuFromKeyboard({ button: -1, pointerType: 'mouse' })).toBe(true)
    expect(menuFromKeyboard({ button: 0, pointerType: '' })).toBe(true)
    expect(menuFromKeyboard({ button: 2, pointerType: 'mouse' })).toBe(false)
    expect(menuFromKeyboard({ button: 0 })).toBe(false)
  })
  it('knows the presses that bring a menu: the right button, and Control-click on a Mac', () => {
    expect(menuPress({ button: 2, ctrlKey: false }, false)).toBe(true)
    expect(menuPress({ button: 0, ctrlKey: true }, true)).toBe(true)
    expect(menuPress({ button: 0, ctrlKey: true }, false)).toBe(false)
    expect(menuPress({ button: 0, ctrlKey: false }, true)).toBe(false)
    expect(menuPress({ button: 1, ctrlKey: false }, false)).toBe(false)
  })
  it('opens the menu with the forcing key whatever else holds (at a prompt or on a view link)', () => {
    expect(rightClick({ ...base, force: true })).toBe('menu')
    expect(rightClick({ ...base, force: true, hasSelection: true })).toBe('menu')
    expect(rightClick({ ...base, force: true, canPaste: false, hasSelection: true })).toBe('menu')
    expect(rightClick({ ...base, force: true, appMouse: true, canPaste: false })).toBe('menu')
    // A Mac's Shift at a prompt still opens the menu.
    expect(rightClick({ ...base, shift: true, force: false })).toBe('menu')
  })
  it('forces with Shift, and with nothing on a Mac', () => {
    expect(forcesSelection({ shiftKey: true, altKey: false }, false)).toBe(true)
    expect(forcesSelection({ shiftKey: false, altKey: true }, false)).toBe(false)
    expect(forcesSelection({ shiftKey: false, altKey: true }, true)).toBe(false)
    expect(forcesSelection({ shiftKey: true, altKey: false }, true)).toBe(false)
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

describe('the terminal mouse moves', () => {
  const move = (o: Partial<{ buttons: number; shiftKey: boolean; altKey: boolean }> = {}) => ({ buttons: 0, shiftKey: false, altKey: false, ...o })
  const at = (o: Partial<{ tracking: MouseTracking; hasSelection: boolean; mac: boolean }> = {}) => ({ tracking: 'any' as MouseTracking, hasSelection: false, mac: false, ...o })
  it('keeps a move with no button from a program that asks for every move while Shift is held or a selection shows', () => {
    expect(withholdsMove(move({ shiftKey: true }), at())).toBe(true)
    expect(withholdsMove(move(), at({ hasSelection: true }))).toBe(true)
    expect(withholdsMove(move({ shiftKey: true }), at({ hasSelection: true }))).toBe(true)
  })
  it('lets a plain move through to the program while no selection shows', () => {
    expect(withholdsMove(move(), at())).toBe(false)
    expect(withholdsMove(move({ altKey: true }), at())).toBe(false)
  })
  it('never keeps a move with a button held: it is a drag, which the selection and a program that asked for drags need', () => {
    for (const buttons of [1, 2, 4, 3]) {
      expect(withholdsMove(move({ buttons, shiftKey: true }), at())).toBe(false)
      expect(withholdsMove(move({ buttons }), at({ hasSelection: true }))).toBe(false)
    }
  })
  it('keeps nothing under any other tracking: xterm reports no move without a button there', () => {
    for (const tracking of ['none', 'x10', 'vt200', 'drag'] as MouseTracking[]) {
      expect(withholdsMove(move({ shiftKey: true }), at({ tracking }))).toBe(false)
      expect(withholdsMove(move(), at({ tracking, hasSelection: true }))).toBe(false)
      expect(withholdsMove(move({ shiftKey: true }), at({ tracking, hasSelection: true }))).toBe(false)
    }
  })
  it('leaves Shift to the program on a Mac, and keeps the moves there only while a selection shows', () => {
    expect(withholdsMove(move({ shiftKey: true }), at({ mac: true }))).toBe(false)
    expect(withholdsMove(move({ altKey: true }), at({ mac: true }))).toBe(false)
    expect(withholdsMove(move(), at({ mac: true, hasSelection: true }))).toBe(true)
    expect(withholdsMove(move({ buttons: 1 }), at({ mac: true, hasSelection: true }))).toBe(false)
  })
})
