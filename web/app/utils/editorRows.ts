import type { PaletteItem } from '~/composables/usePalette'

/**
 * The editor's own rows in the command palette (#321): what `EditorArea`
 * exposes as one reactive snapshot (`EditorPaletteState`) read into four
 * rows of a page's group when a file is open: Keys → Neovim / Keys →
 * default, Save, Close file and Ask the agent about the selection, each
 * disabled with the editor's own words for why. Pure.
 */
export interface EditorPaletteState {
  /** The tab in front, or none: no rows without one. */
  activeId: string | null
  keymap: 'default' | 'nvim'
  /** Neovim can take the file here (offered on the page, the file not read only). */
  nvimOffered: boolean
  /** Why not, when it cannot. */
  nvimWhy: string
  /** Why the keymap cannot switch now (unsaved changes, keys still on their way), or empty. */
  switchBlocked: string
  /** Save would write now: the file is editable here and has changes. */
  saveable: boolean
  /** Why not, when it would not. */
  saveWhy: string
  /** The agent may be asked from this page. */
  canAsk: boolean
  hasSelection: boolean
}

export interface EditorPaletteActs {
  setKeymap: (k: 'default' | 'nvim') => void
  save: () => void
  close: (id: string) => void
  askAgent: () => void
}

export function editorRows(ed: EditorPaletteState | null | undefined, acts: EditorPaletteActs): PaletteItem[] {
  if (!ed?.activeId) return []
  const id = ed.activeId
  const next = ed.keymap === 'nvim' ? 'default' : 'nvim'
  const keysBlocked = ed.switchBlocked || (next === 'nvim' && !ed.nvimOffered ? ed.nvimWhy || 'Neovim is not offered here' : '')
  return [
    {
      key: 'page:keys',
      label: next === 'nvim' ? 'Keys → Neovim' : 'Keys → default',
      detail: keysBlocked || (next === 'nvim' ? "The real Neovim on the session's machine holds the file" : "Monaco's keys"),
      icon: 'i-lucide-keyboard',
      disabled: !!keysBlocked,
      run: () => acts.setKeymap(next),
    },
    { key: 'page:save', label: 'Save', detail: ed.saveWhy || 'Writes the file', icon: 'i-lucide-save', kbds: ['ctrl', 'S'], disabled: !ed.saveable, run: () => acts.save() },
    { key: 'page:close-file', label: 'Close file', detail: 'An unsaved file asks first', icon: 'i-lucide-x', kbds: ['ctrl', 'W'], run: () => acts.close(id) },
    {
      key: 'page:ask-selection',
      label: 'Ask the agent about the selection',
      detail: !ed.canAsk ? 'The agent cannot be asked from here' : !ed.hasSelection ? 'Select some lines first' : 'The composer opens on the selection',
      icon: 'i-lucide-message-circle-question',
      kbds: ['ctrl', 'shift', 'A'],
      disabled: !ed.canAsk || !ed.hasSelection,
      run: () => acts.askAgent(),
    },
  ]
}
