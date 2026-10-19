import type { Logs } from './logs'

/**
 * updateChannel says whether the app can update itself: the dmg/zip and
 * nsis installs do (electron-updater against the GitHub releases), while a
 * deb, rpm or AppImage from a package manager is updated by it, so the app
 * only points at the release page.
 */
export function updateChannel(platform: string, appImage: boolean, packaged: boolean): 'auto' | 'link' | 'none' {
  if (!packaged) return 'none'
  if (platform === 'darwin' || platform === 'win32') return 'auto'
  return appImage ? 'auto' : 'link'
}

export const RELEASES_URL = 'https://github.com/phenixrizen/conductor/releases/latest'
const EVERY = 6 * 60 * 60 * 1000

/**
 * startUpdater checks for a release on start and every six hours where the
 * app updates itself, downloads it in the background and installs it on
 * quit; it logs what it does and never interrupts a session. electron-updater
 * is loaded lazily so the unit tests need no Electron. onAvailable runs at
 * every check that finds the update downloaded, the same version again
 * included (electron-updater emits update-downloaded for a cached download
 * too): UpdateNotice decides what is said. It returns what installs the
 * downloaded update now (quitting the app, which stops the server), null
 * where the app does not update itself.
 */
export async function startUpdater(channel: 'auto' | 'link' | 'none', logs: Logs, onAvailable: (version: string) => void): Promise<(() => void) | null> {
  if (channel !== 'auto') return null
  try {
    const { autoUpdater } = await import('electron-updater')
    autoUpdater.autoDownload = true
    autoUpdater.autoInstallOnAppQuit = true
    autoUpdater.logger = { info: (m: unknown) => logs.line('main', `updater: ${String(m)}`), warn: (m: unknown) => logs.line('main', `updater: ${String(m)}`), error: (m: unknown) => logs.line('main', `updater: ${String(m)}`), debug: () => {} }
    autoUpdater.on('update-downloaded', (info: { version: string }) => onAvailable(info.version))
    const check = () => autoUpdater.checkForUpdates().catch((e: Error) => logs.line('main', `updater: ${e.message}`))
    await check()
    setInterval(check, EVERY).unref()
    return () => autoUpdater.quitAndInstall()
  } catch (e) {
    logs.line('main', `updater unavailable: ${(e as Error).message}`)
    return null
  }
}

/** What the workbench is told of the update downloaded as it asks: its version ('' for none), and whether to say so. */
export interface UpdateTold {
  ready: string
  announce: boolean
}

/**
 * UpdateNotice decides what the workbench is told of the updates downloaded (#290). electron-updater says an update is
 * downloaded at every check that finds it (on start and every six hours) while the app runs on, in the tray; each was a dialog
 * of the app's own, left open on top of the last. Now the workbench asks (as it loads, and when the app says a check found one),
 * and an update is said once per version: to the first page that asks after it is downloaded, across starts, so that one found
 * while no window was open, or before the page was ready, is said when one is. announced() is the version said last (the
 * settings' updateAnnounced), remember() keeps the one said now.
 */
export class UpdateNotice {
  private version = ''
  private owed = false

  constructor(
    private readonly announced: () => string,
    private readonly remember: (version: string) => void,
  ) {}

  /** downloaded records the update a check found downloaded: owed to the workbench unless its version was said already. */
  downloaded(version: string): void {
    this.version = version
    this.owed = this.announced() !== version
  }

  /** take is what the page asking is told: the update ready, and whether to say so, which only the first to ask for it is told. */
  take(): UpdateTold {
    const announce = this.owed
    if (announce) {
      this.owed = false
      this.remember(this.version)
    }
    return { ready: this.version, announce }
  }
}

/**
 * installFailed says whether the update announced (settings' updateAnnounced) is newer than the version running: the app was
 * quit, or ended, since, and the update did not install (macOS, for one, updates only a signed app). '' or unreadable: no.
 */
export function installFailed(announced: string, running: string): boolean {
  return announced !== '' && compareVersions(announced, running) > 0
}

/**
 * compareVersions orders two release versions as semver does ("0.7.0-rc.12" before "0.7.0", "rc.12" after "rc.9"): negative
 * when a is older, 0 when they are the same or either does not read as one, positive when a is newer.
 */
export function compareVersions(a: string, b: string): number {
  const parse = (v: string) => /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/.exec(v.trim())
  const x = parse(a)
  const y = parse(b)
  if (!x || !y) return 0
  for (let i = 1; i <= 3; i++) {
    const d = Number(x[i]) - Number(y[i])
    if (d !== 0) return Math.sign(d)
  }
  const px = x[4]?.split('.') ?? []
  const py = y[4]?.split('.') ?? []
  // A release is newer than its prereleases.
  if (!px.length || !py.length) return Math.sign(py.length - px.length)
  for (let i = 0; i < Math.max(px.length, py.length); i++) {
    if (i >= px.length) return -1
    if (i >= py.length) return 1
    const nx = /^\d+$/.test(px[i]!)
    const ny = /^\d+$/.test(py[i]!)
    if (nx && ny) {
      const d = Number(px[i]) - Number(py[i])
      if (d !== 0) return Math.sign(d)
    } else if (nx !== ny) {
      return nx ? -1 : 1
    } else if (px[i] !== py[i]) {
      return px[i]! < py[i]! ? -1 : 1
    }
  }
  return 0
}
