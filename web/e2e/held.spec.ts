import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// A line a link's controller sent is typed into the session, and the link is
// revoked before its Enter: the Enter is never written, the session's view
// says so in plain words and takes no reply, and a reply sent before waits;
// typing in the terminal clears the words, and the waiting reply goes, a
// line of its own.
const HELD = 'Text from a link that was let go was typed and not entered: type in the terminal to continue'

test('a line typed for a link that is let go waits for the terminal, and a reply sent meanwhile then goes', async ({ page, api, browser, state }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'held line' })
  const guestCtx = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  try {
    const transcript = join(state.home, '.stub-sessions', `${s.agentSession?.id ?? ''}.txt`)
    const typedLines = () => (existsSync(transcript) ? readFileSync(transcript, 'utf8') : '')
    const { token, link } = await api.ok<{ token: string; link: { id: string } }>('POST', `/api/sessions/${s.id}/links`, { role: 'control' })

    // A guest joins through the link.
    await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Priya'))
    const guest = await guestCtx.newPage()
    await guest.goto(`/join/${token}`)
    await guest.getByRole('button', { name: 'Join session' }).click()
    await expect(guest.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 15_000 })

    // The owner's view of the session. The stub draws for a while, reading
    // nothing, as an agent at work does: a line's Enter waits for the screen
    // to be quiet, as long as Conductor's look does.
    await page.goto(`/sessions/${encodeURIComponent(s.id)}`)
    const rows = page.locator('.terminal-host .xterm-rows').first()
    await expect(rows).toContainText('conductor e2e stub', { timeout: 30_000 })
    const terminal = page.locator('.terminal-host .xterm-helper-textarea').first()
    await terminal.focus()
    await page.keyboard.type('stub spin 12')
    await page.keyboard.press('Enter')
    await expect(rows).toContainText('.....', { timeout: 10_000 })

    // A question with no choices: the reply bars offer a reply.
    await api.ok('POST', `/api/sessions/${s.id}/attention`, { state: 'needs_input', message: 'Which name?' })
    const guestBar = guest.locator('[data-quick-reply]')
    await expect(guestBar).toBeVisible({ timeout: 15_000 })
    await guestBar.locator('input').fill('guest line')
    await guestBar.getByRole('button', { name: 'Send' }).click()
    // Its text is typed (the terminal echoes it). The owner replies meanwhile: the reply waits for its turn,
    // which the guest's line holds. The link is revoked before the guest's Enter.
    await expect.poll(async () => (await rows.innerText()).includes('guest line'), { timeout: 10_000, intervals: [50] }).toBe(true)
    const bar = page.locator('[data-quick-reply]')
    await bar.locator('input').fill('owner line')
    await bar.getByRole('button', { name: 'Send' }).click()
    await api.ok('DELETE', `/api/sessions/${s.id}/links/${link.id}`)

    // The owner's view says the session waits for the terminal, in plain words, and takes no reply meanwhile.
    await expect(bar).toHaveAttribute('data-held', 'true', { timeout: 10_000 })
    await expect(bar).toContainText('Type in the terminal to go on')
    await expect(bar).toContainText(HELD)
    await expect(bar.locator('input')).toBeDisabled()
    await expect(bar.locator('input')).toHaveAttribute('placeholder', 'Type in the terminal first')
    expect((await api.session(s.id)).attention).toMatchObject({ state: 'needs_input', source: 'submit', message: HELD })
    // The sidebar's row says it too, and takes no reply either.
    const row = page.locator(`[data-session-list="sidebar"] [data-sidebar-row="s:${s.id}"]`).first()
    await expect(row.locator('[data-row-prompt]')).toHaveText(HELD, { timeout: 10_000 })
    await expect(row.locator('[data-row-reply] input, input[data-row-reply]').first()).toBeDisabled()

    // The queued reply waits: nothing of it is typed.
    await page.waitForTimeout(1_000)
    await expect(rows).not.toContainText('owner line')
    await expect(bar).toContainText(HELD)

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
