/** Document fullscreen state with a toggle, for the wall and the carousel. */
export function useFullscreenToggle() {
  const fullscreen = ref(false)

  function onChange() {
    fullscreen.value = !!document.fullscreenElement
  }

  function toggle() {
    if (document.fullscreenElement) document.exitFullscreen()
    else document.documentElement.requestFullscreen?.()
  }

  onMounted(() => {
    onChange()
    document.addEventListener('fullscreenchange', onChange)
  })
  onBeforeUnmount(() => document.removeEventListener('fullscreenchange', onChange))

  return { fullscreen, toggle }
}
