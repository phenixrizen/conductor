import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import type { BrowserContext, Page } from '@playwright/test'
import { expect, test, type Session } from './fixtures'

// Chat beside the terminal (design 2a, 2b, 2h): the people on a session talk
// over the terminal's own connection; a controller can have a message typed
// into the agent; the tab counts what arrived while it was closed; an ended
// session's chat is read-only.
test.describe.configure({ mode: 'serial' })

const sessions: string[] = []
let other: BrowserContext | null = null

test.afterAll(async ({ api }) => {
  await other?.close()
  for (const id of sessions) await api.stopSession(id)
})

function transcript(home: string, s: Session): string {
  const file = join(home, '.stub-sessions', `${s.agentSession?.id ?? ''}.txt`)
  return existsSync(file) ? readFileSync(file, 'utf8') : ''
}

/** A second person's workbench: the same server, their own name. */
async function personPage(context: BrowserContext, token: string, name: string): Promise<Page> {
  await context.addInitScript(
    ({ token, name }) => {
      localStorage.setItem('conductor.workbenchToken', token)
      localStorage.setItem('conductor.displayName', name)
    },
    { token, name },
  )
  return context.newPage()
}

test('two people on one session see each other, the closed tab counts, and a line can go to the agent', async ({ page, api, state, browser }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'chat room' })
  sessions.push(s.id)
  // Nate, in this browser; Priya, in another.
  await page.addInitScript(() => localStorage.setItem('conductor.displayName', 'Nate'))
  other = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const priya = await personPage(other, state.token, 'Priya')
  await page.goto(`/sessions/${s.id}`)
  await priya.goto(`/sessions/${s.id}`)
  const nateTab = page.locator('[data-inspector] [data-chat-tab]')
  const priyaTab = priya.locator('[data-inspector] [data-chat-tab]')
  await expect(nateTab).toBeVisible({ timeout: 15_000 })
  await expect(priyaTab).toBeVisible({ timeout: 15_000 })

  // Nate opens the chat and writes; Priya, on People, sees the count on the tab.
  await nateTab.click()
  const nateChat = page.locator('[data-chat]')
  await expect(nateChat.locator('[data-chat-empty]')).toBeVisible()
  await nateChat.locator('[data-chat-input] textarea, textarea[data-chat-input]').first().fill('hello from Nate')
  await page.keyboard.press('Enter')
  const nateMsg = nateChat.locator('[data-chat-kind="message"]').first()
  await expect(nateMsg).toContainText('hello from Nate')
  await expect(nateMsg).toContainText('Nate')
  await expect(priyaTab.locator('[data-chat-unread="1"]')).toBeVisible({ timeout: 15_000 })

  // Priya opens it: the count goes, the message is there, and her reply reaches Nate with the view of who she is.
  await priyaTab.click()
  await expect(priyaTab.locator('[data-chat-unread]')).toHaveCount(0)
  const priyaChat = priya.locator('[data-chat]')
  await expect(priyaChat.locator('[data-chat-kind="message"]').first()).toContainText('hello from Nate')
  const priyaInput = priyaChat.locator('[data-chat-input] textarea, textarea[data-chat-input]').first()
  await priyaInput.fill('line one')
  await priya.keyboard.press('Shift+Enter')
  await priya.keyboard.type('line two')
  await priya.keyboard.press('Enter')
  const reply = nateChat.locator('[data-chat-kind="message"]').nth(1)
  await expect(reply).toContainText('Priya')
  await expect(reply).toHaveText(/line one\s+line two/)
  // Nate's own thread never counted his own message, nor Priya's while it was open.
  await expect(nateTab.locator('[data-chat-unread]')).toHaveCount(0)

  // The counter past 1.5 KiB, and the bound.
  const nateInput = nateChat.locator('[data-chat-input] textarea, textarea[data-chat-input]').first()
  await nateInput.fill('x'.repeat(1600))
  await expect(nateChat.locator('[data-chat-counter]')).toHaveText('1.6 / 2 KiB')
  await nateInput.fill('x'.repeat(2100))
  await expect(nateChat.locator('[data-chat-send]')).toBeDisabled()
  await nateInput.fill('')

  // To agent: typed into the session, marked in the chat for everyone.
  await nateInput.fill('echo chat-to-agent')
  await nateChat.locator('[data-chat-to-agent]').click()
  await expect(nateChat.locator('[data-chat-marker]').first()).toContainText('Sent to agent by Nate', { timeout: 15_000 })
  await expect(priyaChat.locator('[data-chat-marker]').first()).toContainText('Sent to agent by Nate', { timeout: 15_000 })
  const info = await api.session(s.id)
  await expect.poll(() => transcript(state.home, info), { timeout: 20_000, message: 'the agent got the line' }).toContain('echo chat-to-agent')
  // A kept message can be sent later from its hover action; Priya, view only here? No: both are owners. Nate sends Priya's.
  await reply.hover()
  await reply.locator('[data-chat-send-to-agent]').click()
  await expect(nateChat.locator('[data-chat-marker]')).toHaveCount(2, { timeout: 15_000 })
  await expect.poll(() => transcript(state.home, info), { timeout: 20_000 }).toContain('line one line two')
})

test('a view link can talk and cannot reach the agent; the chat ends with the session', async ({ page, api, browser }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'chat ends' })
  sessions.push(s.id)
  await page.addInitScript(() => localStorage.setItem('conductor.displayName', 'Nate'))
  await page.goto(`/sessions/${s.id}`)
  await page.locator('[data-inspector] [data-chat-tab]').click()
  const nateChat = page.locator('[data-chat]')
  // A guest with a view link, in a browser with no workbench token.
  const { token } = await api.ok<{ token: string }>('POST', `/api/sessions/${s.id}/links`, { role: 'view' })
  const guestCtx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Priya'))
  const guest = await guestCtx.newPage()
  await guest.goto(`/join/${token}`)
  await expect(guest.locator('[data-join-frame]')).toHaveAttribute('data-join-frame', 'bare')
  await guest.getByRole('button', { name: 'Join session' }).click()
  await expect(guest.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
  // The guest's join and words reach Nate's chat; her row carries the view tag.
  await expect(nateChat.locator('[data-chat-system]').last()).toContainText('Priya joined · view', { timeout: 15_000 })
  await nateChat.locator('[data-chat-input] textarea, textarea[data-chat-input]').first().fill('welcome, Priya')
  await page.keyboard.press('Enter')
  await expect(nateChat.locator('[data-chat-kind="message"]').first()).toContainText('welcome, Priya')

  // Stopped, the session's chat is read-only.
  await api.stopSession(s.id)
  await expect(nateChat.locator('[data-chat-ended]')).toBeVisible({ timeout: 15_000 })
  await expect(nateChat.locator('[data-chat-composer]')).toHaveCount(0)
  await guestCtx.close()
})
