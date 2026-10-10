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
import { defaultSettings, loadSettings, migrateToWsl, owedNotice, saveSettings, type DesktopSettings } from './settings'
import { loginShellPath, mergePaths } from './shellPath'
import { createTray } from './tray'
import { createWindow, followOrigin, logWindowOptions, restrictPermissions, sameOrigin } from './window'
import { nextZoom, zoomKey, type ZoomMove } from './zoom'
import { WslLauncher, wslAvailable, wslNetworkingMode } from './launcher-wsl'
import { ICE_UDP_PORT, UdpForwarder, windowsLanAddress } from './udp-forwarder'
import { allowIceThroughFirewall, firewallRuleExists, type IceStatus } from './firewall'
import { InviteDelivery, inviteInArgv, invitePath, parseInvite, type Invite } from './invite'
import { RELEASES_URL, startUpdater, updateChannel } from './updater'
import { buildLine, showSplash, SPLASH_STEPS, splashQuery, type Splash } from './splash'

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

// The invite scheme: conductor://<switchyard>/join/<token> opens here.
if (app.isPackaged || process.platform !== 'linux') app.setAsDefaultProtocolClient('conductor')
let openUrlInvite: Invite | null = null
let pendingInvite: ((inv: Invite) => void) | null = null
app.on('open-url', (e, url) => {
  e.preventDefault()
  const inv = parseInvite(url)
  if (!inv) return
  if (pendingInvite) pendingInvite(inv)
  else openUrlInvite = inv
})

if (!app.requestSingleInstanceLock()) {
  app.quit()
} else {
  void run()
}

