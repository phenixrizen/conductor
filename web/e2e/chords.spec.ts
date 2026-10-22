import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

function transcript(home: string, s: Session): string {
  const file = join(home, '.stub-sessions', `${s.agentSession?.id ?? ''}.txt`)
  return existsSync(file) ? readFileSync(file, 'utf8') : ''
}

// The chords per system (#312), on the one system the suite runs on
// (Linux, Chromium): Alt+Q leaves a terminal's focus on the Yard's focus and
// the Roundhouse, Alt+Esc too where the system leaves it; the shortcuts
// modal shows Alt+Q for that row and says what this system takes; Ctrl+B
// collapses the sidebar outside a terminal and Alt+B inside one.
test('Alt+Q (and Alt+Esc here) leave the terminal on the Yard and the Roundhouse, and reaches the shell on a session page; the modal shows the system’s chords and notes', async ({ page, api, state }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e chords' })
  try {
    const inTerminal = () => page.evaluate(() => !!document.activeElement?.closest('.terminal-host'))
    await page.goto(`/yard?focus=${s.id}`)
    const terminal = page.locator('[data-yard-focus] .terminal-host .xterm-helper-textarea').first()
    await expect(terminal).toBeVisible({ timeout: 30_000 })
    await terminal.focus()
    expect(await inTerminal()).toBe(true)
    await page.keyboard.press('Alt+q')
    // The Yard's Alt+Q with nothing to fold leaves the focus and goes back to the grid.
    await expect(page).toHaveURL(/\/yard$/)
    expect(await inTerminal()).toBe(false)
    await page.goto('/roundhouse')
    const slide = page.locator('[aria-roledescription="slide"]').filter({ has: page.getByText('e2e chords', { exact: true }) })
    await expect(slide).toBeVisible({ timeout: 30_000 })
    await page.locator('[data-carousel-strip] [data-strip-chip]').filter({ has: page.getByText('e2e chords', { exact: true }) }).click()
    await page.keyboard.press('Enter')
    await expect.poll(inTerminal, { timeout: 15_000 }).toBe(true)
    await page.keyboard.press('Alt+q')
    await expect.poll(inTerminal).toBe(false)
    await page.keyboard.press('Enter')
    await expect.poll(inTerminal, { timeout: 15_000 }).toBe(true)
    await page.keyboard.press('Alt+Escape')
    await expect.poll(inTerminal).toBe(false)
    // The modal: the row shows Alt+Q, and the notes name this system.
    await page.keyboard.press('?')
    const modal = page.locator('[data-shortcuts]')
    await expect(modal).toBeVisible()
    await expect(modal.locator('[data-shortcut^="Leave the terminal"] [data-shortcut-terminal]')).toHaveText(/Alt.*Q/i)
    await expect(modal.locator('[data-shortcuts-notes]')).toHaveAttribute('data-shortcuts-system', 'linux')
    await expect(modal.locator('[data-shortcuts-notes] h3')).toContainText('On Linux, in a browser')
    await expect(modal.locator('[data-shortcut-note="Ctrl+B"]')).toContainText('Firefox')
    await expect(modal.locator('[data-shortcut-note="Alt+Esc"]')).toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(modal).toBeHidden()
    // The sidebar: Ctrl+B outside a terminal, Alt+B inside one.
    await page.goto(`/sessions/${s.id}`)
    const session = page.locator('.terminal-host .xterm-helper-textarea').first()
    await expect(session).toBeFocused({ timeout: 30_000 })
    const rail = page.locator('[data-rail-expand]')
    await page.keyboard.press('Alt+b')
    await expect(rail).toBeVisible()
    await page.keyboard.press('Alt+b')
    await expect(rail).toBeHidden()
    await page.locator('[data-session-list]').click({ position: { x: 5, y: 5 } })
    await page.keyboard.press('Control+b')
    await expect(rail).toBeVisible()
    await page.keyboard.press('Control+b')
    await expect(rail).toBeHidden()
    // On a session page, which takes no Alt+Q, the chord is the shell's: it lands in the line as Escape then q (Codex's round).
    await session.focus()
    await page.keyboard.type('a')
    await page.keyboard.press('Alt+q')
    await page.keyboard.type('b')
    await page.keyboard.press('Enter')
    await expect.poll(() => transcript(state.home, s), { timeout: 15_000, message: 'the line with the chord was typed' }).toContain('a\u001bqb\n')
  } finally {
    await api.stopSession(s.id)
  }
})
