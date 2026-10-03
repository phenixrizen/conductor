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
      return { ok: true, out: '' }
    }
    expect(await wslAvailable('', fake)).toEqual({ ok: true, distro: 'Ubuntu-24.04' })
    expect(await wslAvailable('', async () => ({ ok: false, out: '' }))).toMatchObject({ ok: false, reason: expect.stringContaining('not installed') })
    const l = new WslLauncher({ source: 'C:\\Program Files\\Conductor\\resources\\conductor-linux', distro: 'Ubuntu-24.04', version: 'v1', windowsHome: false, log: () => {}, run: fake })
    await l.prepare()
    const install = calls[calls.length - 1]!
    expect(install.slice(0, 5)).toEqual(['-d', 'Ubuntu-24.04', '--exec', 'sh', '-c'])
    expect(install[install.length - 2]).toBe('/mnt/c/Program Files/Conductor/resources/conductor-linux')
    expect(WslLauncher.linuxRoots(true, 'C:\\Users\\me')).toEqual(['$HOME', '/mnt/c/Users/me'])
  })
})
