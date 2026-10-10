import { expect, test, type Session } from './fixtures'
import type { Page } from '@playwright/test'

// Copy and paste as Windows Terminal has them (round 15): Ctrl+Shift+C
// copies the selection, Ctrl+C on a selection copies and clears it without
// the interrupt, Ctrl+Shift+V pastes; right-click copies a selection and
// otherwise pastes; Shift+right-click opens the terminal's menu, whose
// "Right-click pastes" turns that off; a view link copies and never pastes.
test.use({ permissions: ['clipboard-read', 'clipboard-write'] })

const clip = (page: Page) => page.evaluate(() => navigator.clipboard.readText())
const setClip = (page: Page, text: string) => page.evaluate((t) => navigator.clipboard.writeText(t), text)
/** The screen's rows as text, top to bottom. */
const rows = (page: Page) => page.locator('.terminal-host .xterm-rows').first().evaluate((el) => Array.from(el.children).map((r) => (r.textContent ?? '').replace(/ /g, ' ').trimEnd()))
/** Double-clicks the first word of the last row that is exactly text: xterm selects the word. */
async function selectRow(page: Page, text: string) {
  const box = await page.locator('.terminal-host .xterm-rows').first().evaluate((el, want) => {
    const all = Array.from(el.children)
    for (let i = all.length - 1; i >= 0; i--) {
      if ((all[i]!.textContent ?? '').replace(/ /g, ' ').trim() === want) {
        const r = all[i]!.getBoundingClientRect()
        return { x: r.left + 6, y: r.top + r.height / 2 }
      }
    }
    return null
  }, text)
  if (!box) throw new Error(`no row ${text}`)
  await page.mouse.dblclick(box.x, box.y)
}

test('copy and paste in the terminal: the keys, right-click, its menu, and a view link that never pastes', async ({ page, api, browser }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'shell', name: 'clipboard' })
  try {
    await page.goto(`/sessions/${s.id}`)
    const screen = page.locator('.terminal-host .xterm-screen').first()
    await expect(screen).toBeVisible({ timeout: 30_000 })
    await screen.click()
    await page.keyboard.type('echo copyme-123\n')
    await expect.poll(async () => (await rows(page)).includes('copyme-123'), { timeout: 15_000 }).toBe(true)

    // Ctrl+Shift+C copies the selection.
    await selectRow(page, 'copyme-123')
    await page.keyboard.press('Control+Shift+C')
    await expect.poll(() => clip(page)).toBe('copyme-123')

    // Ctrl+C on a selection copies and clears it, and interrupts nothing.
    await setClip(page, 'before')
    await selectRow(page, 'copyme-123')
    await page.keyboard.press('Control+C')
    await expect.poll(() => clip(page)).toBe('copyme-123')
    await page.waitForTimeout(300)
    expect((await rows(page)).some((r) => r.includes('^C'))).toBe(false)

    // Ctrl+Shift+V pastes what the clipboard holds.
    await setClip(page, 'echo pasted-456')
    await screen.click()
    await page.keyboard.press('Control+Shift+V')
    await page.keyboard.press('Enter')
    await expect.poll(async () => (await rows(page)).includes('pasted-456'), { timeout: 15_000 }).toBe(true)

    // Right-click with nothing selected pastes; on a selection it copies it.
    await setClip(page, 'echo right-789')
    await screen.click()
    await screen.click({ button: 'right' })
    await page.keyboard.press('Enter')
    await expect.poll(async () => (await rows(page)).includes('right-789'), { timeout: 15_000 }).toBe(true)
    await setClip(page, 'before')
    await selectRow(page, 'right-789')
    const sel = await page.locator('.terminal-host .xterm-rows').first().boundingBox()
    await page.mouse.click(sel!.x + 6, (await page.locator('.terminal-host .xterm-rows > div', { hasText: /^right-789/ }).last().boundingBox())!.y + 6, { button: 'right' })
    await expect.poll(() => clip(page)).toBe('right-789')

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
      await expect.poll(async () => (await rows(guest)).includes('right-789'), { timeout: 15_000 }).toBe(true)
      await selectRow(guest, 'right-789')
      await guest.keyboard.press('Control+Shift+C')
      await expect.poll(() => clip(guest)).toBe('right-789')
      await setClip(guest, 'echo nope-000')
      await gscreen.click()
      await guest.keyboard.press('Control+Shift+V')
      await gscreen.click({ button: 'right' })
      await expect(guest.getByRole('menuitem', { name: 'Paste' })).toBeDisabled()
      await guest.keyboard.press('Escape')
      await page.waitForTimeout(800)
      expect((await rows(page)).some((r) => r.includes('nope-000'))).toBe(false)
    } finally {
      await guestCtx.close()
    }
  } finally {
    await api.stopSession(s.id)
  }
})
