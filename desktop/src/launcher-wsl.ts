import { execFile, spawn, type ChildProcess } from 'node:child_process'
import { existsSync, readFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { join } from 'node:path'
import type { Launcher } from './launcher'
import { chooseDistro, decode, INSTALL_SCRIPT, parseList, parseWslconfigNetworking, windowsPathToWsl } from './wsl'

/** runWsl runs wsl.exe with args and returns its decoded output (both streams), '' on failure. */
export function runWsl(args: string[], timeoutMs = 20_000, exe = 'wsl.exe'): Promise<{ ok: boolean; out: string }> {
  return new Promise((resolve) => {
    execFile(exe, args, { timeout: timeoutMs, encoding: 'buffer', env: { ...process.env, WSL_UTF8: '1' }, windowsHide: true }, (err, stdout, stderr) => {
      resolve({ ok: !err, out: decode(Buffer.concat([stdout as Buffer, stderr as Buffer])) })
    })
  })
}

/** wslAvailable says whether WSL 2 with a distribution is there, and which one to use. */
export async function wslAvailable(wanted: string, run = runWsl): Promise<{ ok: true; distro: string } | { ok: false; reason: string }> {
  const status = await run(['--status'], 10_000)
  if (!status.ok && !status.out) return { ok: false, reason: 'WSL is not installed (wsl.exe did not answer)' }
  const list = await run(['-l', '-v'], 10_000)
  if (!list.ok && !list.out.trim()) return { ok: false, reason: 'WSL is installed but lists no distribution' }
  return chooseDistro(parseList(list.out), wanted)
}

/** wslNetworkingMode reads .wslconfig: 'mirrored' lets the server map ports on the router; '' or 'nat' does not. */
export function wslNetworkingMode(home = homedir()): string {
  try {
    return parseWslconfigNetworking(readFileSync(join(home, '.wslconfig'), 'utf8'))
  } catch {
    return ''
  }
}

export interface WslLauncherOptions {
  /** The bundled Linux binary, a Windows path. */
  source: string
  distro: string
  version: string
  windowsHome: boolean
  log: (line: string) => void
  exe?: string
  run?: typeof runWsl
}

/**
 * WslLauncher runs the Linux server inside a WSL 2 distribution: the binary copied into the distribution (never run from /mnt/c),
 * started through the user's login shell so the agents' PATH is theirs, its CONDUCTOR_* variables carried by WSLENV, its handshake
 * read from stdout as on any platform, and stopped by its stdin closing (then `kill` inside the distribution, never `wsl --terminate`,
 * which would take the person's other shells with it).
 */
export class WslLauncher implements Launcher {
  private bin = '$HOME/.local/share/conductor/bin/conductor'
  private pid: number | undefined

  constructor(private o: WslLauncherOptions) {}

  describe(): string {
    return `in WSL (${this.o.distro})`
  }

  async prepare(): Promise<string> {
    const run = this.o.run ?? runWsl
    const src = windowsPathToWsl(this.o.source)
    const r = await run(['-d', this.o.distro, '--exec', 'sh', '-c', INSTALL_SCRIPT, 'conductor-install', src, this.o.version], 120_000, this.o.exe)
    if (!r.ok) throw new Error(`could not install the server into ${this.o.distro}: ${r.out.trim()}`)
    this.o.log(`server installed in ${this.o.distro} at ${this.bin}`)
    return this.bin
  }

  spawn(args: string[], env: Record<string, string>): ChildProcess {
    // The variables the server needs cross into WSL through WSLENV (the
    // Linux-side paths are the distribution's own); the rest of the
    // environment is the login shell's.
    const names = Object.keys(env).filter((k) => k.startsWith('CONDUCTOR_'))
    const winEnv: Record<string, string> = { ...process.env } as Record<string, string>
    for (const k of names) winEnv[k] = env[k]!
    winEnv.WSLENV = [...new Set([...(process.env.WSLENV ?? '').split(':').filter(Boolean), ...names])].join(':')
    const argv = ['-d', this.o.distro, '--cd', '~', '--exec', 'sh', '-lc', 'exec "$HOME/.local/share/conductor/bin/conductor" "$@"', 'conductor', ...args]
    const child = spawn(this.o.exe ?? 'wsl.exe', argv, { env: winEnv, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true })
    return child
  }

  kill(_child: ChildProcess, pid: number | undefined): void {
    const run = this.o.run ?? runWsl
    if (pid) void run(['-d', this.o.distro, '--exec', 'kill', '-KILL', String(pid)], 10_000, this.o.exe)
  }

  /** linuxRoots are the allowed roots inside the distribution: the Linux home, and the Windows profile under /mnt when asked. */
  static linuxRoots(windowsHome: boolean, userProfile: string): string[] {
    const roots = ['$HOME']
    if (windowsHome && userProfile) roots.push(windowsPathToWsl(userProfile))
    return roots
  }
}
