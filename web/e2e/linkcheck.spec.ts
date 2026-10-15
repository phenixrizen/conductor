import { spawn, type ChildProcess } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { mkdirSync, openSync, readFileSync, writeFileSync } from 'node:fs'
import { connect, createServer, type Server, type Socket } from 'node:net'
import { join } from 'node:path'
import { endHostedSession, expect, hostedSession, test } from './fixtures'
import { serverBinary } from './server'

// A guest on a control link of a session another machine hosts (`conductor
// host`), its editor on a data channel straight to the host: what the guest
// saves passes the link's check on the host, which needs the server heard
// from within 15 s to tell that the link still stands. While the host's
// connection to the server carries nothing (both ends still open), the
// guest's save is refused and the editor shows the words it is sent, the
// file as it was; once the connection carries again, the save goes through.
let host: ChildProcess | null = null
let proxy: StallProxy | null = null
let id = ''

test.afterAll(async ({ api }) => {
  try {
    // The hosted session ends through its host and is removed before the host dies, so that no later spec or repetition meets it:
    // the host must hear the server for that, so a proxy left stalled (the test failed in its stall) carries again first.
    proxy?.resume()
    if (id) await endHostedSession(api, id)
  } finally {
    host?.kill('SIGKILL')
    proxy?.close()
  }
})

/** A TCP proxy to the server that can stop carrying anything either way, closing nothing, and carry again. */
class StallProxy {
  readonly server: Server
  private readonly pairs: Array<[Socket, Socket]> = []
  private stalled = false

  constructor(port: number) {
    this.server = createServer((client) => {
      const upstream = connect(port, '127.0.0.1')
      const end = () => {
        client.destroy()
        upstream.destroy()
      }
      client.on('error', end)
      upstream.on('error', end)
      client.on('close', end)
      upstream.on('close', end)
      this.pairs.push([client, upstream])
      if (!this.stalled) this.carry(client, upstream)
    })
  }

  listen(): Promise<number> {
    return new Promise((resolve) => this.server.listen(0, '127.0.0.1', () => resolve((this.server.address() as { port: number }).port)))
  }

  private carry(a: Socket, b: Socket) {
    a.pipe(b)
    b.pipe(a)
  }

  stall() {
    this.stalled = true
    for (const [a, b] of this.pairs) {
      a.unpipe(b)
      b.unpipe(a)
      a.pause()
      b.pause()
    }
  }

  /** Carries again; once only, since a second pipe would send everything twice. */
  resume() {
    if (!this.stalled) return
    this.stalled = false
    for (const [a, b] of this.pairs) this.carry(a, b)
  }

  close() {
    for (const [a, b] of this.pairs) {
      a.destroy()
      b.destroy()
    }
    this.server.close()
  }
}

test("a link guest's save on a hosted session is refused with the words while the host cannot hear its server, and saves once it can", async ({ api, state, browser }) => {
  test.setTimeout(150_000)
  const cwd = join(state.root, 'hosted-linkcheck')
  mkdirSync(cwd, { recursive: true })
  const file = join(cwd, 'notes.txt')
  writeFileSync(file, 'alpha\n')
  proxy = new StallProxy(Number(new URL(state.baseURL).port))
  const proxyPort = await proxy.listen()
  // This run's own host log: the session is found by the id on the line the host prints once registered, never by its name, which
  // an earlier run's (a repetition's, a parallel run's) has too (#217).
  const logFile = join(state.root, `hosted-linkcheck-${randomUUID()}.log`)
  const log = openSync(logFile, 'a')
  host = spawn(serverBinary(), ['host', '--server', `http://127.0.0.1:${proxyPort}`, '--token', state.hostToken, '--host-name', 'e2e-desk', '--name', 'hosted-linkcheck', '--cwd', cwd, '--no-local', '--', '/bin/cat'], {
    env: { PATH: process.env.PATH ?? '/usr/bin:/bin', LANG: 'C.UTF-8', HOME: state.home },
    stdio: ['ignore', log, log],
  })
  id = (await hostedSession(api, logFile, 'hosted-linkcheck')).id
  const { token } = await api.ok<{ token: string }>('POST', `/api/sessions/${id}/links`, { role: 'control', label: 'guest' })

  const guestCtx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Priya'))
  const guest = await guestCtx.newPage()
  await guest.goto(`/join/${token}`)
  await guest.getByRole('button', { name: 'Join session' }).click()
  const badge = guest.locator('[data-transport-state]').first()
  await expect(badge).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
  // The data channel: what the guest sends reaches the host without the server.
  await expect(badge).toHaveAttribute('data-transport-kind', 'webrtc', { timeout: 30_000 })

  const aside = guest.locator('[data-files-aside]')
  await expect(aside.locator(`[data-file-node="${file}"]`)).toBeVisible({ timeout: 30_000 })
  await aside.locator(`[data-file-node="${file}"]`).click()
  const area = guest.locator('[data-editor-area]')
  const lines = area.locator('.monaco-editor .view-lines')
  await expect(lines).toContainText('alpha', { timeout: 30_000 })
  await lines.click()
  await guest.keyboard.press('Control+End')
  await guest.keyboard.type('beta\n')
  await expect(area.locator('[data-editor-dirty]')).toHaveCount(1)

  // The host's connection to the server carries nothing from now on; past
  // the host's 15 s it no longer vouches for the link.
  proxy.stall()
  await guest.waitForTimeout(17_000)
  await expect(badge).toHaveAttribute('data-transport-state', 'open')
  await guest.keyboard.press('Control+s')
  const refused = area.locator('[data-editor-save-error]')
  await expect(refused).toContainText('has not been heard from', { timeout: 15_000 })
  await expect(area.locator('[data-editor-saved]')).toHaveCount(0)
  await expect(area.locator('[data-editor-dirty]')).toHaveCount(1)
  expect(readFileSync(file, 'utf8')).toBe('alpha\n')

  // Carried again: the host hears the server, and the save goes through.
  proxy.resume()
  await expect
    .poll(
      async () => {
        await lines.click()
        await guest.keyboard.press('Control+s')
        await guest.waitForTimeout(1_000)
        return readFileSync(file, 'utf8')
      },
      { timeout: 30_000, message: 'the save once the server is heard' },
    )
    .toBe('alpha\nbeta\n')
  await expect(area.locator('[data-editor-dirty]')).toHaveCount(0, { timeout: 15_000 })
  await expect(refused).toHaveCount(0)
  await guestCtx.close()
})
