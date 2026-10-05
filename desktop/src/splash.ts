import { BrowserWindow, shell } from 'electron'
import { external } from './window'

/** The sponsor line, in the Sponsor Kit's words (the same as the workbench's Settings → The app). */
export const CREDIT = 'Sponsored and maintained by RockSolid Labs'
export const SPONSOR_URL = 'https://rocksolidlabs.io'

/** The copyright line, from the first release's year through `year` (the package's holder, electron-builder's copyright). */
export function copyrightLine(year: number = new Date().getFullYear()): string {
  const first = 2026
  return `© ${year > first ? `${first}–${year}` : first} the Conductor authors`
}

/** The build line under the name: "0.6.0-rc.4 · windows x64 · electron 44.5.1". */
export function buildLine(version: string, platform: NodeJS.Platform, arch: string, electron: string): string {
  const os = platform === 'win32' ? 'windows' : platform === 'darwin' ? 'macos' : platform
  return [version, `${os} ${arch}`, electron ? `electron ${electron}` : ''].filter(Boolean).join(' · ')
}

/** The splash page's query: what it shows that the main process knows. */
export function splashQuery(o: { version: string; build: string; year?: number }): Record<string, string> {
  return { version: o.version, build: o.build, copyright: copyrightLine(o.year), credit: CREDIT }
}

/** The words the splash says while the app starts, in order. */
export const SPLASH_STEPS = {
  wsl: 'Checking WSL…',
  prepare: 'Preparing the server…',
  start: 'Starting the server…',
  open: 'Opening the workbench…',
} as const

export interface Splash {
  /** status shows what the app is doing now. */
  status(text: string): void
  /** close takes the splash away; again is a no-op. */
  close(): void
}

/**
 * showSplash puts a small frameless window up while the server starts (in WSL that takes a while): the mark, the version,
 * what is happening, the copyright and the sponsor. It has no preload and no node; its link opens in the system browser.
 */
export function showSplash(page: string, query: Record<string, string>, icon?: string): Splash {
  const w = new BrowserWindow({
    width: 520,
    height: 340,
    frame: false,
    resizable: false,
    movable: true,
    center: true,
    show: false,
    skipTaskbar: false,
    title: 'Conductor',
    icon,
    backgroundColor: '#18181B',
    webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true },
  })
  w.webContents.on('will-navigate', (e, url) => {
    e.preventDefault()
    if (external(url)) void shell.openExternal(url)
  })
  w.webContents.setWindowOpenHandler(({ url }) => {
    if (external(url)) void shell.openExternal(url)
    return { action: 'deny' }
  })
  w.once('ready-to-show', () => w.show())
  let last = ''
  let loaded = false
  const push = () => {
    if (loaded && !w.isDestroyed()) void w.webContents.executeJavaScript(`window.setStatus && window.setStatus(${JSON.stringify(last)})`).catch(() => {})
  }
  w.webContents.once('did-finish-load', () => {
    loaded = true
    push()
  })
  void w.loadFile(page, { query })
  return {
    status(text) {
      last = text
      push()
    },
    close() {
      if (!w.isDestroyed()) w.destroy()
    },
  }
}
