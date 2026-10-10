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
 * Select all, "Right-click pastes"), and with that setting off right-click always opens it.
 */
export interface KeyLike {
  type: string
  code: string
  ctrlKey: boolean
  shiftKey: boolean
  altKey: boolean
  metaKey: boolean
}

/** What a key does: copy the selection (and clear it), let the browser paste, swallow it, or nothing (xterm's as before). */
export type ClipboardKey = 'copy' | 'copy-and-clear' | 'paste' | 'swallow' | null

export function clipboardKey(e: KeyLike, ctx: { hasSelection: boolean; canPaste: boolean; mac: boolean }): ClipboardKey {
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

/** What a right-click does. */
export type RightClick = 'copy' | 'paste' | 'menu'

export function rightClick(ctx: { hasSelection: boolean; canPaste: boolean; pastes: boolean; shift: boolean }): RightClick {
  if (ctx.shift || !ctx.pastes) return 'menu'
  if (ctx.hasSelection) return 'copy'
  return ctx.canPaste ? 'paste' : 'menu'
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
