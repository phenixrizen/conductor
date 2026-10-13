import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Browser, BrowserContext, Locator, Page } from '@playwright/test'
import { type Api, expect, test, type Session } from './fixtures'

// A line a link's controller sent is typed into the session, and the link is
// revoked before its Enter: the Enter is never written, the session's view
// says so in plain words and takes no reply, and nothing but a person's typing
// in the terminal ends it (the pointer moved over it does not); typing clears
// the words, and a reply sent before then goes, a line of its own.
//
// Whatever renderer the browser has: with WebGL the screen is a canvas and
// holds no text, so what the agent holds is read from the stub's files (its
// TUI mode's line not yet entered, the mouse reports it read, whether it
// spins, its transcript of the lines entered), never from the screen.
const HELD = 'Text from a link that was let go was typed and not entered: type in the terminal to continue'

/** The stub's files for the agent session of s: each read as it is now, '' before it is there. */
function stubFiles(home: string, s: Session) {
  const path = (ext: string) => join(home, '.stub-sessions', `${s.agentSession?.id ?? ''}${ext}`)
  const file = (ext: string) => () => (existsSync(path(ext)) ? readFileSync(path(ext), 'utf8') : '')
  /** The whole lines of a file the stub appends to (a last one still being written left out). */
  const records = (ext: string) => {
    const log = file(ext)()
    return log.slice(0, log.lastIndexOf('\n') + 1).split('\n').slice(0, -1)
  }
  /** The last whole record of a file the stub appends one to as what it says changes: what is now ('' before any). */
  const now = (ext: string) => () => records(ext).at(-1) ?? ''
  const typed = file('.txt')
  return {
    /** Whether the stub reads as a TUI (its line's file is there). */
    tui: () => existsSync(path('.line')),
    /** Ends the stub's spin at its next dot: its screen goes quiet, and a line entered meanwhile is answered. */
    stopSpin: () => writeFileSync(path('.spin'), 'stop\n'),
    /** The stub's transcript: the lines entered, one a line. */
    typedLines: typed,
    /** Whether the lines entered hold line, the whole of one. */
    entered: (line: string) => typed().split('\n').includes(line),
    /** The line typed into the stub and not yet entered. */
    pending: now('.line'),
    /** How many mouse reports the stub has read. */
    mice: () => Number(now('.mouse')() || '0'),
    /** "spinning" while the stub draws, "done" once it is. */
    spin: () => file('.spin')().trim(),
    /** The Enters the stub has taken, oldest first: when (ms since the epoch, on this machine's clock) and the line's text. */
    enters: () =>
      records('.entered').map((l) => {
        const sp = l.indexOf(' ')
        return { at: Number(l.slice(0, sp)) / 1000, line: l.slice(sp + 1) }
      }),
  }
}

/** What holdOne returns: the session, the owner's view's reply bar and terminal, the guest's context to close, the stub's files. */
type Held = { s: Session; bar: Locator; terminal: Locator; guestCtx: BrowserContext; stub: ReturnType<typeof stubFiles> }

/**
 * holdOne brings a session to the held turn: a guest joins through a control link, the stub reads as the agents' TUIs do (mouse
 * tracking on) and draws without answering until the test stops it, the guest's reply is typed into it and its link revoked
 * before its Enter. With reply, the owner replies from its own view just before the revoke, a reply that waits for the session's
 * turn.
 *
 * The revoke has to come between the text and its Enter, which Conductor writes once it has looked at the screen: a quarter
 * of a second and at most two more while the stub draws. A machine too busy to revoke in that time sees the Enter first: when
 * the stub had taken the guest's Enter before the revoke was asked for (its log's time against the test's, one clock), that
 * session is put aside and the setup done again, three times at most; an Enter taken any later fails the test, for it may have
 * been written after the revoke. Once held, every check is made.
 */
