const KEY = 'conductor.sidebar.hidden'

function readHidden(): boolean {
  try {
    return localStorage.getItem(KEY) === '1'
  } catch {
    return false
  }
}

/**
 * Whether the desktop sidebar is hidden. Persisted per browser so a wall
 * or carousel left on a spare monitor stays edge to edge after a reload.
 * On narrow screens the sidebar is a slideover and this state is ignored.
 */
export function useSidebar() {
  const hidden = useState<boolean>('sidebarHidden', () => (import.meta.client ? readHidden() : false))

  function set(value: boolean) {
    hidden.value = value
    try {
      localStorage.setItem(KEY, value ? '1' : '0')
    } catch {
      /* ignore */
    }
  }

  return {
    hidden,
    hide: () => set(true),
    show: () => set(false),
    toggle: () => set(!hidden.value),
  }
}
