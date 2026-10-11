import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { expect, test, type Api, type Session } from './fixtures'

// The mouse while the program asks for it (round 15): Codex's TUI turns on
// mouse reporting, and then clicks, drags, the wheel and a right-click are
// the program's, as in Windows Terminal: xterm reports each one and a
// right-click neither pastes nor opens the menu. Shift is the way back to the
// terminal: Shift+right-click opens its menu, Shift+drag selects. Once the
// program turns reporting off, a right-click pastes again.
//
// The program is a few lines of Python that ask for SGR mouse reports
// (1000, 1002, 1006), put a word on the screen's first row and log every
// byte they read to a file; "q" ends them. Two links sit on the rows
// below the word. With "any" they ask for every move too (1003, as Codex's
// and Claude Code's TUIs do); with "plain" the reports come in xterm's
// first encoding (ESC [ M and three bytes), not SGR's.
test.use({ permissions: ['clipboard-read', 'clipboard-write'] })

const APP = `import os, select, sys, termios, time, tty
# With "letgo", the program lets go of the mouse and ends at the first right-button press it reads.
letgo = "letgo" in sys.argv[1:]
# With "any", it asks for every move, a button held or not (1003), after presses and drags, as crossterm asks for them.
anymove = "any" in sys.argv[1:]
# With "plain", it asks for no SGR reports (1006): xterm sends ESC [ M and three bytes.
sgr = "plain" not in sys.argv[1:]
fd = sys.stdin.fileno()
old = termios.tcgetattr(fd)
tty.setraw(fd)
sys.stdout.write("\\x1b[2J\\x1b[Hselect-me-42\\r\\nhttps://example.test/shift-click\\r\\nhttps://example.test/after-select\\r\\n")
sys.stdout.write("\\x1b[?1000h\\x1b[?1002h" + ("\\x1b[?1003h" if anymove else "") + ("\\x1b[?1006h" if sgr else ""))
sys.stdout.flush()
open("ready.txt", "w").write("ready")
log = open("got.txt", "ab", buffering=0)
end = time.time() + 120
while time.time() < end:
    if select.select([fd], [], [], 0.2)[0]:
        b = os.read(fd, 4096)
        if b == b"q":
            break
        log.write(b)
        if letgo and b"\\x1b[<2;" in b:
            break
sys.stdout.write("\\x1b[?1006l\\x1b[?1003l\\x1b[?1002l\\x1b[?1000l")
sys.stdout.flush()
termios.tcsetattr(fd, termios.TCSADRAIN, old)
open("done.txt", "w").write("done")
`

const clip = (page: Page) => page.evaluate(() => navigator.clipboard.readText())
const setClip = (page: Page, text: string) => page.evaluate((t) => navigator.clipboard.writeText(t), text)

/** The middle of a cell of the page's terminal: the screen's box over the session's size. */
async function cell(page: Page, api: Api, id: string, col: number, row: number) {
  const { cols, rows } = (await api.session(id)) as Session & { cols: number; rows: number }
  const box = (await page.locator('.terminal-host .xterm-screen').first().boundingBox())!
  return { x: box.x + ((col + 0.5) * box.width) / cols, y: box.y + ((row + 0.5) * box.height) / rows }
}

/**
 * Waits until xterm has taken the program's request for mouse reports (on) or its letting go (off): xterm marks its element
 * enable-mouse-events while reports are on. The program writing its request is not enough; it reaches the page a moment later.
 */
async function reporting(page: Page, on: boolean) {
  const el = page.locator('.terminal-host .xterm').first()
  if (on) await expect(el).toHaveClass(/enable-mouse-events/, { timeout: 15_000 })
  else await expect(el).not.toHaveClass(/enable-mouse-events/, { timeout: 15_000 })
}

