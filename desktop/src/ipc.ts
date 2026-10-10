import { app, ipcMain, shell, type BrowserWindow, type IpcMainInvokeEvent, type WebContents } from 'electron'
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
  /** The one-time notice owed to this person, handed out once ('' after, and when none is owed). */
  notice: () => string
  mainWindow: () => BrowserWindow | null
  serverVersion: () => string
  /** What the app forwards for WebRTC from WSL (Windows), for Settings. */
  ice: () => IceStatus
  /** Adds the firewall rule for the ICE port through an elevated netsh; the rule's state after. */
  allowIceFirewall: () => Promise<IceStatus['firewall']>
  /** Windows: the WSL distribution the server runs in, whose paths the settings are; null elsewhere. */
  wsl?: () => { distro: string } | null
  /** The page in sender listens for invites (the bridge's onInvite): one pending goes to it now. */
  inviteReady?: (sender: WebContents) => void
  /** The page in sender routed the invite it was sent with id. */
  inviteTaken?: (sender: WebContents, id: number) => void
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
  ipcMain.handle('conductor:notice', guard(() => d.notice()))
  ipcMain.handle(
    'conductor:settings:set',
    guard(async (_e, patch) => {
      const cur = d.settings.get()
      // The zoom is the app's own (Ctrl+= and Ctrl+-): a form saved with an older level leaves the current one.
      const next = { ...cur, ...(patch as Partial<DesktopSettings>), zoomLevel: cur.zoomLevel } as DesktopSettings
      const problems = validate(next, !!d.wsl?.())
      if (problems.length) throw new Error(problems.join('; '))
      d.settings.set(next)
      if (serverAffecting(cur, next)) await d.supervisor.restart()
      return next
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
    'conductor:inviteReady',
    guard((e) => {
      d.inviteReady?.(e.sender)
    }),
  )
  ipcMain.handle(
    'conductor:inviteTaken',
    guard((e, id) => {
      if (typeof id === 'number' && Number.isSafeInteger(id)) d.inviteTaken?.(e.sender, id)
    }),
  )
  ipcMain.handle(
    'conductor:versions',
    guard(() => ({ app: app.getVersion(), electron: process.versions.electron, node: process.versions.node, chrome: process.versions.chrome, server: d.serverVersion() })),
  )
}
