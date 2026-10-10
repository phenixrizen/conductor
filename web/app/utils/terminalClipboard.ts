/**
 * Copy and paste in the terminal, as Windows Terminal has them (round 15).
 *
 * Keys (Windows and Linux; on macOS the ⌘ keys stay the browser's and xterm's):
 * - Ctrl+Shift+C and Ctrl+Insert copy the selection. Ctrl+Shift+C is the page's even with nothing selected: it is Chrome's element
 *   picker otherwise.
 * - Ctrl+C with a selection copies it and clears it, without the interrupt; with none it stays the interrupt.
 * - Ctrl+Shift+V and Shift+Insert paste through the browser's own paste, which reaches xterm's text area: bracketed paste is kept, and
 *   it works where the page cannot read the clipboard (a plain-HTTP origin). Ctrl+V already pastes so.
 * - A connection that may not type (view only, or a read-only view) pastes nothing.
 *
 * Right-click: with a selection it copies and clears it, without one it pastes; Shift+right-click opens the terminal's menu (Copy, Paste,
 * Select all, "Right-click pastes"), and with that setting off right-click always opens it. While the program running asks for the
 * mouse (Codex's TUI, vim with mouse=a), a right-click is the program's, as in Windows Terminal: xterm reports it and nothing is copied,
 * pasted or opened. Shift, with which xterm takes the mouse back, still opens the menu with a right-click and selects with a drag; xterm
 * reports neither. On a Mac nothing takes it back (xterm's Option forcing is left off: its Option-click would move the program's
 * cursor), so those are the program's too; ⌘C and ⌘V still copy and paste.
 */
export interface ClipboardKeyEvent {
  type: string
  code: string
  ctrlKey: boolean
  shiftKey: boolean
  altKey: boolean
  metaKey: boolean
}

/** What a key does: copy the selection (and clear it), let the browser paste, swallow it, or nothing (xterm's as before). */
export type ClipboardKey = 'copy' | 'copy-and-clear' | 'paste' | 'swallow' | null

export function clipboardKey(e: ClipboardKeyEvent, ctx: { hasSelection: boolean; canPaste: boolean; mac: boolean }): ClipboardKey {
  if (ctx.mac || e.metaKey || e.altKey) return null
  const ctrl = e.ctrlKey
  const shift = e.shiftKey
  if (ctrl && shift && e.code === 'KeyC') return ctx.hasSelection ? 'copy' : 'swallow'
  if (ctrl && !shift && e.code === 'Insert') return ctx.hasSelection ? 'copy' : null
  if (ctrl && !shift && e.code === 'KeyC') return ctx.hasSelection ? 'copy-and-clear' : null
  if ((ctrl && shift && e.code === 'KeyV') || (!ctrl && shift && e.code === 'Insert')) return ctx.canPaste ? 'paste' : 'swallow'
  if (ctrl && !shift && e.code === 'KeyV' && !ctx.canPaste) return 'swallow'
  return null
}

/** What a right-click does: copy, paste, the terminal's menu, or nothing of ours ('app': the program asked for the mouse and has it). */
export type RightClick = 'copy' | 'paste' | 'menu' | 'app'

/**
 * appMouse: the program running asked for mouse reports when the button went down. It gets the click only on a connection that may type
 * (canPaste), since a view link's reports reach nothing; there the right-click copies or opens the menu as ever. force: the key xterm
 * takes the mouse back with (Shift; none on a Mac): with it the menu always opens, and xterm reports nothing. Shift opens the menu too,
 * except on a Mac over a program that holds the mouse, where xterm reports a Shift+right-click and so it is the program's.
 */
export function rightClick(ctx: { hasSelection: boolean; canPaste: boolean; pastes: boolean; shift: boolean; appMouse?: boolean; force?: boolean }): RightClick {
  if (ctx.force) return 'menu'
  if (ctx.appMouse && ctx.canPaste) return 'app'
  if (ctx.shift || !ctx.pastes) return 'menu'
  if (ctx.hasSelection) return 'copy'
  return ctx.canPaste ? 'paste' : 'menu'
}

/** What a right-button press held: the program's hold on the mouse, and the key that takes the mouse back from it. */
export interface PressHeld {
  appMouse: boolean
  force: boolean
}

/**
 * What brought the next menu event, kept until it comes: a press that brings one (the right button, Control-click on a Mac) with what it
 * held, or the keyboard's menu key. On Windows the menu event comes after the release, and by then the program may have let go of the
 * mouse (it had the press reported) or the forcing key may be up (xterm reported nothing). The menu key is noted when it goes down, since
 * not every browser marks its menu event (Firefox on Windows sends one like a click's). A press anywhere else, or one that brings no
 * menu, forgets what was kept (clear).
 */
export class RightPress {
  private held: PressHeld | null = null
  private key = false
  press(held: PressHeld) {
    this.held = held
    this.key = false
  }
  /** The keyboard's menu key went down. */
  menuKey() {
    this.held = null
    this.key = true
  }
  clear() {
    this.held = null
    this.key = false
  }
  /** What brought this menu event, read once: the keyboard (the menu key, or an event marked as the keyboard's), a press, or nothing. */
  take(e: { button: number; pointerType?: string }): PressHeld & { keyboard: boolean } {
    const keyboard = this.key || menuFromKeyboard(e)
    const held = keyboard || !this.held ? { appMouse: false, force: false } : this.held
    this.held = null
    this.key = false
    return { ...held, keyboard }
  }
}

/**
 * A menu event from the keyboard (the menu key; xterm sends Shift+F10 to the program), with no press behind it: Chromium sends it with
 * no button (-1), the Pointer Events spec with no pointer type. It opens the menu, never pastes and is never the program's.
 */
export function menuFromKeyboard(e: { button: number; pointerType?: string }): boolean {
  return e.button < 0 || e.pointerType === ''
}

/** A press that brings a menu event after it: the right button, or Control with the main button on a Mac. */
export function menuPress(e: { button: number; ctrlKey: boolean }, mac: boolean): boolean {
  return e.button === 2 || (mac && e.button === 0 && e.ctrlKey)
}

/**
 * The key xterm forces a selection with while the program holds the mouse, and that takes a right-click back from it: Shift, and none on
 * a Mac, where xterm would want Option with macOptionClickForcesSelection, left off since its Option-click moves the program's cursor.
 */
export function forcesSelection(e: { shiftKey: boolean; altKey: boolean }, mac: boolean): boolean {
  return !mac && e.shiftKey
}

/** Where the right-click setting is kept, per browser. */
export const RIGHT_CLICK_KEY = 'conductor.terminal.rightClickPastes'

type KeyValueStore = Pick<Storage, 'getItem' | 'setItem'>

/** Whether right-click copies or pastes (the default) or, turned off, opens the menu. */
export function readRightClickPastes(storage: KeyValueStore | null): boolean {
  try {
    return storage?.getItem(RIGHT_CLICK_KEY) !== 'false'
  } catch {
    return true
  }
}

export function writeRightClickPastes(storage: KeyValueStore | null, pastes: boolean): void {
  try {
    storage?.setItem(RIGHT_CLICK_KEY, pastes ? 'true' : 'false')
  } catch {
    /* a private window: the choice lasts the page */
  }
}
