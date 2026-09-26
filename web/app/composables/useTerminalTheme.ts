import type { ITheme } from '@xterm/xterm'
import { buildTerminalTheme } from '~/utils/terminalTheme'

function isDark(): boolean {
  return document.documentElement.classList.contains('dark')
}

/** Reads the workbench colours from the resolved CSS variables on <html>. */
function resolve(): ITheme {
  const dark = isDark()
  const style = getComputedStyle(document.documentElement)
  const get = (name: string) => style.getPropertyValue(name)
  return buildTerminalTheme(dark, {
    bg: get('--ui-bg'),
    text: get(dark ? '--ui-text' : '--ui-text-highlighted'),
    secondary: get('--ui-secondary'),
    primary: get('--ui-primary'),
  })
}

let observing = false

/**
 * One xterm theme for the whole app, derived from the UI palette so the
 * terminal canvas matches the page in both colour modes. Recomputed when
 * the colour-mode class on <html> changes.
 */
export function useTerminalTheme() {
  const theme = useState<ITheme>('terminalTheme', () => (import.meta.client ? resolve() : {}))
  if (import.meta.client && !observing) {
    observing = true
    const observer = new MutationObserver(() => {
      theme.value = resolve()
    })
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  }
  return theme
}
