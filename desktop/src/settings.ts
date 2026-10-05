import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute, join } from 'node:path'
import { clampZoom } from './zoom'

/** The desktop app's settings (userData/settings.json): what it starts the server with. */
export interface DesktopSettings {
  /** The server's data directory (catalog, crews, hooks, certificates). */
  dataDir: string
  /** Directories server sessions may run in. */
  allowedRoots: string[]
  /** The working directory a launch gets without one. */
  defaultCwd: string
  /** Launch every agent with its yolo recipe. */
  yolo: boolean
  /** The server's reach mode: auto maps the TLS port on the router once there is one. */
  reach: 'auto' | 'manual' | 'off'
  /** Closing the window keeps the app in the tray; the server goes on. */
  closeToTray: boolean
  /** Windows: the WSL distribution the server runs in; '' for the default. */
  wslDistro: string
  /** Windows: also allow /mnt/c/Users/<user> (slow, but where Windows projects live). */
  wslWindowsHome: boolean
  /** Publish every session to a switchyard, so a share link works from anywhere. On by default. */
  switchyardEnabled: boolean
  /** The switchyard every session is published to (`https://host`); '' for the public one, switchyard.rslabs.net. */
  switchyardServer: string
  /** Optional: one of the switchyard's host tokens (a private switchyard, or a trusted seat on the public one). */
  switchyardToken: string
  /** How this machine is named there; '' for its host name. */
  switchyardName: string
  /** The one-time notices already shown (or not owed: a fresh install owes none), by name. */
  noticed: string[]
  /** The workbench's zoom level (0 is 100%), kept by the app as Ctrl+= and Ctrl+- move it; never set from the Settings page. */
  zoomLevel: number
}

/** The notices the app owes once: publishing, the round that turned sharing through the public switchyard on by default. */
export const NOTICES = ['publishing'] as const
export type Notice = (typeof NOTICES)[number]

export const SERVER_SETTINGS: ReadonlyArray<keyof DesktopSettings> = ['dataDir', 'allowedRoots', 'defaultCwd', 'yolo', 'reach', 'wslDistro', 'wslWindowsHome', 'switchyardEnabled', 'switchyardServer', 'switchyardToken', 'switchyardName']

export function defaultSettings(home: string, userData: string): DesktopSettings {
  return { dataDir: join(userData, 'conductor'), allowedRoots: [home], defaultCwd: home, yolo: false, reach: 'auto', closeToTray: true, wslDistro: '', wslWindowsHome: false, switchyardEnabled: true, switchyardServer: '', switchyardToken: '', switchyardName: '', noticed: [...NOTICES], zoomLevel: 0 }
}

/**
 * validate returns the problems with s, in words; none for good settings. With posix the paths must be the WSL distribution's
 * (absolute Linux paths), whatever the host: Windows, where the server runs inside WSL.
 */
export function validate(s: DesktopSettings, posix = false): string[] {
  const out: string[] = []
  const absolute = (p: string) => (posix ? p.startsWith('/') : isAbsolute(p))
  const kind = posix ? 'an absolute path inside the WSL distribution, such as /home/<user>/code' : 'an absolute path'
  if (!s.dataDir || !absolute(s.dataDir)) out.push(`the data directory must be ${kind}`)
  if (!Array.isArray(s.allowedRoots) || s.allowedRoots.length === 0) out.push('at least one allowed root is needed')
  for (const r of s.allowedRoots ?? []) if (!r || !absolute(r)) out.push(`allowed root ${JSON.stringify(r)} must be ${kind}`)
  if (!s.defaultCwd || !absolute(s.defaultCwd)) out.push(`the default working directory must be ${kind}`)
  else if (s.allowedRoots?.length && !s.allowedRoots.some((r) => s.defaultCwd === r || s.defaultCwd.startsWith(r.replace(/\/+$/, '') + '/'))) out.push('the default working directory must lie under an allowed root')
  if (!['auto', 'manual', 'off'].includes(s.reach)) out.push('reach must be auto, manual or off')
  if (s.switchyardServer) {
    let ok = false
    try {
      const u = new URL(s.switchyardServer)
      ok = (u.protocol === 'https:' || u.protocol === 'http:') && !!u.host && !u.search && !u.hash && !u.username
    } catch {
      ok = false
    }
    if (!ok) out.push('the switchyard must be an http(s) URL with a host and nothing after it')
  }
  if ((s.switchyardName ?? '').length > 64) out.push('the name at the switchyard is at most 64 characters')
  return out
}

/** wslDefaults are the defaults for a server inside a WSL distribution whose home is linuxHome. */
export function wslDefaults(s: DesktopSettings, linuxHome: string): DesktopSettings {
  return { ...s, dataDir: `${linuxHome}/.local/share/conductor/data`, allowedRoots: [linuxHome], defaultCwd: linuxHome }
}

