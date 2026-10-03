import { Menu, app, type MenuItemConstructorOptions } from 'electron'

export interface MenuDeps {
  settings: () => void
  openInBrowser: () => void
  restart: () => void
  showLog: () => void
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
    { label: 'View', submenu: [{ role: 'reload' }, ...(d.dev ? [{ role: 'toggleDevTools' as const }] : []), { type: 'separator' }, { role: 'resetZoom' }, { role: 'zoomIn' }, { role: 'zoomOut' }, { type: 'separator' }, { role: 'togglefullscreen' }] },
    { role: 'windowMenu' },
    { role: 'help', submenu: [{ label: `Conductor ${app.getVersion()}`, enabled: false }] },
  ]
  return Menu.buildFromTemplate(template)
}
