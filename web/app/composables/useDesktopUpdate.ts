/**
 * The update the desktop app downloaded (#290): its version ('' while none), the same for every page under the layout; whether
 * the restart that installs it is being asked (the header's button and the toast open it); and what installs it (the app quits,
 * its server and the sessions on it with it). Nothing outside the desktop app, and from an app that predates it.
 */
export function useDesktopUpdate() {
  const ready = useState<string>('desktopUpdateReady', () => '')
  const restartOpen = useState<boolean>('desktopUpdateRestart', () => false)
  const desktop = useDesktop()
  return {
    ready,
    restartOpen,
    install: () => desktop.bridge.value?.installUpdate?.() ?? Promise.resolve(),
  }
}
