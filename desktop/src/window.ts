import { join } from 'node:path'
import { BrowserWindow, shell, session, type BrowserWindowConstructorOptions } from 'electron'

export interface WindowOptions {
  preload: string
  /** The server's origin (http://127.0.0.1:<port>): the only place the window navigates within. */
  origin: () => string
  icon?: string
  onClose?: (w: BrowserWindow, e: Electron.Event) => void
}

/** sameOrigin reports whether url is on the server's origin. */
export function sameOrigin(url: string, origin: string): boolean {
  try {
    const u = new URL(url)
    const o = new URL(origin)
    return u.protocol === o.protocol && u.host === o.host
  } catch {
    return false
  }
}

/**
 * followOrigin is where a window showing the app's own server goes once that server listens on a new origin: a restart (a crash, the
 * health check, Restart server) binds a fresh port, and a window left on the old one shows a dead page. The same path, query and hash
 * on the new origin; null when the window is there already or is not on a local server page (the splash, an outside site).
 */
export function followOrigin(current: string, origin: string): string | null {
  try {
    const u = new URL(current)
    const o = new URL(origin)
    if (u.protocol !== 'http:' && u.protocol !== 'https:') return null
    if (!['127.0.0.1', 'localhost', '[::1]'].includes(u.hostname)) return null
    if (u.protocol === o.protocol && u.host === o.host) return null
    return o.origin + u.pathname + u.search + u.hash
  } catch {
    return null
  }
}

/** external reports whether url may be opened outside: http(s) only, never file:, javascript: or an app's own scheme. */
export function external(url: string): boolean {
  try {
    const u = new URL(url)
    return u.protocol === 'http:' || u.protocol === 'https:'
  } catch {
    return false
  }
}

/**
 * logWindowOptions are the server log window's: its page (static/log.html) shows the lines its preload passes it (window.conductorLog,
 * log-preload.js in dir), so without the preload it stayed an empty dark box, seen by the owner on Windows on 2026-10-06.
 */
export function logWindowOptions(dir: string): BrowserWindowConstructorOptions {
  return { width: 900, height: 600, title: 'Conductor server log', webPreferences: { preload: join(dir, 'log-preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true } }
}

/**
 * createWindow makes the workbench window: context isolation on, no node integration, navigation kept to the server's origin,
 * window.open on that origin as a window of the app and anything else in the system browser, and no permissions beyond the clipboard.
 */
export function createWindow(o: WindowOptions): BrowserWindow {
  const w = new BrowserWindow({
    width: 1440,
    height: 900,
    minWidth: 390,
    minHeight: 500,
    title: 'Conductor',
    icon: o.icon,
    backgroundColor: '#263D35',
    show: false,
    webPreferences: { preload: o.preload, contextIsolation: true, nodeIntegration: false, sandbox: true, spellcheck: false },
  })
  w.webContents.on('will-navigate', (e, url) => {
    if (!sameOrigin(url, o.origin())) {
      e.preventDefault()
      if (external(url)) void shell.openExternal(url)
    }
  })
  w.webContents.setWindowOpenHandler(({ url }) => {
    if (sameOrigin(url, o.origin())) return { action: 'allow', overrideBrowserWindowOptions: { webPreferences: { preload: o.preload, contextIsolation: true, nodeIntegration: false, sandbox: true } } }
    if (external(url)) void shell.openExternal(url)
    return { action: 'deny' }
  })
  w.on('close', (e) => o.onClose?.(w, e))
  w.once('ready-to-show', () => w.show())
  return w
}

/** restrictPermissions denies every permission request but the clipboard's. */
export function restrictPermissions(): void {
  session.defaultSession.setPermissionRequestHandler((_wc, permission, cb) => {
    cb(permission === 'clipboard-read' || permission === 'clipboard-sanitized-write')
  })
}
