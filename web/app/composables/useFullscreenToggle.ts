/**
 * Document fullscreen, shared by every page: one state that follows the
 * browser's fullscreenchange event, bound by the layout (`listen`), toggled
 * by any FullscreenButton or the F shortcut (`shortcuts`, registered by the
 * layout, so no page registers it twice). Plain F never fires while a text
 * field has focus, like every plain key; Alt+F works inside a terminal.
 */
export function useFullscreenToggle() {
  const fullscreen = useState<boolean>('fullscreen', () => false)

  function toggle() {
    if (!import.meta.client) return
    if (document.fullscreenElement) document.exitFullscreen()
    else document.documentElement.requestFullscreen?.()
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
      f: toggle,
      alt_f: { usingInput: true, handler: toggle },
    })
  }

  return { fullscreen, toggle, listen, shortcuts }
}