async function run() {
  await app.whenReady()
  // Up first: in WSL the server takes a while to start, and the window comes only once it answers.
  let splash: Splash | null = showSplash(join(resources, 'static/splash.html'), splashQuery({ version: app.getVersion(), build: buildLine(app.getVersion(), process.platform, process.arch, process.versions.electron ?? '') }), iconPath())
  const endSplash = () => {
    splash?.close()
    splash = null
  }
  const userData = app.getPath('userData')
  const logs = new Logs(join(userData, 'logs'))
  const log = (kind: 'main' | 'server', line: string) => logs.line(kind, line)
  const settingsFile = join(userData, 'settings.json')
  const defaults = defaultSettings(homedir(), userData)
  let settings = loadSettings(settingsFile, defaults)
  // A notice owed to settings from an earlier build: shown once, in the workbench, then marked.
  const owed = owedNotice(settings)
  let notice = owed.notice
  if (owed.settings !== settings) {
    settings = owed.settings
    saveSettings(settingsFile, settings)
  }
  const token = randomBytes(32).toString('hex')
  process.env.CONDUCTOR_DESKTOP_VERSION = app.getVersion()

  const source = sourceBinary()
  let launcher: Launcher
  if (process.platform === 'win32') {
    splash?.status(SPLASH_STEPS.wsl)
    const wsl = await wslAvailable(settings.wslDistro)
    if (!wsl.ok) {
      endSplash()
      await showSetup(wsl.reason)
      return
    }
    launcher = new WslLauncher({ source, distro: wsl.distro, version: app.getVersion(), log: (l) => log('main', l), windowsHome: settings.wslWindowsHome })
  } else {
    if (!existsSync(source)) {
      endSplash()
      await dialog.showMessageBox({ type: 'error', title: 'Conductor', message: `The server binary is missing: ${source}`, detail: dev ? 'Run make build-go first, or set CONDUCTOR_DESKTOP_BIN.' : 'Reinstall Conductor.' })
      app.quit()
      return
    }
    launcher = new NativeLauncher({ source, stable: app.isPackaged ? stableBinaryPath(process.platform, homedir()) : '', version: app.getVersion(), ensureStable: ensureStableBinary, log: (l) => log('main', l) })
  }
  log('main', `conductor desktop ${app.getVersion()} (${process.platform}), server ${launcher.describe()}`)
  splash?.status(SPLASH_STEPS.prepare)
  await launcher.prepare()
  if (launcher instanceof WslLauncher) {
    // The settings are the distribution's paths; ones saved as Windows
    // paths by an earlier build move to the defaults inside it.
    const m = migrateToWsl(settings, launcher.linuxHome())
    if (m.changed) {
      settings = m.settings
      saveSettings(settingsFile, settings)
      log('main', `settings: the paths are inside ${launcher.describe()} now: ${settings.defaultCwd}`)
    }
  }
  const shellPath = mergePaths(await loginShellPath(process.env.SHELL ?? ''), process.env.PATH ?? '', homedir())

  // WebRTC from inside WSL: in NAT mode (the default, left as it is) the
  // app forwards one UDP port into the distribution and the server
  // advertises the Windows LAN address; in mirrored mode nothing is needed.
  const ice: IceStatus = { forwarding: false, port: ICE_UDP_PORT, publicIp: '', wslAddress: '', firewall: 'unknown' }
  let forwarder: UdpForwarder | null = null
  if (launcher instanceof WslLauncher) {
    if (wslNetworkingMode() === 'mirrored') ice.reason = 'WSL is in mirrored networking mode: nothing to forward'
    else {
      ice.publicIp = windowsLanAddress()
      if (!ice.publicIp) ice.reason = 'no LAN address found on this machine'
    }
  } else ice.reason = 'not Windows'
  const wsl = launcher instanceof WslLauncher ? launcher : null
  const supervisor = new ServerSupervisor({
    launcher,
    args: SERVE_ARGS,
    env: () => {
      const env = serverEnv(process.env, settings, token, shellPath)
      // Inside a WSL distribution the paths are its own: the server's data
      // beside its binary, the Linux home as the root (and the Windows
      // profile under /mnt when asked).
      return wsl ? wsl.linuxEnv(env, settings.wslWindowsHome, process.env.USERPROFILE ?? '', ice.publicIp ? { port: ice.port, publicIp: ice.publicIp } : undefined) : env
    },
    log,
  })
  /** After each start of the server inside WSL: the distribution's address of the moment, and the forwarder on it. */
  const forwardIce = async () => {
    if (!wsl || !ice.publicIp) return
    const addr = await wsl.address()
    if (!addr) {
      log('main', 'udp forwarder: the distribution gave no address; WebRTC from WSL is off until the next start')
      return
    }
    ice.wslAddress = addr
    if (!forwarder) {
      forwarder = new UdpForwarder({ port: ice.port, target: { host: addr, port: ice.port }, log: (l) => log('main', l) })
      try {
        await forwarder.start()
        ice.forwarding = true
      } catch (e) {
        forwarder = null
        ice.reason = (e as Error).message
        log('main', `udp forwarder: ${(e as Error).message}; WebRTC from WSL is off`)
        return
      }
    } else forwarder.retarget({ host: addr, port: ice.port })
    log('main', `udp forwarder: ${ice.publicIp}:${ice.port} → ${addr}:${ice.port}`)
    ice.firewall = await firewallRuleExists(ice.port)
    if (ice.firewall === 'missing') log('main', `udp forwarder: no firewall rule for UDP ${ice.port}; Settings offers to add it`)
  }
  supervisor.on('state', (st) => {
    if (st.state === 'running') void forwardIce()
  })
  let main: BrowserWindow | null = null
  let tray: Tray | null = null
  let quitting = false
  const origin = () => supervisor.status.url || 'http://127.0.0.1'

  // An invite for the open window goes to its page over the bridge, which routes in place: the workbench keeps its terminals.
  const delivery = new InviteDelivery({
    send: (inv) => {
      if (main && !main.isDestroyed()) main.webContents.send('conductor:invite', inv)
    },
    load: (inv) => {
      log('main', 'invite: the page did not answer; loading the join page')
      if (main && !main.isDestroyed()) void main.loadURL(origin() + invitePath(inv))
    },
    setTimer: (f, ms) => setTimeout(f, ms),
    clearTimer: (t) => clearTimeout(t as NodeJS.Timeout),
  })
  /** openInvite opens the app's own join page for an invite: it signals to the server the invite names (a switchyard). */
  const openInvite = (inv: Invite) => {
    log('main', `invite: joining through ${inv.server}`)
    if (main && !main.isDestroyed()) {
      main.show()
      main.focus()
      if (delivery.deliver(inv) === 'sent') log('main', 'invite: handed to the open window')
      return
    }
    show(invitePath(inv)).focus()
  }
  pendingInvite = (inv) => openInvite(inv)
  const show = (path = '/') => {
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
    delivery.pageLeft()
    main.webContents.on('did-start-navigation', (details) => {
      if (details.isMainFrame && !details.isSameDocument) delivery.pageLeft()
    })
    void main.loadURL(origin() + path)
    return main
  }
  const showLog = () => {
    const w = new BrowserWindow(logWindowOptions(__dirname))
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
    notice: () => {
      const n = notice
      notice = ''
      return n
    },
    mainWindow: () => main,
    inviteReady: (sender) => {
      if (main && !main.isDestroyed() && sender === main.webContents) delivery.pageReady()
    },
    serverVersion: () => supervisor.status.version ?? '',
    ice: () => ({ ...ice }),
    wsl: () => (wsl ? { distro: wsl.distro() } : null),
    allowIceFirewall: async () => {
      if (await allowIceThroughFirewall(ice.port)) ice.firewall = await firewallRuleExists(ice.port)
      return ice.firewall
    },
  })
  restrictPermissions()
  // The workbench's zoom: the app's level, on every window of the server's origin, the keys taken before the page sees them.
  const applyZoom = (wc: Electron.WebContents) => {
    if (!wc.isDestroyed() && sameOrigin(wc.getURL(), origin())) wc.setZoomLevel(settings.zoomLevel)
  }
  const zoom = (move: ZoomMove) => {
    const level = nextZoom(settings.zoomLevel, move)
    if (level !== settings.zoomLevel) {
      settings = { ...settings, zoomLevel: level }
      saveSettings(settingsFile, settings)
    }
    for (const w of BrowserWindow.getAllWindows()) applyZoom(w.webContents)
  }
  app.on('web-contents-created', (_e, wc) => {
    if (wc.getType() !== 'window') return
    wc.on('before-input-event', (e, input) => {
      if (!sameOrigin(wc.getURL(), origin())) return
      const move = zoomKey(input, process.platform)
      if (!move) return
      e.preventDefault()
      zoom(move)
    })
    wc.on('did-finish-load', () => applyZoom(wc))
  })
  Menu.setApplicationMenu(
    buildMenu({
      settings: () => void show().loadURL(origin() + '/settings'),
      openInBrowser: () => void import('electron').then(({ shell }) => shell.openExternal(`${origin()}/#token=${encodeURIComponent(token)}`)),
      restart: () => void supervisor.restart().catch(() => {}),
      showLog,
      zoom,
      dev,
    }),
  )
  supervisor.on('state', (st) => {
    if (!main || main.isDestroyed()) return
    main.webContents.send('conductor:serverState', st)
    // A restarted server listens on a fresh port: the window follows it, on the same page.
    const next = st.state === 'running' ? followOrigin(main.webContents.getURL(), origin()) : null
    if (next) {
      log('main', `server origin changed; the window follows to ${new URL(next).origin}`)
      void main.loadURL(next)
    }
  })
  supervisor.on('gaveUp', (st) => {
    void dialog.showMessageBox({ type: 'error', title: 'Conductor', message: 'The server keeps failing', detail: `${st.lastError ?? ''}\nSee the server log (Server › Server log), then Restart server.` })
  })

  splash?.status(SPLASH_STEPS.start)
  try {
    await supervisor.start()
  } catch (e) {
    endSplash()
    await dialog.showMessageBox({ type: 'error', title: 'Conductor', message: 'The server did not start', detail: (e as Error).message })
    showLog()
    return
  }
  // An invite the app was started with (Windows and Linux pass it in argv; macOS through open-url, taken below or before this point).
  const first = inviteInArgv(process.argv) ?? openUrlInvite
  splash?.status(SPLASH_STEPS.open)
  if (first) openInvite(first)
  else show()
  // The splash goes when the workbench window shows (or after a while, should it never be ready).
  const opened = main as BrowserWindow | null
  if (opened && !opened.isDestroyed()) opened.once('show', endSplash)
  setTimeout(endSplash, 20_000)
  tray = createTray({ icon: iconPath(), show: () => show(), openInBrowser: () => void import('electron').then(({ shell }) => shell.openExternal(`${origin()}/#token=${encodeURIComponent(token)}`)), restart: () => void supervisor.restart().catch(() => {}), showLog, quit: () => app.quit() })
  const channel = updateChannel(process.platform, !!process.env.APPIMAGE, app.isPackaged)
  log('main', `updates: ${channel === 'auto' ? 'from the GitHub releases, checked every six hours' : channel === 'link' ? `by your package manager (${RELEASES_URL})` : 'none in development'}`)
  void startUpdater(channel, logs, (version) => {
    void dialog.showMessageBox({ type: 'info', title: 'Conductor', message: `Conductor ${version} is downloaded`, detail: 'It installs when you quit the app.' })
  })

  app.on('second-instance', (_e, argv) => {
    const inv = inviteInArgv(argv)
    if (inv) return openInvite(inv)
    show().focus()
  })
  app.on('activate', () => show())
  app.on('window-all-closed', () => {
    if (!settings.closeToTray) app.quit()
  })
  app.on('before-quit', (e) => {
    if (quitting) return
    quitting = true
    e.preventDefault()
    forwarder?.stop()
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
