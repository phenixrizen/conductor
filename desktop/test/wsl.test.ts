import { describe, expect, it } from 'vitest'
import { chooseDistro, decode, INSTALL_SCRIPT, parseList, parseWslconfigNetworking, windowsPathToWsl } from '../src/wsl'
import { wslAvailable, WslLauncher } from '../src/launcher-wsl'

const LIST = '  NAME            STATE           VERSION\r\n* Ubuntu-24.04    Running         2\r\n  Debian          Stopped         1\r\n'

describe('wsl', () => {
  it('decodes UTF-16LE and UTF-8 output', () => {
    expect(decode(Buffer.from(LIST, 'utf16le'))).toBe(LIST)
    expect(decode(Buffer.from(LIST, 'utf8'))).toBe(LIST)
  })
  it('lists the distributions with the default marked', () => {
    expect(parseList(LIST)).toEqual([
      { name: 'Ubuntu-24.04', default: true, state: 'Running', version: 2 },
      { name: 'Debian', default: false, state: 'Stopped', version: 1 },
    ])
  })
  it('chooses the named or default WSL 2 distribution and names what is wrong', () => {
    const list = parseList(LIST)
    expect(chooseDistro(list, '')).toEqual({ ok: true, distro: 'Ubuntu-24.04' })
    expect(chooseDistro(list, 'Debian')).toMatchObject({ ok: false, reason: expect.stringContaining('WSL 1') })
    expect(chooseDistro(list, 'Arch')).toMatchObject({ ok: false, reason: expect.stringContaining('not installed') })
    expect(chooseDistro([], '')).toMatchObject({ ok: false })
  })
  it('turns Windows paths into /mnt paths and reads .wslconfig', () => {
    expect(windowsPathToWsl('C:\\Users\\me\\Conductor')).toBe('/mnt/c/Users/me/Conductor')
    expect(windowsPathToWsl('/already/linux')).toBe('/already/linux')
    expect(parseWslconfigNetworking('[wsl2]\nmemory=8GB\nnetworkingMode=mirrored\n')).toBe('mirrored')
    expect(parseWslconfigNetworking('[experimental]\nnetworkingMode=mirrored\n')).toBe('')
    expect(INSTALL_SCRIPT).toContain('.local/share/conductor/bin')
    expect(INSTALL_SCRIPT).not.toContain('${')
  })
  it('detects WSL through a fake wsl.exe runner', async () => {
    const calls: string[][] = []
    const fake = async (args: string[]) => {
      calls.push(args)
      if (args[0] === '--status') return { ok: true, out: 'Default Version: 2' }
      if (args[0] === '-l') return { ok: true, out: LIST }
      if (args.some((a) => a.includes('__HOME__'))) return { ok: true, out: 'motd\n__HOME__/home/me\n' }
      return { ok: true, out: '' }
    }
    expect(await wslAvailable('', fake)).toEqual({ ok: true, distro: 'Ubuntu-24.04' })
    expect(await wslAvailable('', async () => ({ ok: false, out: '' }))).toMatchObject({ ok: false, reason: expect.stringContaining('not installed') })
    const l = new WslLauncher({ source: 'C:\\Program Files\\Conductor\\resources\\conductor-linux', distro: 'Ubuntu-24.04', version: 'v1', windowsHome: false, log: () => {}, run: fake })
    await l.prepare()
    const install = calls[calls.length - 1]!
    expect(install.slice(0, 5)).toEqual(['-d', 'Ubuntu-24.04', '--exec', 'sh', '-c'])
    expect(install[install.length - 2]).toBe('/mnt/c/Program Files/Conductor/resources/conductor-linux')
    expect(l.linuxHome()).toBe('/home/me')
    expect(l.linuxEnv({ CONDUCTOR_ADMIN_TOKEN: 't', CONDUCTOR_DATA_DIR: 'C:\\x' }, true, 'C:\\Users\\me')).toEqual({ CONDUCTOR_ADMIN_TOKEN: 't', CONDUCTOR_DATA_DIR: '/home/me/.local/share/conductor/data', CONDUCTOR_ALLOWED_ROOTS: '/home/me,/mnt/c/Users/me', CONDUCTOR_DEFAULT_CWD: '/home/me' })
    expect(WslLauncher.linuxRoots(true, 'C:\\Users\\me')).toEqual(['$HOME', '/mnt/c/Users/me'])
  })
})

describe('WebRTC from inside WSL', () => {
  const fake = async (args: string[]) => {
    if (args.includes('hostname')) return { ok: true, out: '172.26.16.42 fe80::1 \n' }
    return { ok: true, out: '' }
  }

  it('reads the distribution address after a start, and passes the ICE variables only when forwarding', async () => {
    const l = new WslLauncher({ source: 'C:\\x\\conductor-linux', distro: 'Ubuntu', version: 'v1', windowsHome: false, log: () => {}, run: fake })
    expect(await l.address()).toBe('172.26.16.42')
    const none = new WslLauncher({ source: 'C:\\x\\conductor-linux', distro: 'Ubuntu', version: 'v1', windowsHome: false, log: () => {}, run: async () => ({ ok: false, out: '' }) })
    expect(await none.address()).toBe('')
    const env = l.linuxEnv({ CONDUCTOR_ADMIN_TOKEN: 't' }, false, '', { port: 7877, publicIp: '192.168.1.127' })
    expect(env.CONDUCTOR_ICE_UDP_PORT).toBe('7877')
    expect(env.CONDUCTOR_ICE_PUBLIC_IP).toBe('192.168.1.127')
    const plain = l.linuxEnv({ CONDUCTOR_ADMIN_TOKEN: 't' }, false, '')
    expect(plain.CONDUCTOR_ICE_UDP_PORT).toBeUndefined()
    expect(l.linuxEnv({}, false, '', { port: 7877, publicIp: '' }).CONDUCTOR_ICE_PUBLIC_IP).toBeUndefined()
  })

  it('the installer script adds the firewall rule for the ICE port and removes it', async () => {
    const { readFileSync } = await import('node:fs')
    const nsh = readFileSync(new URL('../build/installer.nsh', import.meta.url), 'utf8')
    expect(nsh).toContain('customInstall')
    expect(nsh).toContain('protocol=UDP localport=7877')
    expect(nsh).toContain('customUnInstall')
    expect(nsh).toContain('delete rule name="Conductor WebRTC (UDP 7877)"')
  })
})
