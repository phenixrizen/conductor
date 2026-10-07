import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// Shift+Enter and Ctrl+Enter in the terminal are a newline in the agent's
// prompt, not a submit: the session gets ESC CR (what Claude Code's own
// /terminal-setup teaches VS Code and iTerm2 to send, read as a newline; Alt+Enter
// to Codex) instead of the plain CR xterm sends for Enter. The stub reads
// lines, so each chord ends one with the ESC still in it, and Enter the last.
test('Shift+Enter and Ctrl+Enter send ESC CR into the session instead of submitting', async ({ page, api, state }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'newline' })
  try {
    await page.goto(`/sessions/${s.id}`)
    const screen = page.locator('.xterm-screen').first()
    await expect(screen).toBeVisible({ timeout: 30_000 })
    await screen.click()
    await page.keyboard.type('first')
    await page.keyboard.press('Shift+Enter')
    await page.keyboard.type('second')
    await page.keyboard.press('Control+Enter')
    await page.keyboard.type('third')
    await page.keyboard.press('Enter')
    const file = join(state.home, '.stub-sessions', `${s.agentSession?.id ?? ''}.txt`)
    await expect.poll(() => (existsSync(file) ? readFileSync(file, 'utf8') : ''), { timeout: 15_000 }).toContain('third')
    const got = readFileSync(file, 'utf8')
    expect(got).toContain('first\u001b')
    expect(got).toContain('second\u001b')
  } finally {
    await api.stopSession(s.id)
  }
})
