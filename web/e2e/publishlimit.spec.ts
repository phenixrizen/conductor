import { spawn, type ChildProcess } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { existsSync, mkdirSync, openSync, readFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, test } from './fixtures'
import { renderConfig, serverBinary, startServer, stopServer, type Started } from './server'

// A server publishing to a switchyard that refuses it for now (its 429: here
// open hosts with one live session per address, as a home running a few
// sessions meets the public switchyard's four). Round 14: the refused crew
// member wore a red "error" badge, as if its agent had failed, and was never
// tried again. Now the refused session has no badge, its Activity says it
// is not shared yet and why, and once the first session ends it is shared
// on a later try.
let switchyard: ChildProcess | undefined
let publisher: Started | undefined
const syAdmin = randomBytes(16).toString('hex')

function pickPort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = createServer()
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address()
      srv.close(() => (typeof addr === 'object' && addr ? resolve(addr.port) : reject(new Error('no port'))))
    })
  })
}

test.afterAll(async ({ state }) => {
  if (publisher) await stopServer(publisher.pid)
  switchyard?.kill('SIGKILL')
  const log = join(state.root, 'publishlimit-switchyard.log')
  if (existsSync(log)) console.log('switchyard log:\n' + readFileSync(log, 'utf8').split('\n').slice(-20).join('\n'))
})

test('a session the switchyard refuses for now wears no error, says why, and is shared once a slot frees', async ({ browser, state }) => {
  test.setTimeout(180_000)
  const port = await pickPort()
  const syURL = `http://127.0.0.1:${port}`
  const syHome = join(state.root, 'limit-switchyard-home')
  const syData = join(state.root, 'limit-switchyard-data')
  for (const d of [syHome, syData]) mkdirSync(d, { recursive: true })
  const log = openSync(join(state.root, 'publishlimit-switchyard.log'), 'a')
  switchyard = spawn(serverBinary(), ['switchyard', '--listen', `127.0.0.1:${port}`], {
    env: { PATH: process.env.PATH ?? '/usr/bin:/bin', LANG: 'C.UTF-8', HOME: syHome, CONDUCTOR_DATA_DIR: syData, CONDUCTOR_PUBLIC_URL: syURL, CONDUCTOR_WORKBENCH_TOKEN: syAdmin, CONDUCTOR_ALLOWED_ROOTS: syHome, CONDUCTOR_REACH: 'off', CONDUCTOR_RENDEZVOUS: '0', CONDUCTOR_SWITCHYARD_OPEN_HOSTS: '1', CONDUCTOR_SWITCHYARD_OPEN_HOST_SESSIONS: '1', CONDUCTOR_SWITCHYARD_OPEN_HOST_REGISTRATIONS: '60' },
    stdio: ['ignore', log, log],
  })
  await expect.poll(async () => (await fetch(`${syURL}/api/health`).catch(() => null))?.ok ?? false, { timeout: 30_000 }).toBe(true)
  const hostedNames = async () => {
    const r = await fetch(`${syURL}/api/sessions`, { headers: { Authorization: `Bearer ${syAdmin}` } })
    const list = (await r.json()) as { sessions?: Array<{ name: string; status: string }> } | Array<{ name: string; status: string }>
    return (Array.isArray(list) ? list : (list.sessions ?? [])).filter((s) => s.status === 'running').map((s) => s.name)
  }

  const here = dirname(fileURLToPath(import.meta.url))
  const home = join(state.root, 'limit-publisher-home')
  const data = join(state.root, 'limit-publisher-data')
  const cwd = join(state.root, 'limit-work')
  for (const d of [home, data, cwd]) mkdirSync(d, { recursive: true })
  const config = join(state.root, 'limit-publisher.json')
  renderConfig(join(here, 'conductor.e2e.json'), config)
  // An open host, as the desktop app publishes: no token.
  publisher = await startServer({ config, home, data, allowedRoot: state.root, defaultCwd: cwd, log: join(state.root, 'limit-publisher.log'), env: { CONDUCTOR_RENDEZVOUS: '1', CONDUCTOR_RENDEZVOUS_SERVER: syURL, CONDUCTOR_RENDEZVOUS_RELAY_ONLY: '1', CONDUCTOR_RENDEZVOUS_HOST_NAME: 'home' } })
  const pub = async <T>(method: string, path: string, body?: unknown): Promise<T> => {
    const r = await fetch(publisher!.baseURL + path, { method, headers: { Authorization: `Bearer ${publisher!.token}`, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
    const text = await r.text()
    return (text ? JSON.parse(text) : undefined) as T
  }
  const first = await pub<{ id: string }>('POST', '/api/sessions', { agentId: 'shell', name: 'first shell', cwd })
  await expect.poll(hostedNames, { timeout: 30_000, message: 'the first session is shared' }).toContain('first shell')
  const second = await pub<{ id: string }>('POST', '/api/sessions', { agentId: 'shell', name: 'second shell', cwd })

  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  await ctx.addInitScript((t) => localStorage.setItem('conductor.workbenchToken', t), publisher.token)
  const owner = await ctx.newPage()
  try {
    await owner.goto(`${publisher.baseURL}/sessions/${second.id}`)
    await owner.locator('[data-inspector] button', { hasText: 'Activity' }).click()
    const inspector = owner.locator('[data-inspector]')
    await expect(inspector).toContainText('not shared through the switchyard yet', { timeout: 30_000 })
    await expect(inspector).toContainText('open_host_limit')
    // No error badge on either session's row: nothing went wrong with its agent.
    const row = owner.locator(`[data-sidebar-row="s:${second.id}"]`)
    await expect(row).toBeVisible()
    await expect(row.locator('[data-event-mark]')).toHaveCount(0)
    await expect(owner.locator('[data-event-mark]', { hasText: 'error' })).toHaveCount(0)
    // The first ends: the slot frees, and the second is shared on a later try (the first wait is 15 seconds).
    await pub('DELETE', `/api/sessions/${first.id}`)
    await expect.poll(hostedNames, { timeout: 90_000, message: 'the second session is shared once the slot frees' }).toContain('second shell')
    await expect(inspector).toContainText('shared through the switchyard after', { timeout: 30_000 })
    await expect(owner.locator('[data-event-mark]', { hasText: 'error' })).toHaveCount(0)
  } finally {
    await ctx.close()
    await pub('DELETE', `/api/sessions/${second.id}`).catch(() => {})
  }
})
