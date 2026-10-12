import { spawn, type ChildProcess } from 'node:child_process'
import { openSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'
import { serverBinary } from './server'

// A session another machine hosts (`conductor host`, its terminal through
// the server's relay) ends while a guest watches it through a view link. Its
// end takes its links with it, and the guest is told the session ended, as a
// server session's guest is: the terminal's overlay and the chat say so, and
// nothing says the link was revoked. (That the guest is first given the
// session's last output is checked against the frames themselves, in Go:
// internal/api/hostend_test.go.)
let host: ChildProcess | null = null

test.afterAll(() => {
  host?.kill('SIGKILL')
})

test("a guest on a hosted session's link is told the session ended as it ends, not that its link was revoked", async ({ api, state, browser }) => {
  const start = join(state.root, 'hosted-end-start')
  const log = openSync(join(state.root, 'hosted-end.log'), 'a')
  // Waits for the start file, prints, and exits.
  const script = 'while [ ! -e "$1" ]; do sleep 0.05; done; seq 1 2000; printf "the %s line\\n" final'
  host = spawn(serverBinary(), ['host', '--server', state.baseURL, '--token', state.hostToken, '--host-name', 'e2e-laptop', '--name', 'hosted-end', '--cwd', state.home, '--relay-only', '--no-local', '--', '/bin/sh', '-c', script, 'sh', start], {
    env: { PATH: process.env.PATH ?? '/usr/bin:/bin', LANG: 'C.UTF-8', HOME: state.home },
    stdio: ['ignore', log, log],
  })
  let id = ''
  await expect
    .poll(
      async () => {
        const list = await api.ok<{ sessions?: Session[] } | Session[]>('GET', '/api/sessions')
        const all = Array.isArray(list) ? list : (list.sessions ?? [])
        id = all.find((s) => s.name === 'hosted-end' && s.status === 'running')?.id ?? ''
        return id
      },
      { timeout: 30_000, message: 'the hosted session is listed' },
    )
    .not.toBe('')
  const { token } = await api.ok<{ token: string }>('POST', `/api/sessions/${id}/links`, { role: 'view', label: 'guest' })

  const guestCtx = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Priya'))
  const guest = await guestCtx.newPage()
  await guest.goto(`/join/${token}`)
  await guest.getByRole('button', { name: 'Join session' }).click()
  const badge = guest.locator('[data-transport-state]').first()
  await expect(badge).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
  await expect(badge).toHaveAttribute('data-transport-kind', 'relay')

  writeFileSync(start, '')
  // The terminal's overlay names the end, with the error's code below it; the chat says the session ended.
  await expect(guest.locator('p').filter({ hasText: /^session ended$/ })).toBeVisible({ timeout: 20_000 })
  await expect(guest.locator('p').filter({ hasText: /^session_ended$/ })).toBeVisible()
  await expect(guest.locator('[data-chat-ended]')).toContainText('This session ended')
  await expect(badge).toHaveAttribute('data-transport-state', 'closed')
  await expect(guest.getByText(/link revoked|link was revoked/)).toHaveCount(0)
  await expect.poll(async () => (await api.session(id)).status, { timeout: 15_000 }).toBe('exited')
  await guestCtx.close()
})
