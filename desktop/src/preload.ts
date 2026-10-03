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
    pickDirectory: (): Promise<string | null> => ipcRenderer.invoke('conductor:pickDirectory'),
  },
  restartServer: (): Promise<void> => ipcRenderer.invoke('conductor:restartServer'),
  openInBrowser: (): Promise<void> => ipcRenderer.invoke('conductor:openInBrowser'),
  showLog: (): Promise<void> => ipcRenderer.invoke('conductor:showLog'),
  serverState: () => ipcRenderer.invoke('conductor:serverState'),
  ice: () => ipcRenderer.invoke('conductor:ice'),
  allowIceFirewall: (): Promise<string> => ipcRenderer.invoke('conductor:allowIceFirewall'),
  versions: () => ipcRenderer.invoke('conductor:versions'),
  onServerState: (cb: (state: unknown) => void): (() => void) => {
    const handler = (_e: unknown, state: unknown) => cb(state)
    ipcRenderer.on('conductor:serverState', handler)
    return () => ipcRenderer.removeListener('conductor:serverState', handler)
  },
}

contextBridge.exposeInMainWorld('conductorDesktop', bridge)
