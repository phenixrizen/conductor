import { expect, test, type Session } from './fixtures'

// An invite (conductor://…) the desktop app is handed while its window is
// open comes to the page over the bridge, a stand-in for
// desktop/src/preload.ts here: the workbench routes to its join page in
// place, with no page load, and drops what a join page would not take.
type Invited = { __invite?: (invite: unknown) => void; __loaded?: string }

test('an invite handed over the bridge opens the join page in place', async ({ page, api, state }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'invited' })
  const { token } = await api.ok<{ token: string }>('POST', `/api/sessions/${s.id}/links`, { role: 'view' })
  await page.addInitScript(
    ({ token, url }) => {
      const w = window as unknown as Invited & { conductorDesktop: unknown }
      w.conductorDesktop = {
        version: 'e2e',
        platform: 'linux',
        token: async () => token,
        openExternal: async () => {},
        settings: { get: async () => ({}), set: async () => ({}) },
        restartServer: async () => {},
        openInBrowser: async () => {},
        showLog: async () => {},
        serverState: async () => ({ state: 'running', url, failures: 0 }),
        notice: async () => '',
        ice: async () => ({ forwarding: false, port: 0, publicIp: '', wslAddress: '', firewall: 'unknown' }),
        allowIceFirewall: async () => 'unknown',
        versions: async () => ({ app: 'e2e', electron: '', node: '', chrome: '', server: '' }),
        onServerState: () => () => {},
        onInvite: (cb: (invite: unknown) => void) => {
          w.__invite = cb
          return () => {}
        },
      }
    },
    { token: state.token, url: state.baseURL },
  )
  await page.goto('/')
  await expect(page.locator('[data-session-list]').first()).toBeVisible()
  await expect.poll(() => page.evaluate(() => typeof (window as unknown as Invited).__invite)).toBe('function')
  await page.evaluate(() => ((window as unknown as Invited).__loaded = 'once'))
  const entries = await page.evaluate(() => history.length)
  // What a join page would not take goes nowhere: plain http off loopback, a token that is a path.
  await page.evaluate((t) => (window as unknown as Invited).__invite!({ server: 'http://switchyard.example.net', token: t }), token)
  await page.evaluate(() => (window as unknown as Invited).__invite!({ server: 'https://switchyard.example.net', token: '../settings?aaaaaaaaaaaaaaaa' }))
  // The invite itself: the join page, told to signal to the server it names, in the same page.
  await page.evaluate(({ server, token }) => (window as unknown as Invited).__invite!({ server, token }), { server: state.baseURL, token })
  await expect(page).toHaveURL(`${state.baseURL}/join/${token}?server=${encodeURIComponent(state.baseURL)}`)
  await expect(page.getByRole('heading', { name: 'Join invited' })).toBeVisible({ timeout: 15_000 })
  expect(await page.evaluate(() => (window as unknown as Invited).__loaded)).toBe('once')
  expect(await page.evaluate(() => history.length)).toBe(entries + 1)
  await api.stopSession(s.id)
})
