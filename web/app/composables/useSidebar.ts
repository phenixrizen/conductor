import { SIDEBAR_SIZE, clampSidebarSize, readSidebarMode, readSidebarSize, writeSidebarMode, writeSidebarSize, type SidebarMode } from '~/utils/sidebar'

/**
 * The desktop sidebar's mode: full, or the icon rail. Kept in localStorage
 * alone (the layout turns Nuxt UI's own collapse cookie off and binds the
 * sidebar's collapsed state to this), per browser, so a wall left on a spare
 * monitor keeps its rail after a reload; an old "hidden" choice reads as the
 * rail. On narrow screens the sidebar is a slideover and the mode is ignored.
 *
 * The full sidebar's width (percent of the window) is kept beside it, read
 * once when the app starts; `resize` saves the width a drag left.
 */
export function useSidebar() {
  const mode = useState<SidebarMode>('sidebarMode', () => {
    if (!import.meta.client) return 'full'
    try {
      return readSidebarMode(localStorage)
    } catch {
      return 'full'
    }
  })
  const rail = computed(() => mode.value === 'rail')
  const size = useState<number>('sidebarSize', () => {
    if (!import.meta.client) return SIDEBAR_SIZE.default
    try {
      return readSidebarSize(localStorage)
    } catch {
      return SIDEBAR_SIZE.default
    }
  })

  function set(m: SidebarMode) {
    mode.value = m
    if (import.meta.client) {
      try {
        writeSidebarMode(localStorage, m)
      } catch {
        /* ignore */
      }
    }
  }

  function resize(width: number) {
    if (!Number.isFinite(width)) return
    size.value = clampSidebarSize(width)
    if (import.meta.client) {
      try {
        writeSidebarSize(localStorage, width)
      } catch {
        /* ignore */
      }
    }
  }

  return {
    mode,
    rail,
    collapse: () => set('rail'),
    expand: () => set('full'),
    toggle: () => set(rail.value ? 'full' : 'rail'),
    size: readonly(size),
    resize,
  }
}