async function holdOne(page: Page, api: Api, browser: Browser, home: string, reply?: string): Promise<Held> {
  for (let attempt = 1; attempt <= 3; attempt++) {
    const held = await tryHold(page, api, browser, home, reply)
    if (held) return held
    test.info().annotations.push({ type: 'setup again', description: `try ${attempt}: the guest's Enter was taken before the revoke was asked for` })
  }
  throw new Error("three tries, and each time the guest's Enter was taken before the revoke was asked for: the machine is too slow for the setup")
}

/** tryHold is one try of holdOne: null when the guest's Enter was taken before the revoke was asked for (the session stopped). */
async function tryHold(page: Page, api: Api, browser: Browser, home: string, reply?: string): Promise<Held | null> {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'held line' })
  const stub = stubFiles(home, s)
  const guestCtx = await browser.newContext({ viewport: { width: 1280, height: 800 } })
  const { token, link } = await api.ok<{ token: string; link: { id: string } }>('POST', `/api/sessions/${s.id}/links`, { role: 'control' })

  // A guest joins through the link.
  await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Priya'))
  const guest = await guestCtx.newPage()
  await guest.goto(`/join/${token}`)
  await guest.getByRole('button', { name: 'Join session' }).click()
  await expect(guest.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 15_000 })

  // The owner's view of the session, once its connection is open (keys typed before are dropped). The stub reads as Claude
  // Code's and Codex's TUIs do, mouse tracking on, and draws, answering nothing, as an agent at work does, until the test
  // stops it (at most 90 s): a submission's Enter waits for a quiet screen as long as Conductor's look does, and no longer.
  await page.goto(`/sessions/${encodeURIComponent(s.id)}`)
  await expect(page.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
  const terminal = page.locator('.terminal-host .xterm-helper-textarea').first()
  await terminal.focus()
  await page.keyboard.type('stub tui on')
  await page.keyboard.press('Enter')
  await expect.poll(stub.tui, { timeout: 30_000, message: 'the stub reads as a TUI' }).toBe(true)
  await page.keyboard.type('stub spin 90')
  await page.keyboard.press('Enter')
  await expect.poll(stub.spin, { timeout: 10_000, message: 'the stub draws' }).toBe('spinning')

  // A question with no choices: the reply bars offer a reply. The owner's, with reply, is written now and sent once the
  // guest's text is typed.
  await api.ok('POST', `/api/sessions/${s.id}/attention`, { state: 'needs_input', message: 'Which name?' })
  const bar = page.locator('[data-quick-reply]')
  if (reply) await bar.locator('input').fill(reply)
  const guestBar = guest.locator('[data-quick-reply]')
  await expect(guestBar).toBeVisible({ timeout: 15_000 })
  await guestBar.locator('input').fill('guest line')
  await guestBar.getByRole('button', { name: 'Send' }).click()
  // Its text is typed: Conductor writes it at once, so the stub's first byte of it says so. The link is revoked before its
  // Enter, nothing slow in between.
  await expect.poll(stub.pending, { timeout: 10_000, intervals: [20], message: 'the guest line is typed' }).not.toBe('')
  if (reply) await bar.locator('input').press('Enter')
  const revokeAsked = Date.now()
  await api.ok('DELETE', `/api/sessions/${s.id}/links/${link.id}`)
  // Held, or the guest's Enter was taken.
  const guestEnter = () => stub.enters().find((e) => e.line === 'guest line')
  const outcome = async () =>
    (await page.locator('[data-quick-reply][data-held="true"]').count()) > 0 ? 'held' : guestEnter() ? 'entered' : 'waiting'
  await expect.poll(outcome, { timeout: 10_000, message: 'the turn is held' }).not.toBe('waiting')
  if ((await outcome()) !== 'held') {
    const at = guestEnter()!.at
    expect(at, "the guest's Enter was taken after the revoke was asked for: it may have been written after it").toBeLessThan(revokeAsked)
    await guestCtx.close()
    await api.stopSession(s.id)
    return null
  }
  await expect.poll(stub.pending, { timeout: 10_000, message: 'the guest line is typed whole' }).toBe('guest line')
  return { s, bar, terminal, guestCtx, stub }
}

