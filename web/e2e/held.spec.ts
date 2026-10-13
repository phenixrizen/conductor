import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Browser, Page } from '@playwright/test'
import { type Api, expect, test, type Session } from './fixtures'

// A line a link's controller sent is typed into the session, and the link is
// revoked before its Enter: the Enter is never written, the session's view
// says so in plain words and takes no reply, and nothing but a person's typing
// in the terminal ends it (the pointer moved over it does not); typing clears
// the words, and a reply sent before then goes, a line of its own.
const HELD = 'Text from a link that was let go was typed and not entered: type in the terminal to continue'

/**
 * holdOne brings a session to the held turn: a guest joins through a control link, the stub draws without reading (with mouse
 * tracking on), the guest's reply is typed (the terminal echoes it) and its link revoked before its Enter. With reply, the owner
 * replies from its own view just before the revoke, a reply that waits for the session's turn. It returns the owner's view's
 * terminal rows, its reply bar, the guest's context to close, and the lines the stub entered.
 */
async function holdOne(page: Page, api: Api, browser: Browser, home: string, reply?: string) {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'held line' })
  const guestCtx = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const transcript = join(home, '.stub-sessions', `${s.agentSession?.id ?? ''}.txt`)
  const typedLines = () => (existsSync(transcript) ? readFileSync(transcript, 'utf8') : '')
  const { token, link } = await api.ok<{ token: string; link: { id: string } }>('POST', `/api/sessions/${s.id}/links`, { role: 'control' })

  // A guest joins through the link.
  await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Priya'))
  const guest = await guestCtx.newPage()
  await guest.goto(`/join/${token}`)
  await guest.getByRole('button', { name: 'Join session' }).click()
  await expect(guest.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 15_000 })

  // The owner's view of the session. The stub turns mouse tracking on, as the agents do, and draws for a while, reading
  // nothing, as an agent at work does: a line's Enter waits for the screen to be quiet, as long as Conductor's look does.
  await page.goto(`/sessions/${encodeURIComponent(s.id)}`)
  const rows = page.locator('.terminal-host .xterm-rows').first()
  await expect(rows).toContainText('conductor e2e stub', { timeout: 30_000 })
  const terminal = page.locator('.terminal-host .xterm-helper-textarea').first()
  await terminal.focus()
  await page.keyboard.type('stub mouse on')
  await page.keyboard.press('Enter')
  await page.keyboard.type('stub spin 12')
  await page.keyboard.press('Enter')
  await expect(rows).toContainText('.....', { timeout: 10_000 })

  // A question with no choices: the reply bars offer a reply.
  await api.ok('POST', `/api/sessions/${s.id}/attention`, { state: 'needs_input', message: 'Which name?' })
  const guestBar = guest.locator('[data-quick-reply]')
  await expect(guestBar).toBeVisible({ timeout: 15_000 })
  await guestBar.locator('input').fill('guest line')
  await guestBar.getByRole('button', { name: 'Send' }).click()
  // Its text is typed (the terminal echoes it); the link is revoked before its Enter.
  await expect.poll(async () => (await rows.innerText()).includes('guest line'), { timeout: 10_000, intervals: [50] }).toBe(true)
  const bar = page.locator('[data-quick-reply]')
  if (reply) {
    await bar.locator('input').fill(reply)
    await bar.getByRole('button', { name: 'Send' }).click()
  }
  await api.ok('DELETE', `/api/sessions/${s.id}/links/${link.id}`)
  await expect(bar).toHaveAttribute('data-held', 'true', { timeout: 10_000 })
  return { s, rows, bar, terminal, guestCtx, typedLines }
}

test('a line typed for a link that is let go holds the session: the view says so and takes no reply, the pointer ends nothing, typing does', async ({ page, api, browser, state }) => {
  const { s, rows, bar, terminal, guestCtx, typedLines } = await holdOne(page, api, browser, state.home)
  try {
    // The owner's view says the session waits for the terminal, in plain words, and takes no reply meanwhile.
    await expect(bar).toContainText('Type in the terminal to go on')
    await expect(bar).toContainText(HELD)
    await expect(bar.locator('input')).toBeDisabled()
    await expect(bar.locator('input')).toHaveAttribute('placeholder', 'Type in the terminal first')
    expect((await api.session(s.id)).attention).toMatchObject({ state: 'needs_input', source: 'submit', message: HELD })
    // The sidebar's row says it too, and takes no reply either.
    const row = page.locator(`[data-session-list="sidebar"] [data-sidebar-row="s:${s.id}"]`).first()
    await expect(row.locator('[data-row-prompt]')).toHaveText(HELD, { timeout: 10_000 })
    await expect(row.locator('[data-row-reply] input, input[data-row-reply]').first()).toBeDisabled()

    // The pointer moved over the terminal types nothing: the hold and its words stay.
    const box = (await page.locator('.terminal-host .xterm-screen').first().boundingBox())!
    for (let i = 0; i < 6; i++) await page.mouse.move(box.x + 40 + i * 30, box.y + 40 + i * 10, { steps: 4 })
    await page.waitForTimeout(1_000)
    await expect(bar).toHaveAttribute('data-held', 'true')
    await expect(bar).toContainText(HELD)
    expect((await api.session(s.id)).attention).toMatchObject({ source: 'submit', message: HELD })

    // Typing in the terminal (Ctrl+U, the leftover line killed) clears the words.
    await terminal.focus()
    await page.keyboard.press('Control+U')
    await expect(page.getByText(HELD)).toHaveCount(0, { timeout: 10_000 })
    await expect.poll(async () => (await api.session(s.id)).attention?.source, { timeout: 10_000 }).not.toBe('submit')
    // A line typed in the terminal is the stub's next: the guest's was never entered.
    await page.keyboard.type('after')
    await page.keyboard.press('Enter')
    await expect.poll(typedLines, { timeout: 30_000, message: 'the typed line was entered' }).toContain('after')
    expect(typedLines()).not.toContain('guest line')
    await expect(rows).not.toContainText('got: guest line')
  } finally {
    await guestCtx.close()
    await api.stopSession(s.id)
  }
})

test('a reply waiting for the held turn goes once someone types in the terminal, a line of its own', async ({ page, api, browser, state }) => {
  const { s, rows, bar, terminal, guestCtx, typedLines } = await holdOne(page, api, browser, state.home, 'owner line')
  try {
    // The reply waits: nothing of it is typed while the turn is held.
    await expect(bar).toContainText(HELD)
    await expect(rows).not.toContainText('owner line')
    // Typing in the terminal (Ctrl+U, the leftover line killed) clears the words, and the waiting reply goes.
    await terminal.focus()
    await page.keyboard.press('Control+U')
    await expect(page.getByText(HELD)).toHaveCount(0, { timeout: 10_000 })
    await expect(rows).toContainText('owner line', { timeout: 10_000 })
    await expect.poll(typedLines, { timeout: 30_000, message: 'the reply was entered' }).toContain('owner line')
    expect(typedLines()).not.toContain('guest line')
  } finally {
    await guestCtx.close()
    await api.stopSession(s.id)
  }
})
