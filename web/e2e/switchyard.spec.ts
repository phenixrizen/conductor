import { hostname } from 'node:os'
import { spawn, type ChildProcess } from 'node:child_process'
import { randomBytes, randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, openSync, readdirSync, readFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, test } from './fixtures'
import { renderConfig, scratchRepo, serverBinary, startServer, stopServer, stubPath, type Started } from './server'

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
/** A second server of the suite's shape, publishing to the switchyard: the crew run link test's. */
let publisher: Started | null = null

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
    env: { PATH: process.env.PATH ?? '/usr/bin:/bin', LANG: 'C.UTF-8', HOME: home, CONDUCTOR_DATA_DIR: data, CONDUCTOR_PUBLIC_URL: syURL, CONDUCTOR_WORKBENCH_TOKEN: syAdmin, CONDUCTOR_HOST_TOKENS: hostToken, CONDUCTOR_ALLOWED_ROOTS: home, CONDUCTOR_REACH: 'off', CONDUCTOR_RENDEZVOUS: '0' },
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

test.afterAll(async ({ state }) => {
  if (publisher) await stopServer(publisher.pid)
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
  // Opened from the workbench (its token is in this browser), the page sits beside the sidebar.
  await expect(page.locator('[data-join-frame]')).toHaveAttribute('data-join-frame', 'workbench')
  await page.getByRole('textbox', { name: 'Your name' }).fill('e2e guest')
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

  // Leave brings the card back, the name kept, and the switchyard no longer counts the viewer; the link still joins.
  await page.locator('[data-join-leave]').click()
  await expect(page.locator('[data-join-left]')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Join session' })).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Your name' })).toHaveValue('e2e guest')
  await expect(page.locator('.terminal-host')).toHaveCount(0)
  await expect
    .poll(async () => {
      const list3 = (await (await fetch(`${syURL}/api/sessions`, { headers: { Authorization: `Bearer ${syAdmin}` } })).json()) as { sessions?: Array<{ id: string; viewers?: number }> } | Array<{ id: string; viewers?: number }>
      const s = (Array.isArray(list3) ? list3 : (list3.sessions ?? [])).find((x) => x.id === hosted.id)
      return s?.viewers ?? 0
    }, { timeout: 30_000, message: 'the switchyard drops the viewer' })
    .toBe(0)

  // The link stays under Shared with you after Leave, on any page; opening it joins at once; × forgets it.
  const shared = page.locator('[data-sidebar-shared] [data-shared-entry]')
  await expect(shared).toHaveCount(1)
  await expect(shared).toContainText('hosted-stub')
  await expect(shared).toContainText(`through 127.0.0.1:${new URL(syURL).port}`)
  await page.goto('/yard')
  await expect(shared).toHaveCount(1)
  await shared.getByRole('link').click()
  await expect(page).toHaveURL(new RegExp(`/join/${link.token}\\?server=`))
  await expect(page.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
  await expect(page.getByRole('button', { name: 'Join session' })).toHaveCount(0)
  await page.locator('[data-shared-forget]').click()
  await expect(page.locator('[data-sidebar-shared]')).toHaveCount(0)
  expect(await page.evaluate(() => localStorage.getItem('conductor.joined'))).toBe('[]')
})

