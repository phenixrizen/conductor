import { app, BrowserWindow, dialog, Menu, type Tray } from 'electron'
import { randomBytes } from 'node:crypto'
import { existsSync } from 'node:fs'
import { homedir } from 'node:os'
import { join } from 'node:path'
import { serverEnv, SERVE_ARGS } from './env'
import { ensureStableBinary, stableBinaryPath } from './install'
import { registerIpc } from './ipc'
import { NativeLauncher, type Launcher } from './launcher'
import { Logs } from './logs'
import { buildMenu } from './menu'
import { ServerSupervisor } from './server'
import { defaultSettings, loadSettings, saveSettings, type DesktopSettings } from './settings'
import { loginShellPath, mergePaths } from './shellPath'
import { createTray } from './tray'
import { createWindow, restrictPermissions } from './window'
import { WslLauncher, wslAvailable } from './launcher-wsl'

const dev = !app.isPackaged
const resources = app.isPackaged ? process.resourcesPath : join(__dirname, '..')

/** The bundled server binary: resources/conductor when packaged, else CONDUCTOR_DESKTOP_BIN or the checkout's bin/conductor. */
function sourceBinary(): string {
  if (app.isPackaged) return join(resources, process.platform === 'win32' ? 'conductor-linux' : 'conductor')
  return process.env.CONDUCTOR_DESKTOP_BIN || join(__dirname, '..', '..', 'bin', 'conductor')
}

function iconPath(): string {
  return join(resources, app.isPackaged ? 'icon.png' : 'build/icon.png')
}

if (!app.requestSingleInstanceLock()) {
  app.quit()
} else {
  void run()
}

