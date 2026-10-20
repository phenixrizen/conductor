import { Menu, Tray, nativeImage, type BrowserWindow, type MenuItemConstructorOptions } from 'electron'

export interface TrayDeps {
  icon: string
  show: () => BrowserWindow
  openInBrowser: () => void
  restart: () => void
  showLog: () => void
  quit: () => void
  /** Installs the update downloaded now, the version the menu offers: the app quits, the server with it. */
  installUpdate: (version: string) => void
}

/**
 * trayItems is the tray's menu: open the window, open in the browser, restart the server, the log, quit; with an update
 * downloaded (its version, #290), Restart to update first.
 */
export function trayItems(d: Omit<TrayDeps, 'icon'>, update: string): MenuItemConstructorOptions[] {
  const items: MenuItemConstructorOptions[] = [
    { label: 'Open Conductor', click: () => d.show().focus() },
    { label: 'Open in browser', click: d.openInBrowser },
    { type: 'separator' },
    { label: 'Restart server', click: d.restart },
    { label: 'Server log', click: d.showLog },
    { type: 'separator' },
    { label: 'Quit', click: d.quit },
  ]
  return update ? [{ label: `Restart to update to ${update}`, click: () => d.installUpdate(update) }, { type: 'separator' }, ...items] : items
}

/** createTray puts Conductor in the tray (trayItems); its update shows the menu with the update downloaded, '' for none. */
export function createTray(d: TrayDeps): { tray: Tray; update: (version: string) => void } {
  const image = nativeImage.createFromPath(d.icon)
  const tray = new Tray(image.isEmpty() ? nativeImage.createEmpty() : image.resize({ width: 18, height: 18 }))
  tray.setToolTip('Conductor')
  tray.setContextMenu(Menu.buildFromTemplate(trayItems(d, '')))
  tray.on('click', () => d.show().focus())
  return { tray, update: (version) => tray.setContextMenu(Menu.buildFromTemplate(trayItems(d, version))) }
}
