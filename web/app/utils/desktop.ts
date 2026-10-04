/** The desktop app's settings as its shell keeps them (desktop/src/settings.ts). */
export interface DesktopSettings {
  dataDir: string
  allowedRoots: string[]
  defaultCwd: string
  yolo: boolean
  reach: 'auto' | 'manual' | 'off'
  closeToTray: boolean
  wslDistro: string
  wslWindowsHome: boolean
  switchyardServer: string
  switchyardToken: string
  switchyardName: string
}

export interface DesktopServerState {
  state: 'stopped' | 'starting' | 'running' | 'failed'
  url: string
  pid?: number
  version?: string
  failures: number
  lastError?: string
}

/** The bridge the desktop shell's preload exposes as window.conductorDesktop (desktop/src/preload.ts). */
export interface DesktopIceStatus {
  forwarding: boolean
  port: number
  publicIp: string
  wslAddress: string
  firewall: 'present' | 'missing' | 'unknown'
  reason?: string
}

export interface DesktopBridge {
  version: string
  platform: string
  token(): Promise<string>
  openExternal(url: string): Promise<void>
  settings: { get(): Promise<DesktopSettings>; set(patch: Partial<DesktopSettings>): Promise<DesktopSettings>; pickDirectory(): Promise<string | null> }
  restartServer(): Promise<void>
  openInBrowser(): Promise<void>
  showLog(): Promise<void>
  serverState(): Promise<DesktopServerState>
  /** Windows: what the app forwards for WebRTC from WSL. */
  ice(): Promise<DesktopIceStatus>
  /** Windows: adds the firewall rule for the ICE port (one elevation prompt); the rule's state after. */
  allowIceFirewall(): Promise<DesktopIceStatus['firewall']>
  versions(): Promise<{ app: string; electron: string; node: string; chrome: string; server: string }>
  onServerState(cb: (state: DesktopServerState) => void): () => void
}

/** desktopBridge is the bridge when the workbench runs in the desktop app, else null. */
export function desktopBridge(): DesktopBridge | null {
  if (typeof window === 'undefined') return null
  const b = (window as unknown as { conductorDesktop?: DesktopBridge }).conductorDesktop
  return b && typeof b.token === 'function' ? b : null
}

/** tokenFromFragment reads a workbench token the desktop app put in the URL fragment ("Open in browser"): `#token=…`, '' when none. */
export function tokenFromFragment(hash: string): string {
  const h = hash.startsWith('#') ? hash.slice(1) : hash
  if (!h) return ''
  const params = new URLSearchParams(h)
  const t = params.get('token') ?? ''
  return /^[A-Za-z0-9_-]{16,256}$/.test(t) ? t : ''
}

/** withoutTokenFragment is the URL with the token taken out of its fragment (the rest of the fragment kept). */
export function withoutTokenFragment(href: string): string {
  const u = new URL(href)
  const h = u.hash.startsWith('#') ? u.hash.slice(1) : u.hash
  const params = new URLSearchParams(h)
  params.delete('token')
  const rest = params.toString()
  u.hash = rest ? '#' + rest : ''
  return u.toString()
}
