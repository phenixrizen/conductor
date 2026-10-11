import { _electron as electron, expect, test, type ElectronApplication, type Page } from '@playwright/test'
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

// A fresh install is told once, in the workbench, that its sessions are published to a switchyard and how to turn that off; an
// upgrade was told already (smoke.spec.ts runs with whatever settings the machine has). The app's settings live under
// XDG_CONFIG_HOME on Linux, so a directory of the test's own is a fresh install. The server runs through a wrapper with reach off
// and publishing off: nothing here leaves the machine. Needs a display (xvfb-run on Linux CI) and bin/conductor (make build-go).
const bin = process.env.CONDUCTOR_DESKTOP_BIN || join(__dirname, '..', '..', 'bin', 'conductor')

test.skip(!existsSync(bin), `no server binary at ${bin}: make build-go`)
test.skip(process.platform !== 'linux', 'a fresh install of the test\'s own is placed with XDG_CONFIG_HOME, which Linux alone reads')

const workbench = /^http:\/\/(127\.0\.0\.1|localhost):\d+\//
const words = 'Sessions are published to a switchyard'

async function open(env: Record<string, string>): Promise<{ app: ElectronApplication; page: Page }> {
  const app = await electron.launch({ args: [join(__dirname, '..')], env })
  let page: Page | undefined
  await expect
    .poll(
      () => {
        page = app.windows().find((p) => workbench.test(p.url()))
        return !!page
      },
      { timeout: 60_000 },
    )
    .toBe(true)
  await expect(page!.locator('[data-session-list], nav').first()).toBeVisible({ timeout: 30_000 })
  return { app, page: page! }
}

test('a fresh install is told once that its sessions are published, and how to turn it off', async () => {
  test.setTimeout(180_000)
  const root = mkdtempSync(join(tmpdir(), 'cd-firstrun-'))
  const home = join(root, 'home')
  mkdirSync(home)
  const wrapper = join(root, 'conductor')
  writeFileSync(wrapper, `#!/bin/sh\nCONDUCTOR_REACH=off CONDUCTOR_RENDEZVOUS=0 exec ${JSON.stringify(bin)} "$@"\n`)
  chmodSync(wrapper, 0o755)
  const env: Record<string, string> = {}
  for (const [k, v] of Object.entries(process.env)) if (v !== undefined && !k.startsWith('CONDUCTOR_')) env[k] = v
  Object.assign(env, { CONDUCTOR_DESKTOP_BIN: wrapper, ELECTRON_DISABLE_SANDBOX: '1', XDG_CONFIG_HOME: join(root, 'config'), HOME: home })
  try {
    const first = await open(env)
    const userData = await first.app.evaluate(({ app }) => app.getPath('userData'))
    expect(userData.startsWith(root), userData).toBe(true)
    const toast = first.page.locator('li').filter({ hasText: words }).last()
    await expect(toast).toBeVisible({ timeout: 30_000 })
    await expect(toast).toContainText('Settings → Switchyard')
    // Marked as soon as it is owed, so a second start never says it again.
    const saved = JSON.parse(readFileSync(join(userData, 'settings.json'), 'utf8')) as { noticed?: string[]; switchyardEnabled?: boolean }
    expect(saved.noticed).toEqual(['publishing'])
    expect(saved.switchyardEnabled).toBe(true)
    await first.app.close()

    const again = await open(env)
    // The notice comes as the workbench mounts, if it comes at all: by the time the agents page has loaded, it would be up.
    await again.page.goto(new URL('/agents', again.page.url()).toString())
    await expect(again.page.locator('[data-agent]').first()).toBeVisible({ timeout: 30_000 })
    await expect(again.page.getByText(words)).toHaveCount(0)
    await again.app.close()
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
