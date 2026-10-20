import { existsSync, mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { defaultSettings, loadSettings, migrateToWsl, noticeOnce, owedNotice, PLAIN_SWITCHYARD, plainSwitchyard, saveSettings, serverAffecting, SWITCHYARD_EXTRA, switchyardExtra, switchyardRefused, validate } from '../src/settings'
import { serverEnv, SERVE_ARGS } from '../src/env'

// On Windows the server and its files live in WSL (launcher-wsl); these POSIX paths, modes and shells are not the app's there.
const win = process.platform === 'win32'

describe('settings', () => {
  const home = '/home/me'
  const defaults = defaultSettings(home, '/home/me/.config/conductor-desktop')
  it('defaults to the home as the root and cwd, reach auto, yolo off', () => {
    expect(defaults).toMatchObject({ allowedRoots: [home], defaultCwd: home, yolo: false, reach: 'auto', closeToTray: true })
    expect(validate(defaults)).toEqual([])
  })
  it('names what is wrong', () => {
    expect(validate({ ...defaults, defaultCwd: '/elsewhere' })).toEqual(['the default working directory must lie under an allowed root'])
    expect(validate({ ...defaults, allowedRoots: [] })).toContain('at least one allowed root is needed')
    expect(validate({ ...defaults, reach: 'always' as 'auto' })).toContain('reach must be auto, manual or off')
    expect(validate({ ...defaults, allowedRoots: ['relative'] })).toContain('allowed root "relative" must be an absolute path')
  })
  it.skipIf(win)('round-trips through the file, mode 0600, and falls back to the defaults for a broken or bad file', () => {
    const dir = mkdtempSync(join(tmpdir(), 'cd-settings-'))
    const file = join(dir, 'settings.json')
    expect(loadSettings(file, defaults)).toEqual(defaults)
    const s = { ...defaults, yolo: true, reach: 'off' as const, allowedRoots: ['/home/me', '/srv'], zoomLevel: 1.5 }
    saveSettings(file, s)
    expect(statSync(file).mode & 0o777).toBe(0o600)
    expect(loadSettings(file, defaults)).toEqual(s)
    writeFileSync(file, '{not json')
    expect(loadSettings(file, defaults)).toEqual(defaults)
    writeFileSync(file, JSON.stringify({ defaultCwd: '/elsewhere' }))
    expect(loadSettings(file, defaults)).toEqual(defaults)
    writeFileSync(file, JSON.stringify({ yolo: 'yes', closeToTray: false }))
    // A file without the noticed list is an earlier build's: every notice is owed to it.
    expect(loadSettings(file, defaults)).toEqual({ ...defaults, closeToTray: false, noticed: [] })
    expect(readFileSync(file, 'utf8')).toContain('closeToTray')
    // The zoom kept is bounded; a file without one is at 100%.
    writeFileSync(file, JSON.stringify({ ...defaults, zoomLevel: 40 }))
    expect(loadSettings(file, defaults).zoomLevel).toBe(5)
    // The updates told (#290) round-trip; an earlier build's file has told none.
    saveSettings(file, { ...defaults, updateAnnounced: '0.7.0-rc.12', updateDownloaded: '0.7.0-rc.13', updateFailedNoticed: '0.7.0-rc.11' })
    expect(loadSettings(file, defaults)).toMatchObject({ updateAnnounced: '0.7.0-rc.12', updateDownloaded: '0.7.0-rc.13', updateFailedNoticed: '0.7.0-rc.11' })
    writeFileSync(file, JSON.stringify({ closeToTray: false, updateAnnounced: 7 }))
    expect(loadSettings(file, defaults)).toMatchObject({ updateAnnounced: '', updateDownloaded: '', updateFailedNoticed: '' })
  })
  it('knows which changes restart the server', () => {
    expect(serverAffecting(defaults, { ...defaults, closeToTray: false })).toBe(false)
    expect(serverAffecting(defaults, { ...defaults, yolo: true })).toBe(true)
    expect(serverAffecting(defaults, { ...defaults, allowedRoots: ['/x'] })).toBe(true)
  })
})

describe('server environment', () => {
  it.skipIf(win)('drops the app\'s CONDUCTOR_* variables, sets the settings\' and the token, and the login shell\'s PATH', () => {
    const defaults = defaultSettings('/home/me', '/ud')
    const env = serverEnv({ HOME: '/home/me', PATH: '/usr/bin', CONDUCTOR_WORKBENCH_TOKEN: 'leaked', CONDUCTOR_YOLO: '1', CONDUCTOR_DESKTOP_DIR: '/elsewhere', LANG: 'C' }, { ...defaults, yolo: true, reach: 'manual' }, 'tok', '/opt/bin:/usr/bin', '/ud')
    expect(env).toEqual({ HOME: '/home/me', PATH: '/opt/bin:/usr/bin', LANG: 'C', CONDUCTOR_WORKBENCH_TOKEN: 'tok', CONDUCTOR_RENDEZVOUS: '1', CONDUCTOR_DATA_DIR: '/ud/conductor', CONDUCTOR_DESKTOP_DIR: '/ud', CONDUCTOR_ALLOWED_ROOTS: '/home/me', CONDUCTOR_DEFAULT_CWD: '/home/me', CONDUCTOR_YOLO: '1', CONDUCTOR_REACH: 'manual', CONDUCTOR_PATHS_BROWSE: 'any' })
    expect(SERVE_ARGS).toEqual(['serve', '--listen', '127.0.0.1:0', '--print-listen', '--exit-on-stdin-close'])
  })
  it('names the app\'s own directory, where its settings and their switchyard token are, for the server to refuse', () => {
    const defaults = defaultSettings('/home/me', '/home/me/.config/conductor-desktop')
    expect(serverEnv({}, defaults, 'tok', '/usr/bin', '/home/me/.config/conductor-desktop').CONDUCTOR_DESKTOP_DIR).toBe('/home/me/.config/conductor-desktop')
    expect(serverEnv({}, defaults, 'tok', '/usr/bin', 'C:\\Users\\me\\AppData\\Roaming\\conductor-desktop').CONDUCTOR_DESKTOP_DIR).toBe('C:\\Users\\me\\AppData\\Roaming\\conductor-desktop')
    // None known: none named, and none the app itself was started with comes through.
    expect(serverEnv({ CONDUCTOR_DESKTOP_DIR: '/elsewhere' }, defaults, 'tok', '/usr/bin', '')).not.toHaveProperty('CONDUCTOR_DESKTOP_DIR')
  })
})

describe('switchyard settings', () => {
  it('publishes by default, takes an optional token, and passes it all to the server as its rendezvous', async () => {
    const { defaultSettings, validate, serverAffecting } = await import('../src/settings')
    const { serverEnv } = await import('../src/env')
    const base = defaultSettings('/home/me', '/home/me/.config/conductor')
    expect(base.switchyardEnabled).toBe(true)
    expect(validate(base)).toEqual([])
    expect(validate({ ...base, switchyardServer: 'ftp://x' })).toContain('the switchyard must be an http(s) URL with a host')
    expect(validate({ ...base, switchyardServer: 'https://switchyard.example.net' })).toEqual([]) // no token needed
    expect(validate({ ...base, switchyardName: 'x'.repeat(65) })).toContain('the name at the switchyard is at most 64 characters')
    const byDefault = serverEnv({}, base, 'tok', '/usr/bin', '')
    expect(byDefault.CONDUCTOR_RENDEZVOUS).toBe('1')
    expect(byDefault.CONDUCTOR_RENDEZVOUS_SERVER).toBeUndefined()
    expect(byDefault.CONDUCTOR_RENDEZVOUS_TOKEN).toBeUndefined()
    const env = serverEnv({}, { ...base, switchyardServer: 'https://switchyard.example.net/', switchyardToken: 'sy-token', switchyardName: 'laptop' }, 'tok', '/usr/bin', '')
    expect(env.CONDUCTOR_RENDEZVOUS_SERVER).toBe('https://switchyard.example.net')
    expect(env.CONDUCTOR_RENDEZVOUS_TOKEN).toBe('sy-token')
    expect(env.CONDUCTOR_RENDEZVOUS_HOST_NAME).toBe('laptop')
    expect(serverEnv({}, { ...base, switchyardEnabled: false }, 'tok', '/usr/bin', '').CONDUCTOR_RENDEZVOUS).toBe('0')
    expect(serverAffecting(base, { ...base, switchyardEnabled: false })).toBe(true)
    expect(serverAffecting(base, { ...base, switchyardServer: 'https://switchyard.example.net', switchyardToken: 't' })).toBe(true)
  })
  it.skipIf(win)('keeps the switchyard settings across a restart', () => {
    const dir = mkdtempSync(join(tmpdir(), 'cd-settings-sy-'))
    const file = join(dir, 'settings.json')
    const s = { ...defaultSettings('/home/me', '/home/me/.config/conductor'), switchyardEnabled: false, switchyardServer: 'https://switchyard.example.net', switchyardToken: 'sy-token', switchyardName: 'laptop' }
    saveSettings(file, s)
    expect(loadSettings(file, defaultSettings('/home/me', '/home/me/.config/conductor'))).toEqual(s)
  })
})

describe('a plain switchyard', () => {
  const base = defaultSettings('/home/me', '/home/me/.config/conductor')
  const words = 'the switchyard must be https:// unless it runs on this machine (localhost): over plain http:// what is published there travels in the clear and unverified'
  it('is refused on another machine, with the words why', () => {
    expect(PLAIN_SWITCHYARD).toBe(words)
    for (const server of ['http://yard.example:8080', 'http://192.168.1.10:8080', 'HTTP://yard.example', 'http://localhost.yard.example', 'http://127.0.0.1.yard.example', 'http://127.1:8080', 'http://0x7f.0.0.1', 'http://127.000.0.1', 'http://0.0.0.0:8080', 'http://[fe80::1]:8080', 'http://LOCALHOST:8080', 'http://LocalHost:8080/']) {
      expect(plainSwitchyard(server), server).toBe(true)
      expect(validate({ ...base, switchyardServer: server }), server).toEqual([words])
    }
  })
  it('is kept on this machine, and an https one anywhere', () => {
    for (const server of ['http://localhost:8080', 'http://localhost:8080/', 'http://127.0.0.1:8080', 'http://127.20.30.40', 'http://[::1]:8080', 'http://[::ffff:127.0.0.1]:8080', 'https://yard.example', 'https://192.168.1.10:8443']) {
      expect(plainSwitchyard(server), server).toBe(false)
      expect(validate({ ...base, switchyardServer: server }), server).toEqual([])
    }
    // Off, nothing is published there: no refusal, and nothing handed to the server.
    expect(validate({ ...base, switchyardEnabled: false, switchyardServer: 'http://yard.example' })).toEqual([])
    // A malformed one is the shape check's.
    expect(validate({ ...base, switchyardServer: 'http://' })).toEqual(['the switchyard must be an http(s) URL with a host'])
  })
  it('saved before it was refused, is kept, never handed to the server, and the server publishes nowhere', () => {
    const s = { ...base, switchyardServer: 'http://yard.example:8080', yolo: true }
    expect(switchyardRefused(s)).toBe(true)
    expect(switchyardRefused({ ...s, switchyardEnabled: false })).toBe(false)
    const env = serverEnv({}, s, 'tok', '/usr/bin', '')
    expect(env.CONDUCTOR_RENDEZVOUS).toBe('0')
    expect(env).not.toHaveProperty('CONDUCTOR_RENDEZVOUS_SERVER')
    expect(serverEnv({}, { ...s, switchyardEnabled: false }, 'tok', '/usr/bin', '')).not.toHaveProperty('CONDUCTOR_RENDEZVOUS_SERVER')
    // On this machine it is handed over as before.
    const here = serverEnv({}, { ...s, switchyardServer: 'http://localhost:9090/' }, 'tok', '/usr/bin', '')
    expect(here.CONDUCTOR_RENDEZVOUS).toBe('1')
    expect(here.CONDUCTOR_RENDEZVOUS_SERVER).toBe('http://localhost:9090')
  })
  it.skipIf(win)('loaded from a file saved before it was refused, keeps every setting, and the file as it was', () => {
    const dir = mkdtempSync(join(tmpdir(), 'cd-settings-plain-'))
    const file = join(dir, 'settings.json')
    const s = { ...base, switchyardServer: 'http://yard.example:8080', yolo: true, allowedRoots: ['/home/me', '/srv'] }
    saveSettings(file, s)
    const before = readFileSync(file, 'utf8')
    // Not the defaults, which would publish to the public switchyard.
    expect(loadSettings(file, base)).toEqual(s)
    expect(readFileSync(file, 'utf8')).toBe(before)
    // Another problem still gives the defaults.
    writeFileSync(file, JSON.stringify({ ...s, defaultCwd: '/elsewhere' }))
    expect(loadSettings(file, base)).toEqual(base)
  })
})

describe('a switchyard named with more than its address', () => {
  const base = defaultSettings('/home/me', '/home/me/.config/conductor')
  const words = 'the switchyard must be named by its address alone, with no user name, password, query or fragment; a host token has a field of its own'
  // As written: the URL parser reads an @ alone, or a : before it, as no user name and no password, and an empty ? or # as none.
  const extra = ['https://me:pw@yard.example', 'https://me@yard.example', 'https://:pw@yard.example', 'https://@yard.example', 'https://:@yard.example', 'https://yard.example/?token=t', 'https://yard.example?', 'https://yard.example/#f', 'https://yard.example#', 'http://me:pw@localhost:9090', 'http://:pw@yard.example', 'http://127.0.0.1\\@yard.example']
  it('is refused while publishing, with the words why, and before the plain http rule', () => {
    expect(SWITCHYARD_EXTRA).toBe(words)
    for (const server of extra) {
      expect(switchyardExtra(server), server).toBe(true)
      expect(validate({ ...base, switchyardServer: server }), server).toEqual([words])
    }
    for (const server of ['https://yard.example', 'https://yard.example/prefix/', 'https://yard.example/team@two', 'http://localhost:9090']) {
      expect(switchyardExtra(server), server).toBe(false)
      expect(validate({ ...base, switchyardServer: server }), server).toEqual([])
    }
    // Off, nothing is published there: no refusal, and nothing handed to the server.
    expect(validate({ ...base, switchyardEnabled: false, switchyardServer: 'https://me:pw@yard.example' })).toEqual([])
  })
  it('is never handed to the server, publishing on or off, and the server publishes nowhere', () => {
    for (const server of extra) {
      const env = serverEnv({}, { ...base, switchyardServer: server }, 'tok', '/usr/bin', '')
      expect(env.CONDUCTOR_RENDEZVOUS, server).toBe('0')
      expect(env, server).not.toHaveProperty('CONDUCTOR_RENDEZVOUS_SERVER')
      expect(serverEnv({}, { ...base, switchyardEnabled: false, switchyardServer: server }, 'tok', '/usr/bin', ''), server).not.toHaveProperty('CONDUCTOR_RENDEZVOUS_SERVER')
    }
  })
  it.skipIf(win)('loaded from a file saved before it was refused, keeps every setting, and the file as it was', () => {
    const dir = mkdtempSync(join(tmpdir(), 'cd-settings-extra-'))
    const file = join(dir, 'settings.json')
    const s = { ...base, switchyardServer: 'https://:pw@yard.example', yolo: true, allowedRoots: ['/home/me', '/srv'] }
    saveSettings(file, s)
    const before = readFileSync(file, 'utf8')
    // Not the defaults, which would publish to the public switchyard.
    const loaded = loadSettings(file, base)
    expect(loaded).toEqual(s)
    expect(serverEnv({}, loaded, 'tok', '/usr/bin', '').CONDUCTOR_RENDEZVOUS).toBe('0')
    expect(readFileSync(file, 'utf8')).toBe(before)
  })
})

describe('settings inside WSL', () => {
  const home = '/home/me'
  it('validates Linux paths whatever the host, and names what is wrong', () => {
    const s = { ...defaultSettings('C:\\Users\\me', 'C:\\Users\\me\\AppData'), dataDir: '/home/me/.local/share/conductor/data', allowedRoots: ['/home/me'], defaultCwd: '/home/me' }
    expect(validate(s, true)).toEqual([])
    expect(validate({ ...s, allowedRoots: ['C:\\Users\\me'], defaultCwd: 'C:\\Users\\me' }, true).join(' ')).toContain('inside the WSL distribution')
  })
  it('moves Windows paths to the defaults inside the distribution, once', () => {
    const win = defaultSettings('C:\\Users\\me', 'C:\\Users\\me\\AppData\\Roaming\\conductor-desktop')
    const m = migrateToWsl(win, home)
    expect(m.changed).toBe(true)
    expect(m.settings).toMatchObject({ dataDir: '/home/me/.local/share/conductor/data', allowedRoots: ['/home/me'], defaultCwd: '/home/me' })
    expect(migrateToWsl(m.settings, home).changed).toBe(false)
    // Linux paths already saved stay; a Windows one among the roots goes.
    const mixed = migrateToWsl({ ...win, dataDir: '/home/me/.conductor', allowedRoots: ['/home/me/code', 'C:\\Users\\me'], defaultCwd: '/home/me/code' }, home)
    expect(mixed.settings).toMatchObject({ dataDir: '/home/me/.conductor', allowedRoots: ['/home/me/code'], defaultCwd: '/home/me/code' })
    // A cwd outside every root after the move takes the first root.
    expect(migrateToWsl({ ...win, allowedRoots: ['/srv'], defaultCwd: 'C:\\x' }, home).settings.defaultCwd).toBe('/srv')
  })
})

describe('the one-time notices', () => {
  const defaults = defaultSettings('/home/me', '/home/me/.config/conductor-desktop')
  it.skipIf(win)('owes a fresh install the publishing notice once, and keeps it marked through the file', () => {
    const dir = mkdtempSync(join(tmpdir(), 'cd-fresh-'))
    const file = join(dir, 'settings.json')
    // No file yet: the defaults, which have been told nothing.
    const fresh = loadSettings(file, defaults)
    expect(fresh.noticed).toEqual([])
    const first = owedNotice(fresh)
    expect(first.notice).toBe('publishing')
    expect(first.settings.noticed).toEqual(['publishing'])
    saveSettings(file, first.settings)
    expect(owedNotice(loadSettings(file, defaults))).toMatchObject({ notice: '' })
  })
  it.skipIf(win)('owes settings from an earlier build the publishing notice once, and keeps it marked through the file', () => {
    const dir = mkdtempSync(join(tmpdir(), 'cd-notice-'))
    const file = join(dir, 'settings.json')
    const { noticed: _drop, ...older } = defaults
    writeFileSync(file, JSON.stringify(older))
    const loaded = loadSettings(file, defaults)
    expect(loaded.noticed).toEqual([])
    const first = owedNotice(loaded)
    expect(first.notice).toBe('publishing')
    saveSettings(file, first.settings)
    expect(owedNotice(loadSettings(file, defaults))).toMatchObject({ notice: '' })
  })
  it.skipIf(win)('marks the notice only when the workbench takes it: a start that never shows the workbench still owes it', () => {
    const dir = mkdtempSync(join(tmpdir(), 'cd-taken-'))
    const file = join(dir, 'settings.json')
    const start = () => {
      let s = loadSettings(file, defaults)
      return noticeOnce(
        () => s,
        (next) => {
          s = next
          saveSettings(file, next)
        },
      )
    }
    // A first start that never asks (setup shown instead of the workbench): nothing marked, nothing written.
    start()
    expect(existsSync(file)).toBe(false)
    // The next start's workbench asks: the notice, once, and marked.
    const take = start()
    expect(take()).toBe('publishing')
    expect(take()).toBe('')
    expect(loadSettings(file, defaults).noticed).toEqual(['publishing'])
    expect(start()()).toBe('')
  })
  it('says nothing to someone who turned publishing off, and marks it all the same', () => {
    const off = owedNotice({ ...defaults, noticed: [], switchyardEnabled: false })
    expect(off.notice).toBe('')
    expect(off.settings.noticed).toEqual(['publishing'])
  })
})
