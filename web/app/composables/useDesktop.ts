import { desktopBridge, type DesktopBridge, type DesktopServerState } from '~/utils/desktop'

/** The desktop shell, when the workbench runs in it: its bridge, and the server's state as the shell reports it. */
export function useDesktop() {
  const bridge = useState<DesktopBridge | null>('desktopBridge', () => (import.meta.client ? desktopBridge() : null))
  const serverState = useState<DesktopServerState | null>('desktopServerState', () => null)
  const isDesktop = computed(() => bridge.value !== null)
  if (import.meta.client && bridge.value && !serverState.value) {
    bridge.value.serverState().then((s) => (serverState.value = s)).catch(() => {})
    bridge.value.onServerState((s) => (serverState.value = s))
  }
  return { isDesktop, bridge, serverState }
}
