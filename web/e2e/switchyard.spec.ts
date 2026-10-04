import { hostname } from 'node:os'
import { spawn, type ChildProcess } from 'node:child_process'
import { randomBytes, randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, openSync, readFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { join } from 'node:path'
import { expect, test } from './fixtures'
import { serverBinary, stubPath } from './server'

// A switchyard: a second Conductor in switchyard mode, a conductor host
// publishing a stub session to it, a link minted there, and this workbench's
// own join page signalling to the switchyard from its own origin (what the
// desktop app does for an invite). The terminal goes through the
// switchyard's relay (the host runs relay-only here, as a CI box may have no
// route for ICE).
test.describe.configure({ mode: 'serial' })

let switchyard: ChildProcess | null = null
let host: ChildProcess | null = null
let syURL = ''
const syAdmin = randomBytes(16).toString('hex')
const hostToken = 'sy-host-' + randomBytes(8).toString('hex')
const agentSession = randomUUID()
let home = ''

function pickPort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const s = createServer()
    s.listen(0, '127.0.0.1', () => {
      const port = (s.address() as { port: number }).port
      s.close(() => resolve(port))
    })
    s.on('error', reject)
  })
}

async function until(what: string, cond: () => Promise<boolean>, ms = 30_000) {
  const end = Date.now() + ms
  while (Date.now() < end) {
    if (await cond().catch(() => false)) return
    await new Promise((r) => setTimeout(r, 200))
  }
  throw new Error(`timed out waiting: ${what}`)
}

test.beforeAll(async ({ state }) => {
  const port = await pickPort()
  syURL = `http://127.0.0.1:${port}`
  home = join(state.root, 'switchyard-home')
  const data = join(state.root, 'switchyard-data')
  mkdirSync(home, { recursive: true })
  mkdirSync(data, { recursive: true })
  const log = openSync(join(state.root, 'switchyard.log'), 'a')
  switchyard = spawn(serverBinary(), ['switchyard', '--listen', `127.0.0.1:${port}`], {
    env: { PATH: process.env.PATH ?? '/usr/bin:/bin', LANG: 'C.UTF-8', HOME: home, CONDUCTOR_DATA_DIR: data, CONDUCTOR_PUBLIC_URL: syURL, CONDUCTOR_WORKBENCH_TOKEN: syAdmin, CONDUCTOR_HOST_TOKENS: hostToken, CONDUCTOR_ALLOWED_ROOTS: home, CONDUCTOR_REACH: 'off' },
    stdio: ['ignore', log, log],
  })
  await until('the switchyard answers', async () => {
    const r = await fetch(`${syURL}/api/health`)
    const h = (await r.json()) as { switchyard?: boolean }
    return r.ok && h.switchyard === true
  })
  // A hosted stub published to the switchyard, relay-only, headless.
  host = spawn(serverBinary(), ['host', '--server', syURL, '--token', hostToken, '--relay-only', '--no-local', '--name', 'hosted-stub', '--cwd', home, '--', '/bin/bash', stubPath, '--session-id', agentSession], {
    env: { PATH: process.env.PATH ?? '/usr/bin:/bin', LANG: 'C.UTF-8', HOME: home, STUB_IDENTITY: 'claude', STUB_REPORT: 'plain' },
    stdio: ['ignore', log, log],
  })
  await until('the hosted session is listed', async () => {
    const r = await fetch(`${syURL}/api/sessions`, { headers: { Authorization: `Bearer ${syAdmin}` } })
    const list = (await r.json()) as { sessions?: Array<{ name: string }> } | Array<{ name: string }>
    const sessions = Array.isArray(list) ? list : (list.sessions ?? [])
    return sessions.some((s) => s.name === 'hosted-stub')
  })
})

test.afterAll(({ state }) => {
  host?.kill('SIGKILL')
  switchyard?.kill('SIGKILL')
  // The log stays readable in the run's output: the scratch root goes with the teardown.
  const log = join(state.root, 'switchyard.log')
  if (existsSync(log)) console.log('switchyard log:\n' + readFileSync(log, 'utf8').split('\n').slice(-40).join('\n'))
})

