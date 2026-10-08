export interface ShortcutRow {
  /** Keys as UKbd values ('meta', 'shift', 'escape', 'arrowleft', single characters…) outside a terminal: when no terminal or text field has the focus. */
  keys: string[]
  label: string
  /** The same action while typing into an agent: the Alt twin the terminal hands back to the page; none when the action needs the list or the page focused. */
  terminal?: string[]
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
export const ALT_PASSTHROUGH_CODES = new Set(['KeyN', 'KeyS', 'KeyY', 'KeyR', 'KeyA', 'KeyE', 'KeyC', 'KeyB', 'KeyH', 'KeyF', 'KeyP', 'KeyJ', 'KeyK', 'KeyT', 'ArrowLeft', 'ArrowRight', 'Escape'])

/** Shortcuts that work on every page. Registered in the default layout. */
export const GLOBAL_SHORTCUTS: ShortcutGroup = {
  title: 'Everywhere',
  rows: [
    { keys: ['meta', 'B'], label: 'Collapse the sidebar to the rail, or expand it', terminal: ['alt', 'B'] },
    { keys: ['?'], label: 'Keyboard shortcuts', terminal: ['alt', 'H'] },
    { keys: ['N'], label: 'Launch an agent', terminal: ['alt', 'N'] },
    { keys: ['G', 'Y'], label: 'Go to the Yard', terminal: ['alt', 'Y'] },
    { keys: ['G', 'R'], label: 'Go to the Roundhouse', terminal: ['alt', 'R'] },
    { keys: ['G', 'C'], label: 'Go to Crews', terminal: ['alt', 'C'] },
    { keys: ['G', 'A'], label: 'Go to Agents', terminal: ['alt', 'A'] },
    { keys: ['G', 'E'], label: 'Go to Events', terminal: ['alt', 'E'] },
    { keys: ['F'], label: 'Toggle fullscreen', terminal: ['alt', 'F'] },
  ],
}

/** The sidebar's keys (design 3c): in the filter, then on the focused row. Registered by the default layout, after Everywhere. */
export const SIDEBAR_SHORTCUTS: ShortcutGroup = {
  title: 'The sidebar',
  rows: [
    { keys: ['/'], label: 'Filter sessions, runs, people', terminal: ['alt', 'S'] },
    { keys: ['arrowdown'], label: 'From the filter, into the list' },
    { keys: ['J'], label: 'Next row (or ↓)' },
    { keys: ['K'], label: 'Previous row (or ↑)' },
    { keys: ['enter'], label: 'Open the session, or the run from its header' },
    { keys: ['1'], label: '1–9 answer the focused row\'s prompt, as on its page' },
    { keys: ['R'], label: 'Open the run of the focused member' },
    { keys: ['S'], label: 'Share the focused session or run' },
    { keys: ['X'], label: 'Stop it: the row asks; Enter stops, Escape cancels' },
    { keys: ['escape'], label: 'Leave the list' },
  ],
}

/** The editor area's keys (design 4b, 4c), on a session page with a file open. */
export const EDITOR_SHORTCUTS: ShortcutGroup = {
  title: 'The editor',
  rows: [
    { keys: ['T'], label: 'Terminal only: fold the editor to its tab strip, or bring it back', terminal: ['alt', 'T'] },
    { keys: ['ctrl', 'tab'], label: 'Next file (Ctrl+Shift+Tab the one before)' },
    { keys: ['ctrl', 'W'], label: 'Close the file' },
    { keys: ['ctrl', 'F'], label: 'Find in the file (Ctrl+H replace)' },
    { keys: ['ctrl', 'G'], label: 'Go to a line' },
  ],
}

export const WALL_SHORTCUTS: ShortcutGroup = {
  title: 'Yard',
  rows: [
    { keys: ['escape'], label: 'Back to the grid', terminal: ['alt', 'escape'] },
    { keys: ['J'], label: 'Next in the queue', terminal: ['alt', 'J'] },
    { keys: ['K'], label: 'Previous in the queue', terminal: ['alt', 'K'] },
    { keys: ['enter'], label: 'Reply to the selected queue item' },
  ],
}

export const CAROUSEL_SHORTCUTS: ShortcutGroup = {
  title: 'Roundhouse',
  rows: [
    { keys: ['arrowleft'], label: 'Previous session', terminal: ['alt', 'arrowleft'] },
    { keys: ['arrowright'], label: 'Next session', terminal: ['alt', 'arrowright'] },
    { keys: ['enter'], label: 'Type into the current session' },
    { keys: ['space'], label: 'Pause or resume rotation', terminal: ['alt', 'P'] },
    { keys: ['escape'], label: 'Leave the terminal', terminal: ['alt', 'escape'] },
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

  const groups = computed<ShortcutGroup[]>(() => (pageGroup.value ? [pageGroup.value, GLOBAL_SHORTCUTS, SIDEBAR_SHORTCUTS] : [GLOBAL_SHORTCUTS, SIDEBAR_SHORTCUTS]))

  return { open, groups, registerPage, show: () => (open.value = true) }
}
