import { dirname } from 'node:path'
import { expect, test } from './fixtures'

// The desktop app's Settings page, under a stand-in for its bridge: the
// folder picker browses the server's own folders (GET /api/paths, scope any,
// as the app's server allows), never a native dialog, so on Windows it is the
// WSL distribution that is browsed.
test.skip(process.platform === 'win32', 'POSIX paths')

test('the folder picker browses the server, outside the roots too, and fills the field', async ({ page, state }) => {
  const form = {
    dataDir: state.data,
    allowedRoots: [state.root],
    defaultCwd: state.root,
    yolo: false,
    reach: 'off',
    closeToTray: true,
    wslDistro: '',
    wslWindowsHome: false,
    switchyardEnabled: false,
    switchyardServer: '',
    switchyardToken: '',
    switchyardName: '',
    noticed: ['publishing'],
  }
  await page.addInitScript(
    ({ token, form, url }) => {
      const settings = { ...form }
      ;(window as unknown as { conductorDesktop: unknown }).conductorDesktop = {
        version: 'e2e',
        platform: 'linux',
        token: async () => token,
        openExternal: async () => {},
        settings: { get: async () => ({ ...settings }), set: async (p: object) => Object.assign(settings, p) },
        restartServer: async () => {},
        openInBrowser: async () => {},
        showLog: async () => {},
        serverState: async () => ({ state: 'running', url, failures: 0 }),
        notice: async () => '',
        ice: async () => ({ forwarding: false, port: 0, publicIp: '', wslAddress: '', firewall: 'unknown' }),
        allowIceFirewall: async () => 'unknown',
        versions: async () => ({ app: 'e2e', electron: '', node: '', chrome: '', server: '' }),
        onServerState: () => () => {},
      }
    },
    { token: state.token, form, url: state.baseURL },
  )
  await page.goto('/settings')
  await page.locator('[data-pick-dir="defaultCwd"]').click()
  const picker = page.getByRole('dialog')
  await expect(picker.locator('[data-dir-picker-entry="repo"]')).toBeVisible()
  // Up from the root lists its parent: outside the allowed roots, which only the any scope lists.
  await picker.locator('[data-dir-picker-up]').click()
  await expect(picker.locator('[data-dir-picker-path]')).toHaveValue(dirname(state.root))
  await expect(picker.locator('[data-dir-picker-note]')).toContainText('Outside the allowed roots')
  // Back in by typing the path, into the repository, and use it.
  await picker.locator('[data-dir-picker-path]').fill(state.root)
  await picker.locator('[data-dir-picker-path]').press('Enter')
  await picker.locator('[data-dir-picker-entry="repo"]').click()
  await expect(picker.locator('[data-dir-picker-path]')).toHaveValue(state.repo)
  await expect(picker.locator('[data-dir-picker-note]')).toHaveCount(0)
  await picker.locator('[data-dir-picker-use]').click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByLabel('Default working directory', { exact: true })).toHaveValue(state.repo)
  // The page uses the whole width: two columns of cards at this size.
  const grid = await page.locator('[data-settings-grid]').boundingBox()
  expect(grid!.width).toBeGreaterThan(1000)
  // The app card ends with the sponsor credit (the Sponsor Kit's 1c).
  const about = page.locator('[data-about-card]')
  await expect(about.locator('[data-about-row="app"]')).toHaveText('e2e')
  await expect(about.locator('[data-about-license]')).toContainText('Apache-2.0')
  await expect(about.locator('[data-about-copyright]')).toContainText('the Conductor Authors and RockSolid Labs, Inc.')
  await expect(about.locator('[data-about-credit]')).toContainText('Sponsored and maintained by RockSolid Labs')
  await expect(about.locator('[data-about-credit] a')).toHaveAttribute('href', 'https://rocksolidlabs.io')
  // The License row links the third-party notices, which open as text from the app's own server.
  const notices = about.locator('[data-about-notices]')
  await expect(notices).toHaveText('Third-party notices')
  const [tab] = await Promise.all([page.context().waitForEvent('page'), notices.click()])
  await expect(tab).toHaveURL(`${state.baseURL}/third-party-notices.txt`)
  await expect(tab.locator('body')).toContainText('THIRD-PARTY NOTICES')
  await expect(tab.locator('body')).toContainText('github.com/pion/webrtc/v4')
  await tab.close()
})

test('in a browser, Settings says what the server is and who sponsors it', async ({ page }) => {
  await page.goto('/settings')
  const about = page.locator('[data-about-card]')
  await expect(about.locator('[data-about-row="server"]')).not.toBeEmpty()
  await expect(about.locator('[data-about-credit]')).toContainText('Sponsored and maintained by RockSolid Labs')
})

