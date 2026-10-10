import { contextBridge, ipcRenderer } from 'electron'

/** The bridge the workbench sees as window.conductorDesktop (web/app/utils/desktop.ts mirrors it). */
const bridge = {
  version: process.env.CONDUCTOR_DESKTOP_VERSION ?? '',
  platform: process.platform,
  token: (): Promise<string> => ipcRenderer.invoke('conductor:token'),
  openExternal: (url: string): Promise<void> => ipcRenderer.invoke('conductor:openExternal', url),
  settings: {
    get: () => ipcRenderer.invoke('conductor:settings:get'),
    set: (patch: Record<string, unknown>) => ipcRenderer.invoke('conductor:settings:set', patch),
  },
  restartServer: (): Promise<void> => ipcRenderer.invoke('conductor:restartServer'),
  openInBrowser: (): Promise<void> => ipcRenderer.invoke('conductor:openInBrowser'),
  showLog: (): Promise<void> => ipcRenderer.invoke('conductor:showLog'),
  serverState: () => ipcRenderer.invoke('conductor:serverState'),
  notice: (): Promise<string> => ipcRenderer.invoke('conductor:notice'),
  ice: () => ipcRenderer.invoke('conductor:ice'),
  allowIceFirewall: (): Promise<string> => ipcRenderer.invoke('conductor:allowIceFirewall'),
  versions: () => ipcRenderer.invoke('conductor:versions'),
  onServerState: (cb: (state: unknown) => void): (() => void) => {
    const handler = (_e: unknown, state: unknown) => cb(state)
    ipcRenderer.on('conductor:serverState', handler)
    return () => ipcRenderer.removeListener('conductor:serverState', handler)
  },
  /**
   * An invite the app was handed while this page is open: the page routes to its join page in place of a reload. cb says whether it
   * did, once its navigation has finished, and an invite routed is reported taken; one refused is left to the app, which loads the
   * join page for it.
   */
  onInvite: (cb: (invite: { server: string; token: string }) => boolean | Promise<boolean>): (() => void) => {
    const handler = (_e: unknown, invite: { server: string; token: string }, id: number) => {
      void Promise.resolve()
        .then(() => cb(invite))
        .then((routed) => {
          if (routed) void ipcRenderer.invoke('conductor:inviteTaken', id)
        })
        .catch(() => {})
    }
    const ask = () => void ipcRenderer.invoke('conductor:inviteReady')
    ipcRenderer.on('conductor:invite', handler)
    ipcRenderer.on('conductor:inviteAsk', ask)
    ask()
    return () => {
      ipcRenderer.removeListener('conductor:invite', handler)
      ipcRenderer.removeListener('conductor:inviteAsk', ask)
    }
  },
}

contextBridge.exposeInMainWorld('conductorDesktop', bridge)
