import { existsSync, mkdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { expect, test, type Api, type Session } from './fixtures'

// Copy and paste as Windows Terminal has them (round 15): Ctrl+Shift+C
// copies the selection, Ctrl+C on a selection copies and clears it without
// the interrupt, Ctrl+Shift+V pastes; right-click copies a selection and
// otherwise pastes; Shift+right-click opens the terminal's menu, whose
// "Right-click pastes" turns that off; a view link copies and never pastes.
//
// Whatever renderer the browser has: with WebGL the screen is a canvas and
// holds no text, so the word to select is put on the screen's first row
// (after a clear) and found by the cell's geometry, and what the shell ran
// is read from the files it wrote.
test.use({ permissions: ['clipboard-read', 'clipboard-write'] })

const clip = (page: Page) => page.evaluate(() => navigator.clipboard.readText())
const setClip = (page: Page, text: string) => page.evaluate((t) => navigator.clipboard.writeText(t), text)

/** The middle of a cell of the page's terminal: the screen's box over the session's size. */
async function cell(page: Page, api: Api, id: string, col: number, row: number) {
  const { cols, rows } = (await api.session(id)) as Session & { cols: number; rows: number }
  const box = (await page.locator('.terminal-host .xterm-screen').first().boundingBox())!
  return { x: box.x + ((col + 0.5) * box.width) / cols, y: box.y + ((row + 0.5) * box.height) / rows }
}

/** A file the shell wrote, once it is there. */
const wrote = (dir: string, name: string) => () => (existsSync(join(dir, name)) ? readFileSync(join(dir, name), 'utf8') : '')

test('copy and paste in the terminal: the keys, right-click, its menu, and a view link that never pastes', async ({ page, api, browser, state }) => {
  const cwd = join(state.root, 'clipboard')
  mkdirSync(cwd, { recursive: true })
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'shell', name: 'clipboard', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    const screen = page.locator('.terminal-host .xterm-screen').first()
    await expect(screen).toBeVisible({ timeout: 30_000 })
    // Keys typed before the connection opens are dropped.
    await expect(page.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
    await screen.click()
    await page.keyboard.type('clear; echo copyme-123; echo shown > shown.txt\n')
    await expect.poll(wrote(cwd, 'shown.txt'), { timeout: 15_000 }).toBe('shown\n')
    const word = await cell(page, api, s.id, 3, 0)

    // Ctrl+Shift+C copies the selection (a double-click selects the word; tried again until the output has reached the screen).
    await expect
      .poll(async () => {
        await page.mouse.dblclick(word.x, word.y)
        await page.keyboard.press('Control+Shift+C')
        return clip(page)
      }, { timeout: 15_000 })
      .toBe('copyme-123')

    // Ctrl+C on a selection copies and clears it, and interrupts nothing: the command running finishes and writes its file.
    await setClip(page, 'before')
    await page.keyboard.type('sleep 2; echo slept > slept.txt\n')
    await page.mouse.dblclick(word.x, word.y)
    await page.keyboard.press('Control+C')
    await expect.poll(() => clip(page)).toBe('copyme-123')
    await expect.poll(wrote(cwd, 'slept.txt'), { timeout: 15_000 }).toBe('slept\n')

    // Ctrl+Shift+V pastes what the clipboard holds.
    await setClip(page, 'echo pasted-456 > pasted.txt')
    await screen.click()
    await page.keyboard.press('Control+Shift+V')
    await page.keyboard.press('Enter')
    await expect.poll(wrote(cwd, 'pasted.txt'), { timeout: 15_000 }).toBe('pasted-456\n')

    // Right-click with nothing selected pastes; on a selection it copies it.
    await setClip(page, 'echo right-789 > right.txt')
    await screen.click()
    await screen.click({ button: 'right' })
    await page.keyboard.press('Enter')
    await expect.poll(wrote(cwd, 'right.txt'), { timeout: 15_000 }).toBe('right-789\n')
    await setClip(page, 'before')
    await page.mouse.dblclick(word.x, word.y)
    await page.mouse.click(word.x, word.y, { button: 'right' })
    await expect.poll(() => clip(page)).toBe('copyme-123')

    // Shift+right-click opens the menu; "Right-click pastes" off, a plain right-click opens it too.
    await screen.click({ button: 'right', modifiers: ['Shift'] })
    await expect(page.getByRole('menuitem', { name: 'Copy' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Paste' })).toBeVisible()
    await expect(page.getByRole('menuitem', { name: 'Select all' })).toBeVisible()
    const toggle = page.getByRole('menuitemcheckbox', { name: 'Right-click pastes' })
    await expect(toggle).toHaveAttribute('aria-checked', 'true')
    await toggle.click()
    await expect(page.getByRole('menuitem', { name: 'Copy' })).toHaveCount(0)
    await screen.click({ button: 'right' })
    await expect(page.getByRole('menuitemcheckbox', { name: 'Right-click pastes' })).toHaveAttribute('aria-checked', 'false')
    await page.keyboard.press('Escape')

    // A view link copies, and pastes nothing: no key, no right-click, a disabled Paste.
    const { token } = await api.ok<{ token: string }>('POST', `/api/sessions/${s.id}/links`, { role: 'view' })
    const guestCtx = await browser.newContext({ permissions: ['clipboard-read', 'clipboard-write'] })
    try {
      await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Viewer'))
      const guest = await guestCtx.newPage()
      await guest.goto(`/join/${token}`)
      await guest.getByRole('button', { name: 'Join session' }).click()
      const gscreen = guest.locator('.terminal-host .xterm-screen').first()
      await expect(gscreen).toBeVisible({ timeout: 30_000 })
      await expect(guest.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
      const gword = await cell(guest, api, s.id, 3, 0)
      await expect
        .poll(async () => {
          await guest.mouse.dblclick(gword.x, gword.y)
          await guest.keyboard.press('Control+Shift+C')
          return clip(guest)
        }, { timeout: 15_000 })
        .toBe('copyme-123')
      await setClip(guest, 'echo nope-000 > nope.txt')
      await gscreen.click()
      await guest.keyboard.press('Control+Shift+V')
      await guest.keyboard.press('Enter')
      await gscreen.click({ button: 'right' })
      await expect(guest.getByRole('menuitem', { name: 'Paste' })).toBeDisabled()
      await guest.keyboard.press('Escape')
      // The owner's next line runs; the guest's never did.
      await screen.click()
      await page.keyboard.type('echo after > after.txt\n')
      await expect.poll(wrote(cwd, 'after.txt'), { timeout: 15_000 }).toBe('after\n')
      expect(existsSync(join(cwd, 'nope.txt'))).toBe(false)
    } finally {
      await guestCtx.close()
    }
  } finally {
    await api.stopSession(s.id)
  }
})
