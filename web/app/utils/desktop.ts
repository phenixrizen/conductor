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
  switchyardEnabled: boolean
  switchyardServer: string
  switchyardToken: string
  switchyardName: string
  /** The one-time notices already shown. */
  noticed?: string[]
}

/** The words for a switchyard that is plain http on another machine: the desktop app's validate (desktop/src/settings.ts PLAIN_SWITCHYARD) says the same. */
export const PLAIN_SWITCHYARD = 'the switchyard must be https:// unless it runs on this machine (localhost): over plain http:// what is published there travels in the clear and unverified'

/**
 * switchyardProblem is what the Settings page says before it saves a switchyard that is plain http on another machine, which the
 * app and its server refuse (PLAIN_SWITCHYARD); '' for one it may save, and while publishing is off. This machine is localhost, in lower case, by
 * that name, an address in 127.0.0.0/8 written as four decimal numbers, or ::1 (or 127 mapped into IPv6), as the server reads it
 * (config.PlainOffMachine); a short form of an address the browser reads as 127.0.0.1 is not. The app checks the rest of the URL.
 */
export function switchyardProblem(s: Pick<DesktopSettings, 'switchyardEnabled' | 'switchyardServer'>): string {
  if (!s.switchyardEnabled || !s.switchyardServer) return ''
  let u: URL
  try {
    u = new URL(s.switchyardServer)
  } catch {
    return ''
  }
  return u.protocol === 'http:' && !thisMachine(s.switchyardServer, u) ? PLAIN_SWITCHYARD : ''
}

function thisMachine(raw: string, u: URL): boolean {
  const host = u.hostname.toLowerCase()
  if (host.startsWith('[')) return host === '[::1]' || /^\[::ffff:7f[0-9a-f]{2}:[0-9a-f]{1,4}\]$/.test(host)
  // The host as written: the URL parser reads 127.1 or 0x7f.0.0.1 as 127.0.0.1, and the server does not; nor does it read a
  // backslash as the parser does.
  if (raw.includes('\\')) return false
  const authority = raw.trim().replace(/^[a-z][a-z0-9+.-]*:\/\//i, '').split(/[/?#]/, 1)[0] ?? ''
  const typed = authority.slice(authority.lastIndexOf('@') + 1).split(':', 1)[0]!
  if (typed.toLowerCase() !== host) return false
  // localhost written so, in lower case, as the server reads it: the one spelling of it that a proxy the environment names never takes.
  if (typed === 'localhost') return true
  const quad = /^127\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(typed)
  return !!quad && quad.slice(1).every((p) => (p === '0' || !p.startsWith('0')) && Number(p) <= 255)
}

/** The words of a one-time notice the desktop app hands the workbench; null for one this build does not know. */
export function noticeWords(notice: string): { title: string; description: string } | null {
  if (notice === 'publishing') {
    return {
      title: 'Sessions are published to a switchyard',
      description:
        'Each session you start is listed at switchyard.rslabs.net, or the switchyard Settings names, so Share makes a link that works from anywhere. The switchyard mints and checks those links: publish only to one whose operator you trust. Turn it off under Settings → Switchyard.',
    }
  }
  return null
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
  settings: {
    get(): Promise<DesktopSettings>
    set(patch: Partial<DesktopSettings>): Promise<DesktopSettings>
    /**
     * What became of the last save ('saved', or 'restarted' when the app restarted the server for it), handed out once; '' when
     * none is owed. A restarted server listens on a fresh port and the app loads the page there: the page that follows asks. Older
     * apps lack it.
     */
    outcome?(): Promise<string>
  }
  restartServer(): Promise<void>
  openInBrowser(): Promise<void>
  showLog(): Promise<void>
  serverState(): Promise<DesktopServerState>
  /** The one-time notice the app owes ('publishing'), handed out once; '' when none. Older apps lack it. */
  notice?(): Promise<string>
  /** Windows: what the app forwards for WebRTC from WSL. */
  ice(): Promise<DesktopIceStatus>
  /** Windows: adds the firewall rule for the ICE port (one elevation prompt); the rule's state after. */
  allowIceFirewall(): Promise<DesktopIceStatus['firewall']>
  versions(): Promise<{ app: string; electron: string; node: string; chrome: string; server: string }>
  onServerState(cb: (state: DesktopServerState) => void): () => void
  /** An invite the app was handed while this page is open (desktop/src/invite.ts InviteDelivery); cb says whether the page routed it. Older apps lack it. */
  onInvite?(cb: (invite: { server: string; token: string }) => boolean | Promise<boolean>): () => void
  /**
   * The update the app downloaded ('' for none) and whether to say so, which the first page to ask after the download is told,
   * once per version; the version the tray's Restart to update asked for, which the page asks in its dialog (once, '' for none);
   * and one whose install did not take ('' for none), said once (#290). Older apps lack it.
   */
  update?(): Promise<{ ready: string; announce: boolean; restart?: string; failed: string }>
  /** A check found an update downloaded, the one downloaded is gone, or the tray asked for the restart: ask update() what to say. Older apps lack it. */
  onUpdate?(cb: () => void): () => void
  /**
   * Installs the update downloaded now, the version offered: the app quits, its server and the sessions running on it with it
   * ('started'). 'gone' when that version is no longer ready (a check found a newer one, or a download failed), 'busy' while a
   * check for updates is under way; nothing stops then. Older apps lack it.
   */
  installUpdate?(version: string): Promise<'started' | 'gone' | 'busy'>
}

/** What the workbench says of an update the desktop app downloaded, once per version (#290). */
export function updateReadyWords(version: string): { title: string; description: string } {
  return { title: `Conductor ${version} is ready`, description: 'It installs when you quit Conductor. Restart to update now, or later.' }
}

/**
 * What the workbench says of an update offered that the app's updater no longer holds: a check found a newer one, whose download
 * replaced it, or a download failed. Nothing restarted.
 */
export function updateGoneWords(version: string): { title: string; description: string } {
  return {
    title: `Conductor ${version} is no longer ready`,
    description: 'A newer update replaced it, or its download failed, and nothing restarted. The button in the header shows when an update is ready.',
  }
}

/** What the workbench says of a restart to update put off while the app checks for updates, which may change the one held. */
export function updateBusyWords(): { title: string; description: string } {
  return { title: 'Conductor is checking for updates', description: 'Nothing restarted. Restart to update again in a moment.' }
}

/** What the workbench says of an update the app downloaded and this start of it runs older than: its install did not take. */
export function updateFailedWords(version: string): { title: string; description: string } {
  return {
    title: `Conductor ${version} did not install`,
    description: "It was downloaded and Conductor started again as the version before. The app's log says why; the update is offered again.",
  }
}

/**
 * What restarting to update ends: the app quits to install version, and the server with it, ending the sessions running on it;
 * sessions is how many, or negative when they could not be counted, which never reads as none.
 */
export function restartWords(version: string, sessions: number): string {
  const ends =
    sessions < 0
      ? 'The sessions running on this computer, if any, end: they could not be counted.'
      : sessions === 0
        ? 'No session runs on this computer, so none ends.'
        : sessions === 1
          ? 'The session running on this computer ends.'
          : `The ${sessions} sessions running on this computer end.`
  return `Conductor quits to install ${version}, and its server stops. ${ends}`
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
