/**
 * The editor's Neovim keymap (design round 12, F8): a key the person presses
 * in Monaco becomes Neovim notation for `nvim_input`. Printable characters go
 * as they are (`<` as `<lt>`); special keys and chords as `<Esc>`, `<C-x>`,
 * `<M-x>`, `<S-Tab>`; a lone modifier is nothing.
 */
const SPECIAL: Record<string, string> = {
  Escape: 'Esc',
  Enter: 'CR',
  Backspace: 'BS',
  Tab: 'Tab',
  Delete: 'Del',
  Insert: 'Insert',
  ArrowUp: 'Up',
  ArrowDown: 'Down',
  ArrowLeft: 'Left',
  ArrowRight: 'Right',
  Home: 'Home',
  End: 'End',
  PageUp: 'PageUp',
  PageDown: 'PageDown',
  ' ': 'Space',
}

const MODIFIERS = new Set(['Shift', 'Control', 'Alt', 'Meta', 'CapsLock', 'NumLock', 'ScrollLock', 'Dead', 'Unidentified'])

export type KeyLike = Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'altKey' | 'metaKey' | 'shiftKey'>

/** keyToNvim is the Neovim notation of a key event, or null when it is nothing Neovim should see. */
export function keyToNvim(e: KeyLike): string | null {
  const key = e.key
  if (!key || MODIFIERS.has(key)) return null
  const ctrl = e.ctrlKey || e.metaKey
  const fn = /^F(\d{1,2})$/.exec(key)
  let name: string | null = null
  if (key in SPECIAL) name = SPECIAL[key]!
  else if (fn) name = key
  if (name) {
    const mods = `${ctrl ? 'C-' : ''}${e.altKey ? 'M-' : ''}${e.shiftKey && key !== ' ' ? 'S-' : ''}`
    return `<${mods}${name}>`
  }
  if (key.length !== 1) return null
  if (ctrl || e.altKey) {
    const mods = `${ctrl ? 'C-' : ''}${e.altKey ? 'M-' : ''}`
    return `<${mods}${key.toLowerCase()}>`
  }
  return key === '<' ? '<lt>' : key
}

/** Keys the editor area keeps for itself in the Neovim keymap: the tab cycle, the fold. */
export function keptByConductor(e: KeyLike): boolean {
  return (e.ctrlKey || e.metaKey) && !e.altKey && e.key === 'Tab'
}

/** The cursor style Monaco shows for a Neovim mode (the `mode()` letter or a UI mode name). */
export function cursorStyleFor(mode: string): 'block' | 'line' | 'underline' {
  const m = mode.toLowerCase()
  if (m.startsWith('i') || m === 'insert' || m.startsWith('c') || m.startsWith('cmdline')) return 'line'
  if (m.startsWith('r') || m === 'replace') return 'underline'
  return 'block'
}

/** The words of the status line for a mode: "-- INSERT --", "-- VISUAL LINE --", nothing in normal mode. */
export function modeWords(mode: string): string {
  switch (mode) {
    case 'i':
    case 'insert':
      return '-- INSERT --'
    case 'v':
    case 'visual':
      return '-- VISUAL --'
    case 'V':
      return '-- VISUAL LINE --'
    case '\x16':
      return '-- VISUAL BLOCK --'
    case 'R':
    case 'replace':
      return '-- REPLACE --'
    case 's':
      return '-- SELECT --'
    case 't':
      return '-- TERMINAL --'
    default:
      return ''
  }
}

/** Whether a mode is one of the visual modes, whose other end comes with the cursor. */
export function isVisual(mode: string): boolean {
  return mode === 'v' || mode === 'V' || mode === '\x16' || mode === 'visual' || mode === 's' || mode === 'S'
}
