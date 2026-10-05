import { contextBridge, ipcRenderer } from 'electron'

contextBridge.exposeInMainWorld('conductorSetup', {
  installWsl: (): Promise<void> => ipcRenderer.invoke('conductor:installWsl'),
  retry: (): Promise<void> => ipcRenderer.invoke('conductor:retrySetup'),
})
