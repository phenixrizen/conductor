import { spawn, type ChildProcess } from 'node:child_process'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import type { Launcher } from '../src/launcher'
import { ServerSupervisor } from '../src/server'

/** A fake server: a node script that prints the handshake (after optional noise), serves /api/health, and exits when stdin closes. */
function fakeServer(opts: { noise?: boolean; crashAfterMs?: number; silent?: boolean }): string {
  const dir = mkdtempSync(join(tmpdir(), 'cd-fake-'))
  const file = join(dir, 'server.js')
  writeFileSync(
    file,
    `
const http = require('http')
const srv = http.createServer((req, res) => { res.end(JSON.stringify({ ok: true, instance: 'x' })) })
srv.listen(0, '127.0.0.1', () => {
  const port = srv.address().port
  if (${!!opts.noise}) process.stdout.write('Welcome to the shell\\n')
  if (!${!!opts.silent}) process.stdout.write(JSON.stringify({ listen: '127.0.0.1:' + port, publicUrl: 'http://127.0.0.1:' + port, pid: process.pid, version: 'fake', adminToken: process.env.CONDUCTOR_ADMIN_TOKEN }) + '\\n')
  process.stderr.write('serving\\n')
  if (${opts.crashAfterMs ?? 0}) setTimeout(() => process.exit(3), ${opts.crashAfterMs ?? 0})
})
process.stdin.on('end', () => { srv.close(); process.exit(0) })
process.stdin.resume()
`,
  )
  return file
}

class ScriptLauncher implements Launcher {
  spawned = 0
  constructor(private script: string) {}
  describe() {
    return 'fake'
  }
  prepare() {
    return Promise.resolve(this.script)
  }
  spawn(args: string[], env: Record<string, string>): ChildProcess {
    this.spawned++
    return spawn(process.execPath, [this.script, ...args], { env, stdio: ['pipe', 'pipe', 'pipe'] })
  }
  kill(child: ChildProcess) {
    child.kill('SIGKILL')
  }
}

const quiet = () => {}

describe('server supervisor', () => {
  it('starts the server, reads its handshake past the shell\'s noise, and stops it by closing stdin', async () => {
    const launcher = new ScriptLauncher(fakeServer({ noise: true }))
    const lines: string[] = []
    const s = new ServerSupervisor({ launcher, args: ['serve'], env: () => ({ ...process.env, CONDUCTOR_ADMIN_TOKEN: 'tok' } as Record<string, string>), log: (k, l) => lines.push(`${k}: ${l}`) })
    const h = await s.start()
    expect(h.adminToken).toBe('tok')
    expect(s.status.state).toBe('running')
    expect(s.status.url).toBe(h.publicUrl)
    const res = await fetch(h.publicUrl + '/api/health')
    expect(res.ok).toBe(true)
    expect(lines).toContain('server: Welcome to the shell')
    await s.stop()
    expect(s.status.state).toBe('stopped')
    await expect(fetch(h.publicUrl + '/api/health')).rejects.toThrow()
  })
  it('restarts a server that dies, with backoff, and gives up after too many failures', async () => {
    const launcher = new ScriptLauncher(fakeServer({ crashAfterMs: 150 }))
    const states: string[] = []
    const s = new ServerSupervisor({ launcher, args: [], env: () => process.env as Record<string, string>, log: quiet, backoffMinMs: 50, backoffMaxMs: 100, giveUpAfter: 3, healthMs: 10_000 })
    s.on('state', (st) => states.push(st.state))
    const gaveUp = new Promise<void>((resolve) => s.once('gaveUp', () => resolve()))
    await s.start()
    await gaveUp
    expect(launcher.spawned).toBeGreaterThanOrEqual(3)
    expect(s.status.failures).toBe(3)
    expect(states).toContain('failed')
    await s.stop()
  })
  it('reports a server that never listens', async () => {
    const launcher = new ScriptLauncher(fakeServer({ silent: true }))
    const s = new ServerSupervisor({ launcher, args: [], env: () => process.env as Record<string, string>, log: quiet, handshakeMs: 300 })
    await expect(s.start()).rejects.toThrow(/did not report its address/)
    await s.stop()
  })
  it('restart stops and starts again with the environment read afresh', async () => {
    const launcher = new ScriptLauncher(fakeServer({}))
    let token = 'one'
    const s = new ServerSupervisor({ launcher, args: [], env: () => ({ ...process.env, CONDUCTOR_ADMIN_TOKEN: token } as Record<string, string>), log: quiet })
    const first = await s.start()
    token = 'two'
    const second = await s.restart()
    expect(first.adminToken).toBe('one')
    expect(second.adminToken).toBe('two')
    expect(second.pid).not.toBe(first.pid)
    await s.stop()
  })
})
