import { Menu, Tray, nativeImage, type BrowserWindow } from 'electron'

export interface TrayDeps {
  icon: string
  show: () => BrowserWindow
  openInBrowser: () => void
  restart: () => void
  showLog: () => void
  quit: () => void
}

/** createTray puts Conductor in the tray: open the window, open in the browser, restart the server, the log, quit. */
export function createTray(d: TrayDeps): Tray {
  const image = nativeImage.createFromPath(d.icon)
  const tray = new Tray(image.isEmpty() ? nativeImage.createEmpty() : image.resize({ width: 18, height: 18 }))
  tray.setToolTip('Conductor')
  tray.setContextMenu(
    Menu.buildFromTemplate([
      { label: 'Open Conductor', click: () => d.show().focus() },
      { label: 'Open in browser', click: d.openInBrowser },
      { type: 'separator' },
      { label: 'Restart server', click: d.restart },
      { label: 'Server log', click: d.showLog },
      { type: 'separator' },
      { label: 'Quit', click: d.quit },
    ]),
  )
  tray.on('click', () => d.show().focus())
  return tray
}
