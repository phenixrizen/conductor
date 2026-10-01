export interface ShortcutRow {
  /** Keys as UKbd values: 'meta', 'shift', 'escape', 'arrowleft', single characters… */
  keys: string[]
  label: string
}

export interface ShortcutGroup {
  title: string
  rows: ShortcutRow[]
}

/**
 * Key codes that a focused terminal hands back to the page when Alt is held,
 * so the Alt variants below work while typing into an agent. Plain keys keep
 * going to the agent; xterm ignores only these Alt chords.
 */
export const ALT_PASSTHROUGH_CODES = new Set(['KeyN', 'KeyS', 'KeyW', 'KeyC', 'KeyA', 'KeyE', 'KeyR', 'KeyB', 'KeyH', 'KeyF', 'KeyP', 'KeyJ', 'KeyK', 'ArrowLeft', 'ArrowRight', 'Escape'])

/** Shortcuts that work on every page. Registered in the default layout. */
export const GLOBAL_SHORTCUTS: ShortcutGroup = {
  title: 'Everywhere',
  rows: [
    { keys: ['meta', 'B'], label: 'Collapse the sidebar to the rail, or expand it (Alt+B in a terminal)' },
    { keys: ['?'], label: 'Keyboard shortcuts (Alt+H in a terminal)' },
    { keys: ['N'], label: 'Launch an agent (Alt+N in a terminal)' },
    { keys: ['/'], label: 'Filter sessions (Alt+S in a terminal)' },
    { keys: ['G', 'W'], label: 'Go to the Wall (Alt+W in a terminal)' },
    { keys: ['G', 'C'], label: 'Go to the Carousel (Alt+C in a terminal)' },
    { keys: ['G', 'A'], label: 'Go to Agents (Alt+A in a terminal)' },
    { keys: ['G', 'E'], label: 'Go to Events (Alt+E in a terminal)' },
    { keys: ['G', 'R'], label: 'Go to Crews (Alt+R in a terminal)' },
    { keys: ['F'], label: 'Toggle fullscreen (Alt+F in a terminal)' },
  ],
}

export const WALL_SHORTCUTS: ShortcutGroup = {
  title: 'Wall',
  rows: [
    { keys: ['escape'], label: 'Back to the grid (Alt+Esc in a terminal)' },
    { keys: ['J'], label: 'Next in the queue (Alt+J in a terminal)' },
    { keys: ['K'], label: 'Previous in the queue (Alt+K in a terminal)' },
    { keys: ['enter'], label: 'Reply to the selected queue item' },
  ],
}

export const CAROUSEL_SHORTCUTS: ShortcutGroup = {
  title: 'Carousel',
  rows: [
    { keys: ['arrowleft'], label: 'Previous session (Alt+← in a terminal)' },
    { keys: ['arrowright'], label: 'Next session (Alt+→ in a terminal)' },
    { keys: ['enter'], label: 'Type into the current session' },
    { keys: ['space'], label: 'Pause or resume rotation (Alt+P in a terminal)' },
    { keys: ['escape'], label: 'Leave the terminal (Alt+Esc in a terminal)' },
  ],
}

/**
 * State for the shortcuts modal. Pages register the group that applies to
 * them so the modal always lists what works on the current screen.
 * Plain shortcuts never fire while a terminal or a form field has focus
 * (keys go to the agent); the Alt variants do, because the terminal hands
 * those chords back to the page.
 */
export function useShortcutsModal() {
  const open = useState<boolean>('shortcutsOpen', () => false)
  const pageGroup = useState<ShortcutGroup | null>('shortcutsPageGroup', () => null)

  function registerPage(group: ShortcutGroup | null) {
    pageGroup.value = group
    onBeforeUnmount(() => {
      if (pageGroup.value === group) pageGroup.value = null
    })
  }

  const groups = computed<ShortcutGroup[]>(() => (pageGroup.value ? [pageGroup.value, GLOBAL_SHORTCUTS] : [GLOBAL_SHORTCUTS]))

  return { open, groups, registerPage, show: () => (open.value = true) }
}