test('a line typed for a link that is let go holds the session: the view says so and takes no reply, the pointer ends nothing, typing does', async ({ page, api, browser, state }) => {
  const { s, bar, terminal, guestCtx, stub } = await holdOne(page, api, browser, state.home)
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

    // The pointer moved over the terminal reaches the agent (its mouse reports) and types nothing: the hold and its words
    // stay, and so does the guest's line, not entered.
    const before = stub.mice()
    const box = (await page.locator('.terminal-host .xterm-screen').first().boundingBox())!
    for (let i = 0; i < 6; i++) await page.mouse.move(box.x + 40 + i * 30, box.y + 40 + i * 10, { steps: 4 })
    await expect.poll(stub.mice, { timeout: 10_000, message: 'the pointer reached the agent' }).toBeGreaterThan(before)
    await page.waitForTimeout(1_000)
    await expect(bar).toHaveAttribute('data-held', 'true')
    await expect(bar).toContainText(HELD)
    expect((await api.session(s.id)).attention).toMatchObject({ source: 'submit', message: HELD })
    expect(stub.pending()).toBe('guest line')

    // Typing in the terminal (Ctrl+U, the leftover line killed) clears the words.
    await terminal.focus()
    await page.keyboard.press('Control+U')
    await expect(page.getByText(HELD)).toHaveCount(0, { timeout: 10_000 })
    await expect.poll(async () => (await api.session(s.id)).attention?.source, { timeout: 10_000 }).not.toBe('submit')
    await expect.poll(stub.pending, { timeout: 10_000, message: 'Ctrl+U cleared the line' }).toBe('')
    // The agent's work done, a line typed in the terminal is the stub's next: the guest's was never entered.
    stub.stopSpin()
    await expect.poll(stub.spin, { timeout: 10_000 }).toBe('done')
    await page.keyboard.type('after')
    await page.keyboard.press('Enter')
    await expect.poll(() => stub.entered('after'), { timeout: 30_000, message: 'the typed line was entered' }).toBe(true)
    expect(stub.typedLines()).not.toContain('guest line')
    expect(stub.enters().map((e) => e.line)).not.toContain('guest line')
  } finally {
    await guestCtx.close()
    await api.stopSession(s.id)
  }
})

test('a reply waiting for the held turn goes once someone types in the terminal, a line of its own', async ({ page, api, browser, state }) => {
  const { s, bar, terminal, guestCtx, stub } = await holdOne(page, api, browser, state.home, 'owner line')
  try {
    // The reply was sent (its box emptied) and waits: nothing of it is typed while the turn is held, the guest's line alone
    // is. The agent's work is done meanwhile, its screen quiet, so that the reply goes as soon as it may: within its ten
    // seconds.
    await expect(bar).toContainText(HELD)
    await expect(bar.locator('input')).toHaveValue('')
    stub.stopSpin()
    await expect.poll(stub.spin, { timeout: 10_000 }).toBe('done')
    expect(stub.pending()).toBe('guest line')
    expect(stub.typedLines()).not.toContain('owner line')
    // Typing in the terminal (Ctrl+U, the leftover line killed) clears the words, and the waiting reply goes, a line of its
    // own: the guest's never entered.
    await terminal.focus()
    await page.keyboard.press('Control+U')
    await expect(page.getByText(HELD)).toHaveCount(0, { timeout: 10_000 })
    await expect.poll(() => stub.entered('owner line'), { timeout: 30_000, message: 'the reply was entered' }).toBe(true)
    expect(stub.typedLines()).not.toContain('guest line')
    expect(stub.enters().map((e) => e.line)).not.toContain('guest line')
  } finally {
    await guestCtx.close()
    await api.stopSession(s.id)
  }
})