test('the switchyard serves its landing page, a 404 for workbench paths, and the app only for joining', async () => {
  const port = new URL(syURL).port
  const landing = await fetch(`${syURL}/`)
  const body = await landing.text()
  expect(landing.status).toBe(200)
  expect(body).toContain('This is a Conductor switchyard.')
  expect(body).toContain(`conductor://127.0.0.1:${port}/join/`)
  expect(body).toContain('Off · plain http')
  for (const p of ['/crews', '/sessions/x', '/wall', '/yard', '/roundhouse', '/agents', '/events', '/settings']) {
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

// A crew run on a server that publishes to the switchyard: its Share mints
// the link there (link_run), the switchyard's join lists the members, a
// member's terminal comes through the relay, and a revoke from the home
// server takes the link away there. The Go loopback test covers the
// protocol; this is what a person sees.
test('a crew run published to the switchyard is shared through it: Share says works from anywhere, the link lists the members there, and a revoke ends it', async ({ page, browser, state }) => {
  test.setTimeout(150_000)
  const here = dirname(fileURLToPath(import.meta.url))
  const home2 = join(state.root, 'publisher-home')
  const data2 = join(state.root, 'publisher-data')
  const repo2 = join(state.root, 'publisher-repo')
  for (const d of [home2, data2, repo2]) mkdirSync(d, { recursive: true })
  scratchRepo(repo2)
  const config = join(state.root, 'publisher.json')
  renderConfig(join(here, 'conductor.e2e.json'), config)
  publisher = await startServer({
    config,
    home: home2,
    data: data2,
    allowedRoot: state.root,
    defaultCwd: repo2,
    log: join(state.root, 'publisher.log'),
    env: { CONDUCTOR_RENDEZVOUS: '1', CONDUCTOR_RENDEZVOUS_SERVER: syURL, CONDUCTOR_RENDEZVOUS_TOKEN: hostToken, CONDUCTOR_RENDEZVOUS_RELAY_ONLY: '1', CONDUCTOR_RENDEZVOUS_HOST_NAME: 'publisher' },
  })
  const pub = async <T>(method: string, path: string, body?: unknown): Promise<{ status: number; body: T }> => {
    const r = await fetch(publisher!.baseURL + path, { method, headers: { Authorization: `Bearer ${publisher!.token}`, 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
    const text = await r.text()
    return { status: r.status, body: (text ? JSON.parse(text) : undefined) as T }
  }
  const crew = await pub<{ crew: { id: string } }>('POST', '/api/crews', {
    name: 'e2e crew link',
    goal: 'be shared',
    cwd: '',
    where: 'server',
    isolation: 'none',
    openAfterLaunch: false,
    members: [
      { name: 'lead', agentId: 'claude', prompt: 'say hello', start: { when: 'immediately' } },
      { name: 'review', agentId: 'claude', prompt: 'review it', start: { when: 'immediately' } },
    ],
  })
  expect(crew.status).toBe(201)
  const launched = await pub<{ run: { id: string } }>('POST', `/api/crews/${crew.body.crew.id}/launch`)
  expect(launched.status).toBe(201)
  const runId = launched.body.run.id
  await until('both members run', async () => {
    const r = await pub<{ run: { members: Array<{ status: string }> } }>('GET', `/api/runs/${runId}`)
    return r.body.run.members.length === 2 && r.body.run.members.every((m) => m.status === 'running')
  })

  // Share on the run page of the publisher's own workbench: minted at the switchyard, and a control link on request.
  const ctx = await browser.newContext()
  await ctx.addInitScript((t) => localStorage.setItem('conductor.workbenchToken', t), publisher.token)
  const owner = await ctx.newPage()
  await owner.goto(`${publisher.baseURL}/runs/${runId}`)
  await owner.getByRole('button', { name: 'Share', exact: true }).first().click()
  const dialog = owner.getByRole('dialog')
  await expect(dialog.locator('[data-created-url]')).toBeVisible({ timeout: 30_000 })
  await expect(dialog.locator('[data-link-reach]')).toHaveAttribute('data-link-reach', 'remote')
  await expect(dialog).toContainText('Works from anywhere')
  await expect(dialog).toContainText('opens every agent of the crew there')
  await dialog.locator('[data-share-role-option="control"]').click()
  await expect(dialog.locator('[data-share-link]').first()).toContainText('Control', { timeout: 30_000 })
  const url = (await dialog.locator('[data-created-url]').getAttribute('title')) ?? ''
  expect(url.startsWith(`${syURL}/join/`), url).toBe(true)
  const token = url.slice(`${syURL}/join/`.length)
  await ctx.close()

  // The link joins through the switchyard from this workbench: the crew's members, one opened, its terminal over the relay.
  const port = new URL(syURL).port
  await page.goto(`/join/${token}?server=${encodeURIComponent(syURL)}`)
  await expect(page.getByRole('heading', { name: 'Join e2e crew link' })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText(`Shared through 127.0.0.1:${port}`)).toBeVisible()
  await page.getByRole('textbox', { name: 'Your name' }).fill('e2e crew guest')
  await page.getByRole('button', { name: 'Join crew' }).click()
  await expect(page.locator('[data-join-tiles] [data-member]')).toHaveCount(2)
  await expect(page.locator('[data-join-tiles] [data-member="lead"]')).toBeVisible()
  await expect(page.locator('[data-join-tiles] [data-member="review"]')).toBeVisible()
  await page.locator('[data-join-tiles] [data-member="lead"] [data-tile-open]').click()
  const badge = page.locator('[data-transport-state]').first()
  await expect(badge).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
  await expect(badge).toHaveAttribute('data-transport-kind', 'relay')
  await expect(page.locator('.terminal-host .xterm-screen').first()).toBeVisible({ timeout: 30_000 })
  await page.locator('.terminal-host .xterm-helper-textarea').first().focus()
  await page.keyboard.type('hello crew through the switchyard')
  await page.keyboard.press('Enter')
  const transcripts = join(home2, '.stub-sessions')
  await expect
    .poll(() => existsSync(transcripts) && readdirSync(transcripts).some((f) => readFileSync(join(transcripts, f), 'utf8').includes('hello crew through the switchyard')), { timeout: 20_000, message: 'the member got the line' })
    .toBe(true)

  // Revoked at home, the link is gone at the switchyard and the viewer is cut off.
  const links = await pub<{ links: Array<{ id: string; remote?: boolean; runId?: string }> }>('GET', `/api/runs/${runId}/links`)
  expect(links.body.links.length).toBeGreaterThan(0)
  expect(links.body.links.every((l) => l.remote === true)).toBe(true)
  for (const l of links.body.links) expect((await pub('DELETE', `/api/runs/${runId}/links/${l.id}`)).status).toBe(204)
  await expect.poll(async () => (await fetch(`${syURL}/api/join/${token}`)).status, { timeout: 15_000 }).toBe(404)
  await expect.poll(() => badge.getAttribute('data-transport-state'), { timeout: 30_000 }).not.toBe('open')
  await pub('POST', `/api/runs/${runId}/stop`)
  await stopServer(publisher.pid)
  publisher = null
})
