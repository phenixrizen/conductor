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
  await expect(about.locator('[data-about-copyright]')).toContainText('the Conductor authors')
  await expect(about.locator('[data-about-credit]')).toContainText('Sponsored and maintained by RockSolid Labs')
  await expect(about.locator('[data-about-credit] a')).toHaveAttribute('href', 'https://rocksolidlabs.io')
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
