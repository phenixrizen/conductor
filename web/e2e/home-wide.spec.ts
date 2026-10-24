import { mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { Api, expect, test, type Session } from './fixtures'
import { renderConfig, startServer, stopServer, type Started } from './server'

// A server with nothing on it yet (#359): at desktop width the home and the
// sessions list both leave for the Yard, whose welcome is not the sidebar's
// empty line; below lg the list is the home screen, and a window grown past
// lg while on it leaves too.

test.describe.configure({ mode: 'serial' })

let server: Started | null = null

test.afterAll(async () => {
  if (server) await stopServer(server.pid)
})

test('the empty home is the Yard with its welcome at desktop width, the list below it, and the list leaves when the window grows', async ({ state, browser }) => {
  test.setTimeout(120_000)
  const here = dirname(fileURLToPath(import.meta.url))
  const root = join(state.root, 'home-wide')
  const home = join(root, 'home')
  const data = join(root, 'data')
  for (const d of [home, data]) mkdirSync(d, { recursive: true })
  const config = join(root, 'conductor.json')
  renderConfig(join(here, 'conductor.e2e.json'), config)
  server = await startServer({ config, home, data, allowedRoot: root, defaultCwd: root, log: join(root, 'server.log') })

  const context = await browser.newContext({ baseURL: server.baseURL, viewport: { width: 1440, height: 900 } })
  const page = await context.newPage()
  await page.addInitScript((token) => localStorage.setItem('conductor.workbenchToken', token), server.token)

  await page.goto('/')
  await expect(page).toHaveURL(/\/yard$/, { timeout: 30_000 })
  const welcome = page.locator('[data-yard-welcome]')
  await expect(welcome).toBeVisible()
  await expect(welcome.getByRole('button', { name: 'Launch agent' })).toBeVisible()
  await expect(welcome.getByRole('button', { name: 'Load the example crews' })).toBeVisible()
  await expect(welcome.getByRole('link', { name: 'Settings' })).toBeVisible()

  // A phone: the list is the home screen.
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  await expect(page).toHaveURL(/\/sessions$/, { timeout: 30_000 })

  // Grown past lg while on the list: the sidebar is the list now, so the page leaves for the Yard.
  await page.setViewportSize({ width: 1440, height: 900 })
  await expect(page).toHaveURL(/\/yard$/, { timeout: 30_000 })
  await expect(page.locator('[data-yard-welcome]')).toBeVisible()

  // With a session on the server, a fresh visit lands on it, not on the Yard: the home waits for the sessions to be read.
  const api = new Api(server)
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e home target' })
  await page.goto('/')
  await expect(page).toHaveURL(new RegExp(`/sessions/${s.id}$`), { timeout: 30_000 })
  await page.goto('/sessions')
  await expect(page).toHaveURL(new RegExp(`/sessions/${s.id}$`), { timeout: 30_000 })
  await api.stopSession(s.id)
  await context.close()
})
