/**
 * The update the desktop app downloaded (#290): its version ('' while none), the same for every page under the layout; the
 * version the restart that installs it is asked for ('' while not: the header's button and the toast ask it); and what installs
 * a version (the app quits, its server and the sessions on it with it), false when the app no longer holds it. Nothing outside
 * the desktop app, and from an app that predates it.
 */
export function useDesktopUpdate() {
  const ready = useState<string>('desktopUpdateReady', () => '')
  const restartFor = useState<string>('desktopUpdateRestart', () => '')
  const desktop = useDesktop()
  return {
    ready,
    restartFor,
    install: (version: string) => desktop.bridge.value?.installUpdate?.(version) ?? Promise.resolve(false),
  }
}
