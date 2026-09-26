export interface ShortcutRow {
  /** Keys as UKbd values: 'meta', 'shift', 'escape', 'arrowleft', single characters… */
  keys: string[]
  label: string
}

export interface ShortcutGroup {
  title: string
  rows: ShortcutRow[]
}

/** Shortcuts that work on every page. Registered in the default layout. */
export const GLOBAL_SHORTCUTS: ShortcutGroup = {
  title: 'Everywhere',
  rows: [
    { keys: ['meta', 'B'], label: 'Show or hide the sidebar' },
    { keys: ['?'], label: 'Keyboard shortcuts' },
    { keys: ['G', 'S'], label: 'Go to Sessions' },
    { keys: ['G', 'W'], label: 'Go to the Wall' },
    { keys: ['G', 'C'], label: 'Go to the Carousel' },
    { keys: ['G', 'A'], label: 'Go to Agents' },
  ],
}

export const WALL_SHORTCUTS: ShortcutGroup = {
  title: 'Wall',
  rows: [
    { keys: ['escape'], label: 'Back to the grid' },
    { keys: ['F'], label: 'Toggle fullscreen' },
  ],
}

export const CAROUSEL_SHORTCUTS: ShortcutGroup = {
  title: 'Carousel',
  rows: [
    { keys: ['arrowleft'], label: 'Previous session' },
    { keys: ['arrowright'], label: 'Next session' },
    { keys: ['enter'], label: 'Type into the current session' },
    { keys: ['space'], label: 'Pause or resume rotation' },
    { keys: ['F'], label: 'Toggle fullscreen' },
  ],
}

/**
 * State for the shortcuts modal. Pages register the group that applies to
 * them so the modal always lists what works on the current screen.
 * Shortcuts never fire while a terminal or a form field has focus: keys go
 * to the terminal, and the buttons in the navbar do the same jobs.
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