test('while the program holds the mouse, clicks are its own and Shift reaches the terminal', async ({ page, api, state }) => {
  const cwd = join(state.root, 'mouse')
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'app.py'), APP)
  const got = () => (existsSync(join(cwd, 'got.txt')) ? readFileSync(join(cwd, 'got.txt'), 'latin1') : '')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'shell', name: 'mouse', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    const screen = page.locator('.terminal-host .xterm-screen').first()
    await expect(screen).toBeVisible({ timeout: 30_000 })
    await expect(page.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
    await screen.click()
    await page.keyboard.type('python3 app.py\n')
    await expect.poll(() => existsSync(join(cwd, 'ready.txt')), { timeout: 15_000 }).toBe(true)
    const mid = await cell(page, api, s.id, 40, 10)
    await reporting(page, true)

    // A right-click is the program's: a press and a release of the right button, nothing pasted, no menu.
    await setClip(page, 'PASTED-TEXT')
    let before = got().length
    await page.mouse.click(mid.x, mid.y, { button: 'right' })
    await expect.poll(() => got().slice(before)).toMatch(/^\x1b\[<2;\d+;\d+M\x1b\[<2;\d+;\d+m$/)
    await page.waitForTimeout(300)
    expect(got()).not.toContain('PASTED-TEXT')
    await expect(page.getByRole('menu')).toHaveCount(0)

    // The wheel and a drag are reported too.
    before = got().length
    await page.mouse.wheel(0, 200)
    await expect.poll(() => got().slice(before)).toMatch(/\x1b\[<65;\d+;\d+M/)
    before = got().length
    await page.mouse.move(mid.x, mid.y)
    await page.mouse.down()
    await page.mouse.move(mid.x + 60, mid.y + 20, { steps: 4 })
    await page.mouse.up()
    await expect.poll(() => got().slice(before)).toMatch(/\x1b\[<32;\d+;\d+M[\s\S]*\x1b\[<0;\d+;\d+m$/)

    // Shift+right-click opens the terminal's menu and the program hears nothing.
    before = got().length
    await page.keyboard.down('Shift')
    await page.mouse.click(mid.x, mid.y, { button: 'right' })
    await page.keyboard.up('Shift')
    await expect(page.getByRole('menuitem', { name: 'Select all' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menu')).toHaveCount(0)

    // Shift+drag selects the word on the first row, and Ctrl+Shift+C copies it, the program hearing neither.
    const from = await cell(page, api, s.id, 0, 0)
    const to = await cell(page, api, s.id, 12, 0) // the middle of the cell after the word: xterm ends a drag at the nearer cell edge
    await page.keyboard.down('Shift')
    await page.mouse.move(from.x - 2, from.y)
    await page.mouse.down()
    await page.mouse.move(to.x, to.y, { steps: 6 })
    await page.mouse.up()
    await page.keyboard.up('Shift')
    await page.keyboard.press('Control+Shift+C')
    await expect.poll(() => clip(page)).toBe('select-me-42')
    expect(got().length).toBe(before)

    // The keyboard's menu key opens the menu and is never the program's, even after a right press in the terminal let go outside it
    // (no menu event came for that one).
    const screenEl = page.locator('.terminal-host .xterm-screen').first()
    const press = { clientX: Math.round(mid.x), clientY: Math.round(mid.y), button: 2, bubbles: true, cancelable: true, composed: true }
    await screenEl.dispatchEvent('mousedown', { ...press, buttons: 2 })
    await page.locator('body').dispatchEvent('mouseup', { ...press, buttons: 0 })
    await expect.poll(() => got().slice(before)).toMatch(/\x1b\[<2;\d+;\d+M/)
    await page.waitForTimeout(300)
    before = got().length
    await page.keyboard.press('ContextMenu')
    await expect(page.getByRole('menuitem', { name: 'Select all' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menu')).toHaveCount(0)
    await page.waitForTimeout(300)
    expect(got()).not.toContain('PASTED-TEXT')
    expect(got().length).toBe(before)

    // So is a menu event that looks like a click (Firefox on Windows sends the menu key's so), once the menu key went down before it.
    await screenEl.dispatchEvent('mousedown', { ...press, buttons: 2 })
    await page.locator('body').dispatchEvent('mouseup', { ...press, buttons: 0 })
    await expect.poll(() => got().length).toBeGreaterThan(before)
    await page.waitForTimeout(300)
    before = got().length
    await page.locator('.terminal-host textarea').first().dispatchEvent('keydown', { key: 'ContextMenu', code: 'ContextMenu', bubbles: true, cancelable: true })
    await screenEl.dispatchEvent('contextmenu', { ...press, button: 0, buttons: 0 })
    await expect(page.getByRole('menuitem', { name: 'Select all' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menu')).toHaveCount(0)
    await page.waitForTimeout(300)
    expect(got()).not.toContain('PASTED-TEXT')
    expect(got().length).toBe(before)

    // The forcing key is judged at the press: Shift held at the press and let go before the menu event (on Windows it comes after the
    // release) still opens the menu, and xterm reported nothing.
    await screenEl.dispatchEvent('mousedown', { ...press, buttons: 2, shiftKey: true })
    await screenEl.dispatchEvent('mouseup', { ...press, buttons: 0, shiftKey: true })
    await screenEl.dispatchEvent('contextmenu', { ...press, buttons: 0, shiftKey: false })
    await expect(page.getByRole('menuitem', { name: 'Select all' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('menu')).toHaveCount(0)
    expect(got().length).toBe(before)

    // Once the program lets go of the mouse, a right-click pastes again.
    await page.keyboard.press('q')
    await expect.poll(() => existsSync(join(cwd, 'done.txt')), { timeout: 15_000 }).toBe(true)
    rmSync(join(cwd, 'done.txt'))
    await reporting(page, false)
    await setClip(page, 'echo pasted-again > again.txt')
    await screen.click()
    await page.mouse.click(mid.x, mid.y, { button: 'right' })
    await page.keyboard.press('Enter')
    await expect.poll(() => (existsSync(join(cwd, 'again.txt')) ? readFileSync(join(cwd, 'again.txt'), 'utf8') : ''), { timeout: 15_000 }).toBe('pasted-again\n')

    // A program that lets go of the mouse between the press and the menu event (which comes after the release on Windows; Chromium on
    // Linux sends it at the press, so the two events are sent here by hand) had the press: the click stays its own and pastes nothing
    // into the shell that comes back.
    rmSync(join(cwd, 'got.txt'), { force: true })
    rmSync(join(cwd, 'ready.txt'), { force: true })
    await setClip(page, 'echo leaked > leaked.txt')
    await screen.click()
    await page.keyboard.type('python3 app.py letgo\n')
    await expect.poll(() => existsSync(join(cwd, 'ready.txt')), { timeout: 15_000 }).toBe(true)
    await reporting(page, true)
    const at = { clientX: Math.round(mid.x), clientY: Math.round(mid.y), button: 2, buttons: 2, bubbles: true, cancelable: true, composed: true }
    await page.locator('.terminal-host .xterm-screen').first().dispatchEvent('mousedown', at)
    await expect.poll(() => existsSync(join(cwd, 'done.txt')), { timeout: 15_000 }).toBe(true)
    // The page has seen the program let go, so the menu event meets a terminal with no reports on: only the press's hold keeps it.
    await reporting(page, false)
    await page.locator('.terminal-host .xterm-screen').first().dispatchEvent('mouseup', { ...at, buttons: 0 })
    await page.locator('.terminal-host .xterm-screen').first().dispatchEvent('contextmenu', { ...at, buttons: 0 })
    await page.waitForTimeout(500)
    await screen.click()
    await page.keyboard.type('echo after > after.txt\n')
    // A paste would have joined the line typed after it ("echo leaked > leaked.txtecho after > after.txt").
    await expect.poll(() => (existsSync(join(cwd, 'after.txt')) ? readFileSync(join(cwd, 'after.txt'), 'utf8') : ''), { timeout: 15_000 }).toBe('after\n')
  } finally {
    await api.stopSession(s.id)
  }
})

/** The toast a link opens with a plain click (or a Shift+click): its address, and what to do with it. */
const linkToast = (page: Page, url: string) => page.locator('li').filter({ hasText: url })

/** Starts the probe with args in a shell session, on the session's page, and waits until xterm has taken its request for the mouse. */
async function probe(page: Page, api: Api, root: string, name: string, args: string) {
  const cwd = join(root, name)
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'app.py'), APP)
  const got = () => (existsSync(join(cwd, 'got.txt')) ? readFileSync(join(cwd, 'got.txt'), 'latin1') : '')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'shell', name, cwd })
  await page.goto(`/sessions/${s.id}`)
  const screen = page.locator('.terminal-host .xterm-screen').first()
  await expect(screen).toBeVisible({ timeout: 30_000 })
  await expect(page.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
  await screen.click()
  await page.keyboard.type(`python3 app.py ${args}\n`)
  await expect.poll(() => existsSync(join(cwd, 'ready.txt')), { timeout: 15_000 }).toBe(true)
  await reporting(page, true)
  return { s, got }
}

/** Shift+drags over the word on the first row, then moves with Shift held and let go, no button, to rest. */
async function shiftDragWord(page: Page, api: Api, id: string, rest: { x: number; y: number }) {
  const from = await cell(page, api, id, 0, 0)
  const to = await cell(page, api, id, 12, 0) // the middle of the cell after the word: xterm ends a drag at the nearer cell edge
  await page.keyboard.down('Shift')
  await page.mouse.move(from.x - 2, from.y)
  await page.mouse.down()
  await page.mouse.move(to.x, to.y, { steps: 6 })
  await page.mouse.up()
  await page.mouse.move(to.x + 60, to.y + 30, { steps: 4 })
  await page.keyboard.up('Shift')
  await page.mouse.move(rest.x, rest.y, { steps: 4 })
}

/** Opens the terminal's menu with Shift+right-click at at, checks whether its Copy may be taken, and closes it. */
async function copyEnabled(page: Page, at: { x: number; y: number }, enabled: boolean) {
  await page.keyboard.down('Shift')
  await page.mouse.click(at.x, at.y, { button: 'right' })
  await page.keyboard.up('Shift')
  const copy = page.getByRole('menuitem', { name: 'Copy' })
  if (enabled) await expect(copy).toBeEnabled()
  else await expect(copy).toBeDisabled()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menu')).toHaveCount(0)
}

// A program that asks for every move (1003: Codex, Claude Code) has xterm report each move with no button held, Shift or not, and xterm
// takes a report for typing, which clears the selection: the move after a Shift+drag used to clear what it had selected, and the
// menu's Copy was greyed out. Such moves are kept from the program while Shift is held or a selection shows, until a click (the
// program's) clears it; xterm still finds the link under the pointer.
test('over a program that asks for every move, a Shift+drag selection stays until a click, and links still open', async ({ page, api, state }) => {
  const { s, got } = await probe(page, api, state.root, 'mouse-any', 'any')
  try {
    // A move with no button held is the program's (35: a move, no button), up to the cell it ends on.
    const mid = await cell(page, api, s.id, 40, 10)
    await page.mouse.move(mid.x, mid.y, { steps: 4 })
    await expect.poll(() => got()).toMatch(/\x1b\[<35;41;11M$/)
    await setClip(page, 'nothing-copied')
    const before = got().length

    // With Shift held the program hears no move, and xterm still finds the link under the pointer: a Shift+click opens it.
    const first = await cell(page, api, s.id, 12, 1)
    await page.keyboard.down('Shift')
    await page.mouse.move(first.x, first.y, { steps: 4 })
    await page.mouse.click(first.x, first.y)
    await page.keyboard.up('Shift')
    await expect(linkToast(page, 'https://example.test/shift-click')).toBeVisible()

    // Shift+drag selects the word on the first row; the moves after it, Shift held and then let go, leave the selection, and
    // Ctrl+Shift+C copies it. Shift+right-click then shows the menu's Copy to take, and the program has read nothing.
    await shiftDragWord(page, api, s.id, mid)
    await page.keyboard.press('Control+Shift+C')
    await expect.poll(() => clip(page)).toBe('select-me-42')
    await copyEnabled(page, mid, true)
    await page.waitForTimeout(300)
    expect(got().slice(before)).toBe('')

    // A click is the program's, as ever, and clears the selection: the program reads its press and release and not the moves to it.
    // On a link it opens the link too: xterm saw those moves.
    const second = await cell(page, api, s.id, 12, 2)
    await page.mouse.move(second.x, second.y, { steps: 4 })
    await page.mouse.click(second.x, second.y)
    await expect.poll(() => got().slice(before)).toMatch(/^\x1b\[<0;\d+;\d+M\x1b\[<0;\d+;\d+m$/)
    await expect(linkToast(page, 'https://example.test/after-select')).toBeVisible()

    // The selection gone, a move with no button held is the program's again.
    const after = got().length
    await page.mouse.move(mid.x, mid.y, { steps: 4 })
    await expect.poll(() => got().slice(after)).toMatch(/\x1b\[<35;\d+;\d+M/)
  } finally {
    await api.stopSession(s.id)
  }
})

// Where a click's report does not clear the selection (xterm's first encoding sends it as bytes, not typing; a view link sends
// nothing), the terminal clears it itself: otherwise the moves kept from the program while it shows would stay kept.
test('a click clears the selection under the first encoding and on a view link', async ({ page, api, state, browser }) => {
  const { s, got } = await probe(page, api, state.root, 'mouse-any-plain', 'any plain')
  try {
    // A move with no button held is the program's: ESC [ M, then 32 + 3 (no button) + 32 (a move), and the cell.
    const mid = await cell(page, api, s.id, 40, 10)
    await page.mouse.move(mid.x, mid.y, { steps: 4 })
    await expect.poll(() => got()).toMatch(/\x1b\[MC[\s\S]{2}$/)
    await setClip(page, 'nothing-copied')
    const before = got().length
    await shiftDragWord(page, api, s.id, mid)
    await page.keyboard.press('Control+Shift+C')
    await expect.poll(() => clip(page)).toBe('select-me-42')
    await page.waitForTimeout(300)
    expect(got().slice(before)).toBe('')

    // A click is the program's (a press, 32, and a release, 35) and clears the selection; the moves after it are the program's again.
    const far = await cell(page, api, s.id, 20, 5)
    await page.mouse.click(far.x, far.y)
    await expect.poll(() => got().slice(before)).toMatch(/^\x1b\[M [\s\S]{2}\x1b\[M#[\s\S]{2}$/)
    await copyEnabled(page, far, false)
    const after = got().length
    await page.mouse.move(mid.x, mid.y, { steps: 4 })
    await expect.poll(() => got().slice(after)).toMatch(/\x1b\[MC[\s\S]{2}/)

    // A view link's terminal reports nothing: a Shift+drag selects there, and a click on a link opens it and clears the selection.
    const { token } = await api.ok<{ token: string }>('POST', `/api/sessions/${s.id}/links`, { role: 'view' })
    const guestCtx = await browser.newContext({ permissions: ['clipboard-read', 'clipboard-write'] })
    try {
      await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Viewer'))
      const guest = await guestCtx.newPage()
      await guest.goto(`/join/${token}`)
      await guest.getByRole('button', { name: 'Join session' }).click()
      await expect(guest.locator('.terminal-host .xterm-screen').first()).toBeVisible({ timeout: 30_000 })
      await expect(guest.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
      await reporting(guest, true)
      const gmid = await cell(guest, api, s.id, 40, 10)
      await setClip(guest, 'nothing-copied')
      await shiftDragWord(guest, api, s.id, gmid)
      await guest.keyboard.press('Control+Shift+C')
      await expect.poll(() => clip(guest)).toBe('select-me-42')
      const link = await cell(guest, api, s.id, 12, 2)
      await guest.mouse.move(link.x, link.y, { steps: 4 })
      await guest.mouse.click(link.x, link.y)
      await expect(linkToast(guest, 'https://example.test/after-select')).toBeVisible()
      await copyEnabled(guest, gmid, false)
    } finally {
      await guestCtx.close()
    }
  } finally {
    await api.stopSession(s.id)
  }
})
