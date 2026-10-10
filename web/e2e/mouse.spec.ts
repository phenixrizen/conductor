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
// byte they read to a file; "q" ends them.
test.use({ permissions: ['clipboard-read', 'clipboard-write'] })

const APP = `import os, select, sys, termios, time, tty
# With "letgo", the program lets go of the mouse and ends at the first right-button press it reads.
letgo = sys.argv[1:] == ["letgo"]
fd = sys.stdin.fileno()
old = termios.tcgetattr(fd)
tty.setraw(fd)
sys.stdout.write("\\x1b[2J\\x1b[Hselect-me-42\\r\\n\\x1b[?1000h\\x1b[?1002h\\x1b[?1006h")
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
sys.stdout.write("\\x1b[?1006l\\x1b[?1002l\\x1b[?1000l")
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
