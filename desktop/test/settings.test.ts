import { mkdtempSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { defaultSettings, loadSettings, migrateToWsl, owedNotice, saveSettings, serverAffecting, validate } from '../src/settings'
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
    const s = { ...defaults, yolo: true, reach: 'off' as const, allowedRoots: ['/home/me', '/srv'] }
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
    const env = serverEnv({ HOME: '/home/me', PATH: '/usr/bin', CONDUCTOR_WORKBENCH_TOKEN: 'leaked', CONDUCTOR_YOLO: '1', LANG: 'C' }, { ...defaults, yolo: true, reach: 'manual' }, 'tok', '/opt/bin:/usr/bin')
    expect(env).toEqual({ HOME: '/home/me', PATH: '/opt/bin:/usr/bin', LANG: 'C', CONDUCTOR_WORKBENCH_TOKEN: 'tok', CONDUCTOR_RENDEZVOUS: '1', CONDUCTOR_DATA_DIR: '/ud/conductor', CONDUCTOR_ALLOWED_ROOTS: '/home/me', CONDUCTOR_DEFAULT_CWD: '/home/me', CONDUCTOR_YOLO: '1', CONDUCTOR_REACH: 'manual', CONDUCTOR_PATHS_BROWSE: 'any' })
    expect(SERVE_ARGS).toEqual(['serve', '--listen', '127.0.0.1:0', '--print-listen', '--exit-on-stdin-close'])
  })
})

describe('switchyard settings', () => {
  it('publishes by default, takes an optional token, and passes it all to the server as its rendezvous', async () => {
    const { defaultSettings, validate, serverAffecting } = await import('../src/settings')
    const { serverEnv } = await import('../src/env')
    const base = defaultSettings('/home/me', '/home/me/.config/conductor')
    expect(base.switchyardEnabled).toBe(true)
    expect(validate(base)).toEqual([])
    expect(validate({ ...base, switchyardServer: 'ftp://x' })).toContain('the switchyard must be an http(s) URL with a host and nothing after it')
    expect(validate({ ...base, switchyardServer: 'https://switchyard.example.net' })).toEqual([]) // no token needed
    expect(validate({ ...base, switchyardName: 'x'.repeat(65) })).toContain('the name at the switchyard is at most 64 characters')
    const byDefault = serverEnv({}, base, 'tok', '/usr/bin')
    expect(byDefault.CONDUCTOR_RENDEZVOUS).toBe('1')
    expect(byDefault.CONDUCTOR_RENDEZVOUS_SERVER).toBeUndefined()
    expect(byDefault.CONDUCTOR_RENDEZVOUS_TOKEN).toBeUndefined()
    const env = serverEnv({}, { ...base, switchyardServer: 'https://switchyard.example.net/', switchyardToken: 'sy-token', switchyardName: 'laptop' }, 'tok', '/usr/bin')
    expect(env.CONDUCTOR_RENDEZVOUS_SERVER).toBe('https://switchyard.example.net')
    expect(env.CONDUCTOR_RENDEZVOUS_TOKEN).toBe('sy-token')
    expect(env.CONDUCTOR_RENDEZVOUS_HOST_NAME).toBe('laptop')
    expect(serverEnv({}, { ...base, switchyardEnabled: false }, 'tok', '/usr/bin').CONDUCTOR_RENDEZVOUS).toBe('0')
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
  it('owes a fresh install nothing', () => {
    expect(owedNotice(defaults)).toEqual({ notice: '', settings: defaults })
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
  it('says nothing to someone who turned publishing off, and marks it all the same', () => {
    const off = owedNotice({ ...defaults, noticed: [], switchyardEnabled: false })
    expect(off.notice).toBe('')
    expect(off.settings.noticed).toEqual(['publishing'])
  })
})