test('a link minted on the switchyard opens on this workbench with ?server=, and the terminal reaches the hosted session', async ({ page }) => {
  const r = await fetch(`${syURL}/api/sessions`, { headers: { Authorization: `Bearer ${syAdmin}` } })
  const list = (await r.json()) as { sessions?: Array<{ id: string; name: string; kind: string }> } | Array<{ id: string; name: string; kind: string }>
  const sessions = Array.isArray(list) ? list : (list.sessions ?? [])
  const hosted = sessions.find((s) => s.name === 'hosted-stub')!
  expect(hosted.kind).toBe('hosted')
  const made = await fetch(`${syURL}/api/sessions/${hosted.id}/links`, { method: 'POST', headers: { Authorization: `Bearer ${syAdmin}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ role: 'control', label: 'e2e invite' }) })
  const link = (await made.json()) as { url: string; token: string; invite: string }
  expect(made.status).toBe(201)
  expect(link.invite).toBe(`conductor://127.0.0.1:${new URL(syURL).port}/join/${link.token}?http=1`)

  // This workbench's own join page, told to signal to the switchyard: the join route answers it across origins.
  await page.goto(`/join/${link.token}?server=${encodeURIComponent(syURL)}`)
  await expect(page.getByText(`Shared through 127.0.0.1:${new URL(syURL).port}`)).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Join hosted-stub' })).toBeVisible({ timeout: 15_000 })
  await page.getByPlaceholder('Priya Shah').fill('e2e guest')
  await page.getByRole('button', { name: 'Join session' }).click()
  const badge = page.locator('[data-transport-state]').first()
  await expect(badge).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
  await expect(badge).toHaveAttribute('data-transport-kind', 'relay')
  const screen = page.locator('.terminal-host .xterm-screen').first()
  await expect(screen).toBeVisible({ timeout: 30_000 })
  await expect
    .poll(async () => {
      const list2 = (await (await fetch(`${syURL}/api/sessions`, { headers: { Authorization: `Bearer ${syAdmin}` } })).json()) as { sessions?: Array<{ id: string; viewers?: number }> } | Array<{ id: string; viewers?: number }>
      const s = (Array.isArray(list2) ? list2 : (list2.sessions ?? [])).find((x) => x.id === hosted.id)
      return s?.viewers ?? 0
    }, { timeout: 30_000, message: 'the switchyard counts the viewer' })
    .toBeGreaterThan(0)
  await page.locator('.terminal-host .xterm-helper-textarea').first().focus()
  await page.keyboard.type('hello through the switchyard')
  await page.keyboard.press('Enter')
  const transcript = join(home, '.stub-sessions', `${agentSession}.txt`)
  await expect.poll(() => (existsSync(transcript) ? readFileSync(transcript, 'utf8') : ''), { timeout: 20_000, message: 'the hosted stub got the line' }).toContain('hello through the switchyard')
})

test('the switchyard serves its landing page, a 404 for workbench paths, and the app only for joining', async () => {
  const port = new URL(syURL).port
  const landing = await fetch(`${syURL}/`)
  const body = await landing.text()
  expect(landing.status).toBe(200)
  expect(body).toContain('This is a Conductor switchyard.')
  expect(body).toContain(`conductor://127.0.0.1:${port}/join/`)
  expect(body).toContain('Off · plain http')
  for (const p of ['/crews', '/sessions/x', '/wall', '/agents', '/events', '/settings']) {
    const r = await fetch(`${syURL}${p}`)
    expect(r.status, p).toBe(404)
    expect(await r.text(), p).toContain("The workbench isn't here.")
  }
  const join = await fetch(`${syURL}/join/not-a-token`)
  expect(join.status).toBe(200)
  const joinBody = await join.text()
  expect(joinBody).toMatch(/__nuxt|\/_nuxt\//)
  expect(joinBody).not.toContain("The workbench isn't here.")
  const status = await fetch(`${syURL}/api/switchyard/status`, { headers: { Authorization: `Bearer ${syAdmin}` } })
  expect(status.status).toBe(200)
  const figures = (await status.json()) as { hosts: number; sessions: number; hostList: Array<{ name: string }> }
  expect(figures.hosts).toBe(1)
  expect(figures.sessions).toBe(1)
  expect(figures.hostList[0]?.name).toBe(hostname()) // the publishing machine, not the session's name
  expect((await fetch(`${syURL}/api/switchyard/status`)).status).toBe(401)
})

test('the switchyard launches nothing, and its join route answers no stranger origin', async () => {
  const r = await fetch(`${syURL}/api/sessions`, { method: 'POST', headers: { Authorization: `Bearer ${syAdmin}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ agentId: 'claude' }) })
  expect(r.status).toBe(403)
  expect(((await r.json()) as { error: { code: string } }).error.code).toBe('switchyard')
  const made = await fetch(`${syURL}/api/health`, { headers: { Origin: 'https://evil.example' } })
  expect(made.headers.get('access-control-allow-origin')).toBeNull()
})
