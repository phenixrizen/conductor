import { _electron as electron, expect, test } from '@playwright/test'
import { existsSync } from 'node:fs'
import { join } from 'node:path'

// The packaged-shape smoke: the shell starts the checkout's server, the window shows the workbench signed in, and quitting ends the
// server. Needs a display (xvfb-run on Linux CI) and bin/conductor (make build-go).
const bin = process.env.CONDUCTOR_DESKTOP_BIN || join(__dirname, '..', '..', 'bin', 'conductor')

test.skip(!existsSync(bin), `no server binary at ${bin}: make build-go`)

test('the app opens the workbench on its own server and stops it on quit', async () => {
  test.setTimeout(120_000)
  const app = await electron.launch({ args: [join(__dirname, '..')], env: { ...process.env, CONDUCTOR_DESKTOP_BIN: bin, ELECTRON_DISABLE_SANDBOX: '1' } })
  const page = await app.firstWindow()
  await page.waitForURL(/^http:\/\/127\.0\.0\.1:\d+\//, { timeout: 60_000 })
  const url = new URL(page.url())
  await expect(page.locator('[data-session-list], nav').first()).toBeVisible({ timeout: 30_000 })
  // Signed in through the bridge: the agents page lists the catalog without asking for a token.
  await page.goto(`${url.origin}/agents`)
  await expect(page.locator('[data-agent]').first()).toBeVisible({ timeout: 30_000 })
  const token = await page.evaluate(() => (window as unknown as { conductorDesktop: { token(): Promise<string> } }).conductorDesktop.token())
  expect(token.length).toBeGreaterThanOrEqual(32)
  expect(await page.evaluate(() => localStorage.getItem('conductor.adminToken'))).toBeNull()
  const health = await fetch(`${url.origin}/api/health`)
  expect(health.ok).toBe(true)
  await app.close()
  await expect.poll(() => fetch(`${url.origin}/api/health`).then(() => 'up').catch(() => 'down'), { timeout: 30_000 }).toBe('down')
})
