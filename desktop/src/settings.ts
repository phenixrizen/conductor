import { mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs'
import { dirname, isAbsolute, join } from 'node:path'

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
}

export const SERVER_SETTINGS: ReadonlyArray<keyof DesktopSettings> = ['dataDir', 'allowedRoots', 'defaultCwd', 'yolo', 'reach', 'wslDistro', 'wslWindowsHome']

export function defaultSettings(home: string, userData: string): DesktopSettings {
  return { dataDir: join(userData, 'conductor'), allowedRoots: [home], defaultCwd: home, yolo: false, reach: 'auto', closeToTray: true, wslDistro: '', wslWindowsHome: false }
}

/** validate returns the problems with s, in words; none for good settings. */
export function validate(s: DesktopSettings): string[] {
  const out: string[] = []
  if (!s.dataDir || !isAbsolute(s.dataDir)) out.push('the data directory must be an absolute path')
  if (!Array.isArray(s.allowedRoots) || s.allowedRoots.length === 0) out.push('at least one allowed root is needed')
  for (const r of s.allowedRoots ?? []) if (!r || !isAbsolute(r)) out.push(`allowed root ${JSON.stringify(r)} must be an absolute path`)
  if (!s.defaultCwd || !isAbsolute(s.defaultCwd)) out.push('the default working directory must be an absolute path')
  else if (s.allowedRoots?.length && !s.allowedRoots.some((r) => s.defaultCwd === r || s.defaultCwd.startsWith(r.replace(/\/+$/, '') + '/'))) out.push('the default working directory must lie under an allowed root')
  if (!['auto', 'manual', 'off'].includes(s.reach)) out.push('reach must be auto, manual or off')
  return out
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
  return out
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
