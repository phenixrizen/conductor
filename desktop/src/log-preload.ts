import { contextBridge, ipcRenderer } from 'electron'

contextBridge.exposeInMainWorld('conductorLog', {
  onLine: (cb: (line: string) => void) => ipcRenderer.on('log-line', (_e, line: string) => cb(line)),
})