async function run() {
  await app.whenReady()
  const userData = app.getPath('userData')
  const logs = new Logs(join(userData, 'logs'))
  const log = (kind: 'main' | 'server', line: string) => logs.line(kind, line)
  const settingsFile = join(userData, 'settings.json')
  const defaults = defaultSettings(homedir(), userData)
  let settings = loadSettings(settingsFile, defaults)
  const token = randomBytes(32).toString('hex')
  process.env.CONDUCTOR_DESKTOP_VERSION = app.getVersion()

  const source = sourceBinary()
  let launcher: Launcher
  if (process.platform === 'win32') {
    const wsl = await wslAvailable(settings.wslDistro)
    if (!wsl.ok) {
      await showSetup(wsl.reason)
      return
    }
    launcher = new WslLauncher({ source, distro: wsl.distro, version: app.getVersion(), log: (l) => log('main', l), windowsHome: settings.wslWindowsHome })
  } else {
    if (!existsSync(source)) {
      await dialog.showMessageBox({ type: 'error', title: 'Conductor', message: `The server binary is missing: ${source}`, detail: dev ? 'Run make build-go first, or set CONDUCTOR_DESKTOP_BIN.' : 'Reinstall Conductor.' })
      app.quit()
      return
    }
    launcher = new NativeLauncher({ source, stable: app.isPackaged ? stableBinaryPath(process.platform, homedir()) : '', version: app.getVersion(), ensureStable: ensureStableBinary, log: (l) => log('main', l) })
  }
  log('main', `conductor desktop ${app.getVersion()} (${process.platform}), server ${launcher.describe()}`)
  await launcher.prepare()
  const shellPath = mergePaths(await loginShellPath(process.env.SHELL ?? ''), process.env.PATH ?? '', homedir())

  const supervisor = new ServerSupervisor({
    launcher,
    args: SERVE_ARGS,
    env: () => serverEnv(process.env, settings, token, shellPath),
    log,
  })
  let main: BrowserWindow | null = null
  let tray: Tray | null = null
  let quitting = false
  const origin = () => supervisor.status.url || 'http://127.0.0.1'

  const show = () => {
    if (main && !main.isDestroyed()) {
      main.show()
      return main
    }
    main = createWindow({
      preload: join(__dirname, 'preload.js'),
      origin,
      icon: iconPath(),
      onClose: (w, e) => {
        if (!quitting && settings.closeToTray && tray) {
          e.preventDefault()
          w.hide()
        }
      },
    })
    void main.loadURL(origin() + '/')
    return main
  }
  const showLog = () => {
    const w = new BrowserWindow({ width: 900, height: 600, title: 'Conductor server log', webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true } })
    void w.loadFile(join(resources, app.isPackaged ? 'static/log.html' : 'static/log.html'))
    const send = (line: string) => w.webContents.send('log-line', line)
    w.webContents.once('did-finish-load', () => {
      for (const line of logs.ring) send(line)
      const off = logs.onLine(send)
      w.on('closed', off)
    })
  }

  registerIpc({
    origin,
    token: () => token,
    supervisor,
    settings: {
      get: () => settings,
      set: (s: DesktopSettings) => {
        settings = s
        saveSettings(settingsFile, s)
      },
    },
    showLog,
    mainWindow: () => main,
    serverVersion: () => supervisor.status.version ?? '',
  })
  restrictPermissions()
  Menu.setApplicationMenu(
    buildMenu({
      settings: () => void show().loadURL(origin() + '/settings'),
      openInBrowser: () => void import('electron').then(({ shell }) => shell.openExternal(`${origin()}/#token=${encodeURIComponent(token)}`)),
      restart: () => void supervisor.restart().catch(() => {}),
      showLog,
      dev,
    }),
  )
  supervisor.on('state', (st) => {
    if (main && !main.isDestroyed()) main.webContents.send('conductor:serverState', st)
  })
  supervisor.on('gaveUp', (st) => {
    void dialog.showMessageBox({ type: 'error', title: 'Conductor', message: 'The server keeps failing', detail: `${st.lastError ?? ''}\nSee the server log (Server › Server log), then Restart server.` })
  })

  try {
    await supervisor.start()
  } catch (e) {
    await dialog.showMessageBox({ type: 'error', title: 'Conductor', message: 'The server did not start', detail: (e as Error).message })
    showLog()
    return
  }
  tray = createTray({ icon: iconPath(), show, openInBrowser: () => void import('electron').then(({ shell }) => shell.openExternal(`${origin()}/#token=${encodeURIComponent(token)}`)), restart: () => void supervisor.restart().catch(() => {}), showLog, quit: () => app.quit() })
  show()

  app.on('second-instance', () => show().focus())
  app.on('activate', () => show())
  app.on('window-all-closed', () => {
    if (!settings.closeToTray) app.quit()
  })
  app.on('before-quit', (e) => {
    if (quitting) return
    quitting = true
    e.preventDefault()
    supervisor
      .stop()
      .catch(() => {})
      .finally(() => {
        tray?.destroy()
        app.exit(0)
      })
  })
}

/** showSetup is the Windows first-run screen: what WSL is for, an Install WSL button (an elevated `wsl --install`, fixed argv), Try again. */
async function showSetup(reason: string) {
  const { ipcMain } = await import('electron')
  const { execFile } = await import('node:child_process')
  const w = new BrowserWindow({ width: 720, height: 560, title: 'Conductor needs WSL', webPreferences: { preload: join(__dirname, 'setup-preload.js'), contextIsolation: true, nodeIntegration: false, sandbox: true } })
  ipcMain.removeHandler('conductor:installWsl')
  ipcMain.removeHandler('conductor:retrySetup')
  ipcMain.handle('conductor:installWsl', () => {
    execFile('powershell.exe', ['-NoProfile', '-Command', 'Start-Process wsl.exe -ArgumentList "--install" -Verb RunAs'], { windowsHide: true }, () => {})
  })
  ipcMain.handle('conductor:retrySetup', () => {
    app.relaunch()
    app.exit(0)
  })
  await w.loadFile(join(resources, 'static/setup.html'), { query: { reason } })
  w.on('closed', () => app.quit())
}
