import { _electron as electron, expect, test } from '@playwright/test'
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

// The packaged-shape smoke: the shell starts the checkout's server, the window shows the workbench signed in, and quitting ends the
// server. Needs a display (xvfb-run on Linux CI) and bin/conductor (make build-go).
const bin = process.env.CONDUCTOR_DESKTOP_BIN || join(__dirname, '..', '..', 'bin', 'conductor')

test.skip(!existsSync(bin), `no server binary at ${bin}: make build-go`)

test('the app opens the workbench on its own server and stops it on quit', async () => {
  test.setTimeout(120_000)
  const app = await electron.launch({ args: [join(__dirname, '..')], env: { ...process.env, CONDUCTOR_DESKTOP_BIN: bin, ELECTRON_DISABLE_SANDBOX: '1' } })
  const output: string[] = []
  app.process().stderr?.on('data', (d: Buffer) => output.push(d.toString()))
  app.process().stdout?.on('data', (d: Buffer) => output.push(d.toString()))
  // The splash is the first window, up while the server starts; the workbench window comes once the server answers.
  const splash = await app.firstWindow()
  await expect.poll(() => splash.url(), { timeout: 30_000 }).toContain('splash.html')
  await expect(splash.locator('#credit')).toHaveAttribute('aria-label', 'Sponsored and maintained by RockSolid Labs')
  const workbench = /^http:\/\/(127\.0\.0\.1|localhost):\d+\//
  let page = splash
  try {
    await expect
      .poll(
        () => {
          const w = app.windows().find((p) => workbench.test(p.url()))
          if (w) page = w
          return !!w
        },
        { timeout: 60_000 },
      )
      .toBe(true)
  } catch (e) {
    // What the shell and its server said, for a CI log that otherwise shows only the timeout.
    console.log('window urls:', app.windows().map((p) => p.url()))
    console.log('electron output:\n' + output.join('').slice(-4000))
    try {
      const userData = await app.evaluate(({ app }) => app.getPath('userData'))
      const dir = join(userData, 'logs')
      for (const f of readdirSync(dir)) console.log(`--- ${f}:\n` + readFileSync(join(dir, f), 'utf8').slice(-4000))
    } catch (err) {
      console.log('no app logs:', (err as Error).message)
    }
    throw e
  }
  const url = new URL(page.url())
  await expect(page.locator('[data-session-list], nav').first()).toBeVisible({ timeout: 30_000 })
  // The splash goes once the workbench window shows.
  await expect.poll(() => app.windows().filter((p) => p.url().includes('splash.html')).length, { timeout: 30_000 }).toBe(0)
  // Signed in through the bridge: the agents page lists the catalog without asking for a token.
  await page.goto(`${url.origin}/agents`)
  await expect(page.locator('[data-agent]').first()).toBeVisible({ timeout: 30_000 })
  const token = await page.evaluate(() => (window as unknown as { conductorDesktop: { token(): Promise<string> } }).conductorDesktop.token())
  expect(token.length).toBeGreaterThanOrEqual(32)
  expect(await page.evaluate(() => localStorage.getItem('conductor.workbenchToken'))).toBeNull()
  const health = await fetch(`${url.origin}/api/health`)
  expect(health.ok).toBe(true)
  await app.close()
  await expect.poll(() => fetch(`${url.origin}/api/health`).then(() => 'up').catch(() => 'down'), { timeout: 30_000 }).toBe('down')
})
