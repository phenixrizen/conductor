/**
 * Document fullscreen, shared by every page: one state that follows the
 * browser's fullscreenchange event, bound by the layout (`listen`), toggled
 * by any FullscreenButton or the F shortcut (`shortcuts`, registered by the
 * layout, so no page registers it twice). Plain F never fires while a text
 * field has focus, like every plain key, nor while a select or its list has
 * it; Alt+F works inside a terminal.
 */

/**
 * A select's trigger or its open list. There a letter is the list's typeahead,
 * and Nuxt UI pauses plain keys only in text fields and contenteditable.
 */
const PICKER = '[role="combobox"], [role="listbox"], [aria-haspopup="listbox"]'

export function useFullscreenToggle() {
  const fullscreen = useState<boolean>('fullscreen', () => false)

  function toggle() {
    if (!import.meta.client) return
    // A refusal (no user gesture, a denied permission, a document going away)
    // rejects the promise; there is nothing to do about it, so it is dropped.
    if (document.fullscreenElement) document.exitFullscreen?.()?.catch(() => {})
    else document.documentElement.requestFullscreen?.()?.catch(() => {})
  }

  function listen() {
    const onChange = () => (fullscreen.value = !!document.fullscreenElement)
    onMounted(() => {
      onChange()
      document.addEventListener('fullscreenchange', onChange)
    })
    onBeforeUnmount(() => document.removeEventListener('fullscreenchange', onChange))
  }

  function shortcuts() {
    defineShortcuts({
      f: () => {
        if (!document.activeElement?.closest(PICKER)) toggle()
      },
      alt_f: { usingInput: true, handler: toggle },
    })
  }

  return { fullscreen, toggle, listen, shortcuts }
}
