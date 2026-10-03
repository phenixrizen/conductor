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

/**
 * wslNetworkingMode reads .wslconfig: '' or 'nat' (the default) puts the distribution behind Hyper-V's NAT, which the app's UDP
 * forwarder carries ICE through; 'mirrored' shares Windows' network stack, so nothing is forwarded. The app never asks for mirrored
 * mode: it changes WSL for every other tool.
 */
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
  private home = ''

  constructor(private o: WslLauncherOptions) {}

  describe(): string {
    return `in WSL (${this.o.distro})`
  }

  async prepare(): Promise<string> {
    const run = this.o.run ?? runWsl
    const home = await run(['-d', this.o.distro, '--exec', 'sh', '-lc', 'printf "__HOME__%s\n" "$HOME"'], 30_000, this.o.exe)
    const m = /__HOME__(\/\S*)/.exec(home.out)
    if (!home.ok || !m) throw new Error(`could not read the home directory inside ${this.o.distro}: ${home.out.trim()}`)
    this.home = m[1]!
    const src = windowsPathToWsl(this.o.source)
    const r = await run(['-d', this.o.distro, '--exec', 'sh', '-c', INSTALL_SCRIPT, 'conductor-install', src, this.o.version], 120_000, this.o.exe)
    if (!r.ok) throw new Error(`could not install the server into ${this.o.distro}: ${r.out.trim()}`)
    this.bin = `${this.home}/.local/share/conductor/bin/conductor`
    this.o.log(`server installed in ${this.o.distro} at ${this.bin}`)
    return this.bin
  }

  /** linuxHome is the distribution's home directory, known after prepare. */
  linuxHome(): string {
    return this.home
  }

  /**
   * address is the distribution's IPv4 address on Hyper-V's NAT (`hostname -I`, the first address), where the UDP forwarder sends
   * ICE; it changes when WSL restarts, so it is read after each start of the server. Empty when the distribution cannot say.
   */
  async address(): Promise<string> {
    const run = this.o.run ?? runWsl
    const r = await run(['-d', this.o.distro, '--exec', 'hostname', '-I'], 15_000, this.o.exe)
    const m = /(\d{1,3}(?:\.\d{1,3}){3})/.exec(r.out)
    return r.ok && m ? m[1]! : ''
  }

  /**
   * linuxEnv rewrites the paths the server gets to the distribution's own: its data beside its binary, its home as the root; with
   * `ice`, the server puts every WebRTC connection on that UDP port and advertises the Windows address, the forwarder's.
   */
  linuxEnv(env: Record<string, string>, windowsHome: boolean, userProfile: string, ice?: { port: number; publicIp: string }): Record<string, string> {
    const home = this.home || '$HOME'
    const out: Record<string, string> = { ...env, CONDUCTOR_DATA_DIR: `${home}/.local/share/conductor/data`, CONDUCTOR_ALLOWED_ROOTS: WslLauncher.linuxRoots(windowsHome, userProfile, home).join(','), CONDUCTOR_DEFAULT_CWD: home }
    if (ice && ice.port > 0 && ice.publicIp) {
      out.CONDUCTOR_ICE_UDP_PORT = String(ice.port)
      out.CONDUCTOR_ICE_PUBLIC_IP = ice.publicIp
    }
    return out
  }

  spawn(args: string[], env: Record<string, string>): ChildProcess {
    // The variables the server needs cross into WSL through WSLENV (the
    // Linux-side paths are the distribution's own); the rest of the
    // environment is the login shell's.
    const names = Object.keys(env).filter((k) => k.startsWith('CONDUCTOR_'))
    const winEnv: Record<string, string> = { ...process.env } as Record<string, string>
    for (const k of names) winEnv[k] = env[k]!
    winEnv.WSLENV = [...new Set([...(process.env.WSLENV ?? '').split(':').filter(Boolean), ...names])].join(':')
    // The login shell expands the $HOME the Linux-side paths are written
    // with (main.ts), then runs the server; the arguments are positional,
    // never part of the script.
    const script = 'for v in CONDUCTOR_DATA_DIR CONDUCTOR_ALLOWED_ROOTS CONDUCTOR_DEFAULT_CWD; do eval "val=\$$v"; case "$val" in *\$HOME*) eval "export $v=\"$(printf %s "$val" | sed "s|\\$HOME|$HOME|g")\"";; esac; done; exec "$HOME/.local/share/conductor/bin/conductor" "$@"'
    const argv = ['-d', this.o.distro, '--cd', '~', '--exec', 'sh', '-lc', script, 'conductor', ...args]
    const child = spawn(this.o.exe ?? 'wsl.exe', argv, { env: winEnv, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true })
    return child
  }

  kill(_child: ChildProcess, pid: number | undefined): void {
    const run = this.o.run ?? runWsl
    if (pid) void run(['-d', this.o.distro, '--exec', 'kill', '-KILL', String(pid)], 10_000, this.o.exe)
  }

  /** linuxRoots are the allowed roots inside the distribution: the Linux home, and the Windows profile under /mnt when asked. */
  static linuxRoots(windowsHome: boolean, userProfile: string, home = '$HOME'): string[] {
    const roots = [home]
    if (windowsHome && userProfile) roots.push(windowsPathToWsl(userProfile))
    return roots
  }
}
