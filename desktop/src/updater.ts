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
 * is loaded lazily so the unit tests need no Electron.
 */
export async function startUpdater(channel: 'auto' | 'link' | 'none', logs: Logs, onAvailable: (version: string) => void): Promise<void> {
  if (channel !== 'auto') return
  try {
    const { autoUpdater } = await import('electron-updater')
    autoUpdater.autoDownload = true
    autoUpdater.autoInstallOnAppQuit = true
    autoUpdater.logger = { info: (m: unknown) => logs.line('main', `updater: ${String(m)}`), warn: (m: unknown) => logs.line('main', `updater: ${String(m)}`), error: (m: unknown) => logs.line('main', `updater: ${String(m)}`), debug: () => {} }
    autoUpdater.on('update-downloaded', (info: { version: string }) => onAvailable(info.version))
    const check = () => autoUpdater.checkForUpdates().catch((e: Error) => logs.line('main', `updater: ${e.message}`))
    await check()
    setInterval(check, EVERY).unref()
  } catch (e) {
    logs.line('main', `updater unavailable: ${(e as Error).message}`)
  }
}