test('a "Runs in" field has a folder button that browses the allowed roots and fills it', async ({ page, state }) => {
  await page.goto('/crews/new')
  const field = page.locator('[data-crew-editor] [data-dir-input] input[role="combobox"]')
  await page.locator('[data-crew-editor] [data-dir-input-pick]').click()
  const picker = page
  // An empty field opens on the server's default directory.
  await expect(picker.locator('[data-dir-picker-path]')).toHaveValue(state.repo)
  // Inside the roots there is no Home to jump outside them.
  await expect(picker.locator('[data-dir-picker-home]')).toHaveCount(0)
  await picker.locator('[data-dir-picker-up]').click()
  await expect(picker.locator('[data-dir-picker-path]')).toHaveValue(state.root)
  await picker.locator('[data-dir-picker-entry="repo"]').click()
  await expect(picker.locator('[data-dir-picker-path]')).toHaveValue(state.repo)
  await picker.locator('[data-dir-picker-use]').click()
  await expect(page.locator('[data-dir-picker-path]')).toHaveCount(0)
  await expect(field).toHaveValue(state.repo)
})

// The one-time notice the desktop app hands the workbench (a fresh install's,
// or an upgrade's): one toast that says sessions are published to a
// switchyard, that it mints and checks the links, and how to turn it off, with
// the way to Settings.
test('the app\'s publishing notice says what is published and leads to Settings', async ({ page, state }) => {
  const form = {
    dataDir: state.data,
    allowedRoots: [state.root],
    defaultCwd: state.root,
    yolo: false,
    reach: 'off',
    closeToTray: true,
    wslDistro: '',
    wslWindowsHome: false,
    switchyardEnabled: true,
    switchyardServer: '',
    switchyardToken: '',
    switchyardName: '',
    noticed: ['publishing'],
  }
  await page.addInitScript(
    ({ token, form, url }) => {
      const settings = { ...form }
      // The app hands the notice out once; a page loaded again is told nothing.
      let owed = sessionStorage.getItem('e2e.noticed') ? '' : 'publishing'
      ;(window as unknown as { conductorDesktop: unknown }).conductorDesktop = {
        version: 'e2e',
        platform: 'linux',
        token: async () => token,
        openExternal: async () => {},
        settings: { get: async () => ({ ...settings }), set: async (p: object) => Object.assign(settings, p) },
        restartServer: async () => {},
        openInBrowser: async () => {},
        showLog: async () => {},
        serverState: async () => ({ state: 'running', url, failures: 0 }),
        notice: async () => {
          const n = owed
          owed = ''
          if (n) sessionStorage.setItem('e2e.noticed', '1')
          return n
        },
        ice: async () => ({ forwarding: false, port: 0, publicIp: '', wslAddress: '', firewall: 'unknown' }),
        allowIceFirewall: async () => 'unknown',
        versions: async () => ({ app: 'e2e', electron: '', node: '', chrome: '', server: '' }),
        onServerState: () => () => {},
      }
    },
    { token: state.token, form, url: state.baseURL },
  )
  await page.goto('/yard')
  const toast = page.locator('li').filter({ hasText: 'Sessions are published to a switchyard' }).last()
  await expect(toast).toBeVisible({ timeout: 15_000 })
  await expect(toast).toContainText('switchyard.rslabs.net')
  await expect(toast).toContainText('publish only to one whose operator you trust')
  await expect(toast).toContainText('Settings → Switchyard')
  await toast.getByRole('button', { name: 'Settings' }).click()
  await expect(page).toHaveURL(/\/settings$/)
  const card = page.locator('[data-switchyard-settings]')
  await expect(card).toBeVisible()
  // The card says what the switchyard is told and what it does.
  await expect(card).toContainText("never its chat, activity or command line")
  await expect(card).toContainText('It mints and checks the links and decides who joins')
  await expect(card).toContainText('Publish only to a switchyard whose operator you trust')
  // Once only.
  await page.reload()
  await expect(page.locator('[data-switchyard-settings]')).toBeVisible()
  await expect(page.getByText('Sessions are published to a switchyard')).toHaveCount(0)
})

