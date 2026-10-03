import { app, dialog, ipcMain, shell, type BrowserWindow, type IpcMainInvokeEvent } from 'electron'
import { external } from './window'
import { serverAffecting, validate, type DesktopSettings } from './settings'
import type { ServerSupervisor, ServerStatus } from './server'
import type { IceStatus } from './firewall'

export interface IpcDeps {
  origin: () => string
  token: () => string
  supervisor: ServerSupervisor
  settings: { get: () => DesktopSettings; set: (s: DesktopSettings) => void }
  showLog: () => void
  mainWindow: () => BrowserWindow | null
  serverVersion: () => string
  /** What the app forwards for WebRTC from WSL (Windows), for Settings. */
  ice: () => IceStatus
  /** Adds the firewall rule for the ICE port through an elevated netsh; the rule's state after. */
  allowIceFirewall: () => Promise<IceStatus['firewall']>
}

/** trusted says whether the sender is the workbench served by this app's own server, or the app's own pages. */
export function trusted(senderUrl: string, origin: string): boolean {
  try {
    const u = new URL(senderUrl)
    const o = new URL(origin)
    if (u.protocol === 'file:') return true
    return u.protocol === o.protocol && u.host === o.host
  } catch {
    return false
  }
}

/** registerIpc wires the handlers the preload bridge calls; each checks its sender first. */
export function registerIpc(d: IpcDeps): void {
  const guard = <T>(f: (e: IpcMainInvokeEvent, ...args: unknown[]) => T) => (e: IpcMainInvokeEvent, ...args: unknown[]): T => {
    if (!trusted(e.senderFrame?.url ?? '', d.origin())) throw new Error('refused: not the workbench')
    return f(e, ...args)
  }
  ipcMain.handle('conductor:token', guard(() => d.token()))
  ipcMain.handle(
    'conductor:openExternal',
    guard(async (_e, url) => {
      if (typeof url === 'string' && external(url)) await shell.openExternal(url)
    }),
  )
  ipcMain.handle('conductor:settings:get', guard(() => d.settings.get()))
  ipcMain.handle(
    'conductor:settings:set',
    guard(async (_e, patch) => {
      const cur = d.settings.get()
      const next = { ...cur, ...(patch as Partial<DesktopSettings>) } as DesktopSettings
      const problems = validate(next)
      if (problems.length) throw new Error(problems.join('; '))
      d.settings.set(next)
      if (serverAffecting(cur, next)) await d.supervisor.restart()
      return next
    }),
  )
  ipcMain.handle(
    'conductor:pickDirectory',
    guard(async () => {
      const w = d.mainWindow()
      const r = await (w ? dialog.showOpenDialog(w, { properties: ['openDirectory', 'createDirectory'] }) : dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'] }))
      return r.canceled || !r.filePaths.length ? null : r.filePaths[0]
    }),
  )
  ipcMain.handle(
    'conductor:restartServer',
    guard(async () => {
      await d.supervisor.restart()
    }),
  )
  ipcMain.handle(
    'conductor:openInBrowser',
    guard(async () => {
      // The token travels in the fragment, which never reaches the server and which the workbench takes and drops.
      await shell.openExternal(`${d.origin()}/#token=${encodeURIComponent(d.token())}`)
    }),
  )
  ipcMain.handle(
    'conductor:showLog',
    guard(() => {
      d.showLog()
    }),
  )
  ipcMain.handle('conductor:serverState', guard((): ServerStatus => d.supervisor.status))
  ipcMain.handle('conductor:ice', guard((): IceStatus => d.ice()))
  ipcMain.handle(
    'conductor:allowIceFirewall',
    guard(() => d.allowIceFirewall()),
  )
  ipcMain.handle(
    'conductor:versions',
    guard(() => ({ app: app.getVersion(), electron: process.versions.electron, node: process.versions.node, chrome: process.versions.chrome, server: d.serverVersion() })),
  )
}
