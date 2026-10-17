import { execFileSync, type ExecFileSyncOptions } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderConfig, stubBash, stubPath } from '../e2e/server'

vi.mock('node:child_process', async (original) => ({
  ...await original<typeof import('node:child_process')>(),
  execFileSync: vi.fn(),
}))
const real = await vi.importActual<typeof import('node:child_process')>('node:child_process')
const run = vi.mocked(execFileSync)
const dirs: string[] = []
// System /bin/bash is 3.2 on macOS. Discover an actual modern interpreter for
// the real-process cases; mocked selection/wiring cases always run.
const installed = ['bash', '/opt/homebrew/bin/bash', '/usr/local/bin/bash', join(homedir(), '.local/bin/bash')].flatMap((command) => {
  try {
    const [path, major] = real.execFileSync(command, ['-c', 'printf "%s\\n" "$BASH" "${BASH_VERSINFO[0]}"'], { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] }).trim().split('\n')
    return Number(major) >= 4 ? [{ command: path!, major: Number(major) }] : []
  } catch {
    return []
  }
})
const testBash = installed.find((bash) => bash.major >= 5) ?? installed[0]

beforeEach(() => { run.mockReset() })
afterEach(() => dirs.splice(0).forEach((dir) => rmSync(dir, { recursive: true, force: true })))

// Exercise the production probe in real bash, with only its native clock disabled
// to reach the older-bash fallback. The fake date is isolated from the host's date.
function clockFixture(dateBody: string, native = false): string {
  const dir = mkdtempSync(join(tmpdir(), 'stub-clock-'))
  dirs.push(dir)
  writeFileSync(join(dir, 'date'), `#!/bin/sh\n${dateBody}\n`, { mode: 0o700 })
  run.mockImplementation((_bash, args, options) => real.execFileSync(testBash!.command, [
    '-c', `${native ? '' : 'unset EPOCHREALTIME\n'}${args![1]}`,
  ], options as ExecFileSyncOptions))
  return dir
}

describe('stubBash', () => {
  it.skipIf(!testBash || testBash.major < 5)('accepts the native clock without calling date', () => {
    const path = clockFixture('exit 89', true)
    expect(stubBash(path)).toEqual({ command: testBash!.command, path })
    expect(run).toHaveBeenCalledTimes(1)
  })

  it.skipIf(!testBash)('accepts a numeric GNU-date fallback', () => {
    const path = clockFixture('[ "$1" = "+%s%6N" ] || exit 1\nprintf "%s\\n" 1729190000123456')
    expect(stubBash(path)).toEqual({ command: testBash!.command, path })
  })

  it.skipIf(!testBash).each([
    'printf "%s\\n" 17291900006N',
    'printf "%s\\n" bad',
    'printf "%s\\n" 1729190000',
    'printf "%s\\n" 1729190000123456; exit 1',
  ])('rejects an unusable fallback: %s', (body) => {
    const path = clockFixture(body)
    // Keep every fallback probe isolated from real installed GNU date.
    run.mockImplementation((_bash, args) => real.execFileSync(testBash!.command, [
      '-c', `unset EPOCHREALTIME\n${args![1]}`,
    ], { env: { PATH: path }, encoding: 'utf8' }))
    expect(() => stubBash(path)).toThrow('bash 5 or bash 4 with GNU date')
  })

  it('rejects bash 3 even with a numeric clock, then uses the candidate PATH', () => {
    run.mockImplementation((bash, _args, options) => {
      const path = (options as ExecFileSyncOptions).env!.PATH
      if (bash === 'bash') {
        expect(path).toBe('/test/bin')
        return '/test/bin/bash\n3\n1729190000123456\n'
      }
      expect(bash).toBe('/opt/homebrew/bin/bash')
      expect(path).toBe('/opt/homebrew/bin:/test/bin')
      return '/opt/homebrew/bin/bash\n4\n1729190000123456\n'
    })
    expect(stubBash('/test/bin')).toEqual({ command: '/opt/homebrew/bin/bash', path: '/opt/homebrew/bin:/test/bin' })
    expect(run).toHaveBeenCalledTimes(2)
  })

  it('rejects failed or malformed probes without treating them as capability', () => {
    run.mockImplementationOnce(() => { throw new Error('not installed') })
      .mockReturnValue('/bin/bash\n5\n1729190000123456\nextra\n')
    expect(() => stubBash('/test/bin')).toThrow('numeric microsecond clock')
    expect(run).toHaveBeenCalledTimes(4)
  })
})


describe('renderConfig', () => {
  it('uses the selected executable for every stub and leaves live configs independent', () => {
    const dir = mkdtempSync(join(tmpdir(), 'stub-config-'))
    dirs.push(dir)
    const input = join(dir, 'input.json')
    const output = join(dir, 'output.json')
    writeFileSync(input, JSON.stringify({ catalog: { agents: [
      { command: ['@BASH@', '@STUB@'] }, { command: ['@BASH@', '@STUB@', 'arg'] },
    ] } }))
    run.mockReturnValue('/selected/bash\n5\n1729190000123456\n')
    renderConfig(input, output)
    expect(JSON.parse(readFileSync(output, 'utf8')).catalog.agents).toEqual([
      { command: ['/selected/bash', stubPath] }, { command: ['/selected/bash', stubPath, 'arg'] },
    ])
    expect(run).toHaveBeenCalledTimes(1)
    run.mockClear()
    writeFileSync(input, JSON.stringify({ catalog: { agents: [{ command: ['claude'] }] } }))
    renderConfig(input, output)
    expect(JSON.parse(readFileSync(output, 'utf8')).catalog.agents).toEqual([{ command: ['claude'] }])
    expect(run).not.toHaveBeenCalled()
  })
})