// A switchyard named as plain http on another machine is refused as it is
// saved, with the words why, and nothing reaches the app; one on this
// machine is saved and kept. Settings saved with such a switchyard before
// the app refused it show the words as the page opens.
test('a plain switchyard on another machine is refused at save with the words why, and a loopback one is kept', async ({ page, state }) => {
  const words = 'the switchyard must be https:// unless it runs on this machine (localhost): over plain http:// what is published there travels in the clear and unverified'
  const form = {
    dataDir: state.data,
    allowedRoots: [state.root],
    defaultCwd: state.root,
    yolo: false,
    reach: 'off',
    closeToTray: true,
    wslDistro: '',
    wslWindowsHome: false,
    switchyardEnabled: true,
    switchyardServer: '',
    switchyardToken: '',
    switchyardName: '',
    noticed: ['publishing'],
  }
  await page.addInitScript(
    ({ token, form, url }) => {
      // The app's settings across reloads of the page, and every save the page asked of it.
      const kept = sessionStorage.getItem('e2e.settings')
      const settings = kept ? JSON.parse(kept) : { ...form }
      const w = window as unknown as { conductorDesktop: unknown; e2eSets: object[] }
      w.e2eSets = JSON.parse(sessionStorage.getItem('e2e.sets') ?? '[]')
      w.conductorDesktop = {
        version: 'e2e',
        platform: 'linux',
        token: async () => token,
        openExternal: async () => {},
        settings: {
          get: async () => ({ ...settings }),
          set: async (p: object) => {
            w.e2eSets.push(p)
            sessionStorage.setItem('e2e.sets', JSON.stringify(w.e2eSets))
            Object.assign(settings, p)
            sessionStorage.setItem('e2e.settings', JSON.stringify(settings))
            return { ...settings }
          },
        },
        restartServer: async () => {},
        openInBrowser: async () => {},
        showLog: async () => {},
        serverState: async () => ({ state: 'running', url, failures: 0 }),
        notice: async () => '',
        ice: async () => ({ forwarding: false, port: 0, publicIp: '', wslAddress: '', firewall: 'unknown' }),
        allowIceFirewall: async () => 'unknown',
        versions: async () => ({ app: 'e2e', electron: '', node: '', chrome: '', server: '' }),
        onServerState: () => () => {},
      }
    },
    { token: state.token, form, url: state.baseURL },
  )
  const sets = () => page.evaluate(() => (window as unknown as { e2eSets: Array<{ switchyardServer?: string }> }).e2eSets)
  await page.goto('/settings')
  const field = page.locator('[data-switchyard-server]')
  const input = field.locator('input')
  const save = page.locator('[data-save-settings]')
  await expect(input).toBeEnabled()
  await expect(field).not.toContainText(words)

  // Another machine, plain: refused at save, the words under the field and at the top, nothing sent to the app.
  await input.fill('http://yard.example:8080')
  await expect(field).not.toContainText(words)
  await save.click()
  await expect(field).toContainText(words)
  await expect(page.locator('[data-settings-error]')).toContainText(words)
  expect(await sets()).toEqual([])
  // Changing the field takes the words away until the next save.
  await input.fill('http://yard.example:808')
  await expect(field).not.toContainText(words)
  await save.click()
  await expect(field).toContainText(words)
  expect(await sets()).toEqual([])

  // This machine: saved, and kept.
  await input.fill('http://localhost:9090')
  await expect(field).not.toContainText(words)
  await save.click()
  await expect(page.locator('[data-settings-error]')).toHaveCount(0)
  await expect(field).not.toContainText(words)
  await expect.poll(async () => (await sets()).map((s) => s.switchyardServer)).toEqual(['http://localhost:9090'])
  await page.reload()
  await expect(page.locator('[data-switchyard-server] input')).toHaveValue('http://localhost:9090')
  await expect(page.locator('[data-switchyard-server]')).not.toContainText(words)

  // Settings saved with one before the app refused it: the words as the page opens.
  await page.evaluate(() => {
    const s = JSON.parse(sessionStorage.getItem('e2e.settings')!)
    sessionStorage.setItem('e2e.settings', JSON.stringify({ ...s, switchyardServer: 'http://192.168.1.10:8080' }))
  })
  await page.reload()
  await expect(page.locator('[data-switchyard-server] input')).toHaveValue('http://192.168.1.10:8080')
  await expect(page.locator('[data-switchyard-server]')).toContainText(words)
  // An https one is saved.
  await page.locator('[data-switchyard-server] input').fill('https://yard.example')
  await page.locator('[data-save-settings]').click()
  await expect.poll(async () => (await sets()).map((s) => s.switchyardServer)).toEqual(['http://localhost:9090', 'https://yard.example'])
  await expect(page.locator('[data-switchyard-server]')).not.toContainText(words)
})
