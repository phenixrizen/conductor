import type { Page } from '@playwright/test'
import { expect, showView, test, type Session } from './fixtures'

// A question answered by keys typed in the terminal leaves the chat saying
// so (#358): its card offers its choices no longer, and a line says
// "Answered in the terminal", as "Answered by …" does for a reply from the
// chat. The dialog is the Claude Code stub's plan approval, read off the
// screen, so that Conductor sees the digit answer it.

const sessions: string[] = []

test.afterAll(async ({ api }) => {
  for (const id of sessions) await api.stopSession(id)
})

async function typeLine(page: Page, sessionId: string, line: string) {
  await page.goto(`/sessions/${encodeURIComponent(sessionId)}`)
  const screen = page.locator('.terminal-host .xterm-screen').first()
  await expect(screen).toBeVisible()
  await screen.click()
  await page.keyboard.type(line)
  await page.keyboard.press('Enter')
}

test("a digit typed in the terminal takes the card's choices away and says so", async ({ page, api }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e chat terminal answer' })
  sessions.push(s.id)
  await typeLine(page, s.id, 'stub plan')
  await expect.poll(async () => (await api.session(s.id)).attention?.source, { timeout: 30_000, message: 'the dialog is asked' }).toBe('question')
  const att = (await api.session(s.id)).attention!
  const first = att.options?.[0]
  expect(first, 'the dialog offers choices').toBeTruthy()

  await showView(page, 'chat')
  const card = page.locator('[data-chat-question]').last()
  await expect(card).toBeVisible({ timeout: 30_000 })
  await expect(card.getByRole('button', { name: first!.label })).toBeVisible()

  // The answer goes to the program directly: its digit, typed in the terminal.
  await page.locator('.terminal-host .xterm-screen').first().click()
  await page.keyboard.press(first!.input)
  await expect.poll(async () => (await api.session(s.id)).attention?.source, { timeout: 30_000 }).not.toBe('question')

  await expect(card.getByRole('button', { name: first!.label })).toHaveCount(0, { timeout: 30_000 })
  await expect(page.locator('[data-chat-system]', { hasText: 'Answered in the terminal' }), `the system lines: ${JSON.stringify(await page.locator('[data-chat-system]').allTextContents())}`).toBeVisible({ timeout: 30_000 })
  // Nothing was answered through Conductor: no "Answered by".
  await expect(page.locator('[data-chat-system]', { hasText: /^Answered by/ })).toHaveCount(0)
})
