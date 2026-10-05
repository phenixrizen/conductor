import { Menu, app, type MenuItemConstructorOptions } from 'electron'
import type { ZoomMove } from './zoom'

export interface MenuDeps {
  settings: () => void
  openInBrowser: () => void
  restart: () => void
  showLog: () => void
  /** Zooms the workbench (the app keeps the level); the keys are taken before the page sees them, see zoom.ts. */
  zoom: (move: ZoomMove) => void
  dev: boolean
}

/** buildMenu is the app menu: Conductor (settings, quit), Server (open in browser, restart, log), Edit, View, Window, Help. */
export function buildMenu(d: MenuDeps): Menu {
  const mac = process.platform === 'darwin'
  const template: MenuItemConstructorOptions[] = [
    ...(mac ? [{ role: 'appMenu' as const, submenu: [{ role: 'about' as const }, { type: 'separator' as const }, { label: 'Settings…', accelerator: 'CmdOrCtrl+,', click: d.settings }, { type: 'separator' as const }, { role: 'hide' as const }, { role: 'quit' as const }] }] : []),
    {
      label: 'Server',
      submenu: [
        ...(mac ? [] : [{ label: 'Settings…', accelerator: 'CmdOrCtrl+,', click: d.settings }, { type: 'separator' as const }]),
        { label: 'Open in browser', click: d.openInBrowser },
        { label: 'Restart server', click: d.restart },
        { label: 'Server log', click: d.showLog },
        ...(mac ? [] : [{ type: 'separator' as const }, { role: 'quit' as const }]),
      ],
    },
    { role: 'editMenu' },
    { label: 'View', submenu: [{ role: 'reload' }, ...(d.dev ? [{ role: 'toggleDevTools' as const }] : []), { type: 'separator' }, { label: 'Actual Size', accelerator: 'CmdOrCtrl+0', click: () => d.zoom('reset') }, { label: 'Zoom In', accelerator: 'CmdOrCtrl+=', click: () => d.zoom('in') }, { label: 'Zoom Out', accelerator: 'CmdOrCtrl+-', click: () => d.zoom('out') }, { type: 'separator' }, { role: 'togglefullscreen' }] },
    { role: 'windowMenu' },
    { role: 'help', submenu: [{ label: `Conductor ${app.getVersion()}`, enabled: false }] },
  ]
  return Menu.buildFromTemplate(template)
}