/**
 * migrateToWsl moves settings saved with Windows paths (an earlier build kept them, and the launcher threw them away) to the
 * distribution's: a path that is not an absolute Linux path becomes the default inside the distribution. changed says so.
 */
export function migrateToWsl(s: DesktopSettings, linuxHome: string): { settings: DesktopSettings; changed: boolean } {
  const d = wslDefaults(s, linuxHome)
  const linux = (p: string) => typeof p === 'string' && p.startsWith('/')
  const roots = (s.allowedRoots ?? []).filter(linux)
  const next: DesktopSettings = {
    ...s,
    dataDir: linux(s.dataDir) ? s.dataDir : d.dataDir,
    allowedRoots: roots.length ? roots : d.allowedRoots,
    defaultCwd: linux(s.defaultCwd) ? s.defaultCwd : d.defaultCwd,
  }
  if (!next.allowedRoots.some((r) => next.defaultCwd === r || next.defaultCwd.startsWith(r.replace(/\/+$/, '') + '/'))) next.defaultCwd = next.allowedRoots[0]!
  return { settings: next, changed: JSON.stringify(next) !== JSON.stringify(s) }
}

/** loadSettings reads the file, filling what it lacks from the defaults; a missing or broken file gives the defaults. */
export function loadSettings(file: string, defaults: DesktopSettings): DesktopSettings {
  try {
    const raw = JSON.parse(readFileSync(file, 'utf8')) as Partial<DesktopSettings>
    const merged: DesktopSettings = { ...defaults, ...pick(raw) }
    return validate(merged).length ? defaults : merged
  } catch {
    return defaults
  }
}

function pick(raw: Partial<DesktopSettings>): Partial<DesktopSettings> {
  const out: Partial<DesktopSettings> = {}
  if (typeof raw.dataDir === 'string') out.dataDir = raw.dataDir
  if (Array.isArray(raw.allowedRoots)) out.allowedRoots = raw.allowedRoots.filter((r): r is string => typeof r === 'string')
  if (typeof raw.defaultCwd === 'string') out.defaultCwd = raw.defaultCwd
  if (typeof raw.yolo === 'boolean') out.yolo = raw.yolo
  if (raw.reach === 'auto' || raw.reach === 'manual' || raw.reach === 'off') out.reach = raw.reach
  if (typeof raw.closeToTray === 'boolean') out.closeToTray = raw.closeToTray
  if (typeof raw.wslDistro === 'string') out.wslDistro = raw.wslDistro
  if (typeof raw.wslWindowsHome === 'boolean') out.wslWindowsHome = raw.wslWindowsHome
  if (typeof raw.switchyardEnabled === 'boolean') out.switchyardEnabled = raw.switchyardEnabled
  if (typeof raw.switchyardServer === 'string') out.switchyardServer = raw.switchyardServer
  if (typeof raw.switchyardToken === 'string') out.switchyardToken = raw.switchyardToken
  if (typeof raw.switchyardName === 'string') out.switchyardName = raw.switchyardName
  // A file from before the notices existed has no noticed list: every notice is owed to it.
  out.noticed = Array.isArray(raw.noticed) ? raw.noticed.filter((n): n is string => typeof n === 'string') : []
  out.zoomLevel = clampZoom(raw.zoomLevel)
  return out
}

/**
 * owedNotice is the notice to show once, and the settings with it marked shown: publishing, to settings that came from a file
 * saved before it existed while publishing is on (turned off, there is nothing to tell). A fresh install starts with every notice
 * marked (defaultSettings), so it owes none.
 */
export function owedNotice(s: DesktopSettings): { notice: Notice | ''; settings: DesktopSettings } {
  if (s.noticed.includes('publishing')) return { notice: '', settings: s }
  const settings = { ...s, noticed: [...s.noticed, 'publishing'] }
  return { notice: s.switchyardEnabled ? 'publishing' : '', settings }
}

/** saveSettings writes the file through a temporary one, mode 0600. */
export function saveSettings(file: string, s: DesktopSettings): void {
  mkdirSync(dirname(file), { recursive: true })
  const tmp = file + '.tmp'
  writeFileSync(tmp, JSON.stringify(s, null, 2) + '\n', { mode: 0o600 })
  renameSync(tmp, file)
}

/** serverAffecting says whether changing from a to b needs the server restarted. */
export function serverAffecting(a: DesktopSettings, b: DesktopSettings): boolean {
  return SERVER_SETTINGS.some((k) => JSON.stringify(a[k]) !== JSON.stringify(b[k]))
}
