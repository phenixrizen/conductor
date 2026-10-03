import { spawn, type ChildProcess, type SpawnOptions } from 'node:child_process'

/** A Launcher starts the server binary: natively, or inside WSL on Windows. */
export interface Launcher {
  /** describe says where the server runs, for the log and the settings page. */
  describe(): string
  /** prepare puts the binary where it runs from and returns that path (or a token the launcher understands). */
  prepare(): Promise<string>
  /** spawn starts `conductor <args>` with env; stdout carries the handshake, stdin's end stops the server. */
  spawn(args: string[], env: Record<string, string>): ChildProcess
  /** kill ends a server that did not stop after its stdin closed. */
  kill(child: ChildProcess, pid: number | undefined): void
}

export interface NativeLauncherOptions {
  /** The bundled binary (or the dev build). */
  source: string
  /** Where to run it from (install.ts); '' runs the source itself (dev). */
  stable: string
  version: string
  ensureStable: (src: string, dst: string, version: string) => boolean
  log: (line: string) => void
}

/** NativeLauncher runs the binary on this machine (macOS, Linux). */
export class NativeLauncher implements Launcher {
  private bin = ''

  constructor(private o: NativeLauncherOptions) {}

  describe(): string {
    return `on this machine (${this.bin || this.o.source})`
  }

  async prepare(): Promise<string> {
    if (!this.o.stable) {
      this.bin = this.o.source
      return this.bin
    }
    if (this.o.ensureStable(this.o.source, this.o.stable, this.o.version)) this.o.log(`copied the server to ${this.o.stable}`)
    this.bin = this.o.stable
    return this.bin
  }

  spawn(args: string[], env: Record<string, string>): ChildProcess {
    const opts: SpawnOptions = { env, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true }
    return spawn(this.bin, args, opts)
  }

  kill(child: ChildProcess): void {
    try {
      child.kill('SIGKILL')
    } catch {
      /* gone already */
    }
  }
}
