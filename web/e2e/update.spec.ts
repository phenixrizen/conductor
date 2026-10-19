import type { Page } from '@playwright/test'
import { expect, test } from './fixtures'

// The update the desktop app downloaded (#290): never a dialog of the app's
// own, which every check that found the same download used to open again.
// The workbench is told once per version, in a toast that offers the
// restart, and keeps a button in the sidebar's header until it installs;
// the restart says what it ends before the app quits to install; an update
// that did not install is said once. The app is a stub bridge here, which
// hands the announcement out once, as the app does.
test.describe.configure({ mode: 'serial' })

type Told = { ready: string; announce: boolean; failed: string }

async function stubApp(page: Page, token: string, url: string, init: Told) {
  await page.addInitScript(
    ({ token, url, init }) => {
      const w = window as unknown as { conductorDesktop: unknown; e2eUpdate: Told; e2eFound?: () => void }
      w.e2eUpdate = JSON.parse(sessionStorage.getItem('e2e.update') ?? JSON.stringify(init))
      w.conductorDesktop = {
        version: 'e2e',
        platform: 'linux',
        token: async () => token,
        openExternal: async () => {},
        settings: { get: async () => ({}), set: async (p: object) => p },
        restartServer: async () => {},
        openInBrowser: async () => {},
        showLog: async () => sessionStorage.setItem('e2e.log', '1'),
        serverState: async () => ({ state: 'running', url, failures: 0 }),
        notice: async () => '',
        ice: async () => ({ forwarding: false, port: 0, publicIp: '', wslAddress: '', firewall: 'unknown' }),
        allowIceFirewall: async () => 'unknown',
        versions: async () => ({ app: 'e2e', electron: '', node: '', chrome: '', server: '' }),
        onServerState: () => () => {},
        // The app's UpdateNotice: the announcement and a failed install go to the first page that asks, once.
        update: async () => {
          const told = { ...w.e2eUpdate }
          w.e2eUpdate = { ...w.e2eUpdate, announce: false, failed: '' }
          sessionStorage.setItem('e2e.update', JSON.stringify(w.e2eUpdate))
          return told
        },
        onUpdate: (cb: () => void) => {
          w.e2eFound = cb
          return () => {}
        },
        installUpdate: async () => sessionStorage.setItem('e2e.installed', String(Number(sessionStorage.getItem('e2e.installed') ?? '0') + 1)),
      }
    },
    { token, url, init },
  )
}

/** The app's check finds an update downloaded: told the version (and that it is new), it says so to the page. */
async function found(page: Page, told: Told) {
  await page.evaluate((told) => {
    const w = window as unknown as { e2eUpdate: Told; e2eFound?: () => void }
    w.e2eUpdate = told
    sessionStorage.setItem('e2e.update', JSON.stringify(told))
    w.e2eFound?.()
  }, told)
}

const READY = 'Conductor 0.7.0-rc.12 is ready'

test('an update found is said once, in a toast, and kept in the header until it installs', async ({ page, state }) => {
  await stubApp(page, state.token, state.baseURL, { ready: '', announce: false, failed: '' })
  await page.goto('/yard')
  await expect(page.locator('[data-sidebar-menus]').first()).toBeVisible()
  await expect(page.locator('[data-header-update]')).toHaveCount(0)

  await found(page, { ready: '0.7.0-rc.12', announce: true, failed: '' })
  const toast = page.locator('li').filter({ hasText: READY })
  await expect(toast).toHaveCount(1)
  await expect(toast).toContainText('It installs when you quit Conductor')
  await expect(toast.getByRole('button', { name: 'Restart to update' })).toBeVisible()
  await expect(page.locator('[data-header-update]').first()).toBeVisible()

  // The checks that find the same download again, six hours apart, say nothing more.
  await found(page, { ready: '0.7.0-rc.12', announce: false, failed: '' })
  await found(page, { ready: '0.7.0-rc.12', announce: false, failed: '' })
  await page.waitForTimeout(500)
  await expect(page.locator('li').filter({ hasText: READY })).toHaveCount(1)

  // A page loaded later is told the update is ready, not said again.
  await page.reload()
  await expect(page.locator('[data-header-update]').first()).toBeVisible()
  await page.waitForTimeout(500)
  await expect(page.locator('li').filter({ hasText: READY })).toHaveCount(0)
})

test('restart to update says what it ends, Later leaves it, and the restart installs', async ({ page, state }) => {
  await stubApp(page, state.token, state.baseURL, { ready: '0.7.0-rc.12', announce: false, failed: '' })
  await page.goto('/yard')
  await page.locator('[data-header-update]').first().click()
  const modal = page.getByRole('dialog')
  await expect(modal).toContainText('Restart to update to 0.7.0-rc.12?')
  await expect(modal).toContainText('Conductor quits to install 0.7.0-rc.12, and its server stops.')
  await expect(modal).toContainText(' on this computer')
  await modal.getByRole('button', { name: 'Later' }).click()
  await expect(modal).toHaveCount(0)
  expect(await page.evaluate(() => sessionStorage.getItem('e2e.installed'))).toBeNull()

  await page.locator('[data-header-update]').first().click()
  await page.locator('[data-update-restart]').click()
  await expect.poll(() => page.evaluate(() => sessionStorage.getItem('e2e.installed'))).toBe('1')
})

test('an update that did not install is said once, with the way to the log', async ({ page, state }) => {
  await stubApp(page, state.token, state.baseURL, { ready: '', announce: false, failed: '0.7.0-rc.12' })
  await page.goto('/yard')
  const toast = page.locator('li').filter({ hasText: 'Conductor 0.7.0-rc.12 did not install' })
  await expect(toast).toHaveCount(1)
  await toast.getByRole('button', { name: 'Show the log' }).click()
  await expect.poll(() => page.evaluate(() => sessionStorage.getItem('e2e.log'))).toBe('1')
  await page.reload()
  await expect(page.locator('[data-sidebar-menus]').first()).toBeVisible()
  await page.waitForTimeout(500)
  await expect(page.locator('li').filter({ hasText: 'did not install' })).toHaveCount(0)
})
