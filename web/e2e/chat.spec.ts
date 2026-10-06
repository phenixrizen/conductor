import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import type { BrowserContext, Page } from '@playwright/test'
import { expect, test, type Session } from './fixtures'

// Chat beside the terminal (design 2a, 2b, 2c, 2d, 2e, 2f, 2h): the people
// on a session talk over the terminal's own connection; a controller can have
// a message typed into the agent; the tab counts what arrived while it was
// closed; a guest on a link gets the same chat on the bare join page; on a
// phone it is a sheet over the terminal; a crew run has one chat for everyone
// on it, beside its tiles; an ended session's chat is read-only.
test.describe.configure({ mode: 'serial' })

const sessions: string[] = []
let other: BrowserContext | null = null
let crewId = ''
let runId = ''

test.afterAll(async ({ api }) => {
  await other?.close()
  if (runId) await api.stopRun(runId)
  for (const id of sessions) await api.stopSession(id)
  if (crewId) await api.call('DELETE', `/api/crews/${encodeURIComponent(crewId)}`)
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

test('a view link can talk beside the terminal and cannot reach the agent; the chat ends with the session', async ({ page, api, browser }) => {
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
  // The bare page has the chat beside the terminal (design 2d): the view-only note, nothing that reaches the agent.
  const guestChat = guest.locator('[data-chat-aside] [data-chat]')
  await expect(guestChat).toBeVisible({ timeout: 15_000 })
  await expect(guestChat.locator('[data-chat-note]')).toHaveText('You are view only: what you write reaches the people here, not the agent.')
  await expect(guestChat.locator('[data-chat-to-agent]')).toHaveCount(0)
  await expect(guest.locator('[data-chat-button]')).toBeVisible()
  // The guest's join and words reach Nate's chat; her row carries the view tag.
  await expect(nateChat.locator('[data-chat-system]').last()).toContainText('Priya joined · view', { timeout: 15_000 })
  await nateChat.locator('[data-chat-input] textarea, textarea[data-chat-input]').first().fill('welcome, Priya')
  await page.keyboard.press('Enter')
  await expect(nateChat.locator('[data-chat-kind="message"]').first()).toContainText('welcome, Priya')
  const toPriya = guestChat.locator('[data-chat-kind="message"]').first()
  await expect(toPriya).toContainText('welcome, Priya', { timeout: 15_000 })
  await toPriya.hover()
  await expect(guestChat.locator('[data-chat-send-to-agent]')).toHaveCount(0)
  await guestChat.locator('[data-chat-input] textarea, textarea[data-chat-input]').first().fill('hi from the link')
  await guest.keyboard.press('Enter')
  const fromPriya = nateChat.locator('[data-chat-kind="message"]').nth(1)
  await expect(fromPriya).toContainText('hi from the link', { timeout: 15_000 })
  await expect(fromPriya.locator('[data-chat-view-tag]')).toBeVisible()
  // The panel folds away behind the header's button and comes back.
  await guest.locator('[data-chat-button]').click()
  await expect(guest.locator('[data-chat-aside]')).toHaveCount(0)
  await guest.locator('[data-chat-button]').click()
  await expect(guestChat).toBeVisible()

  // Stopped, the session's chat is read-only, for the guest too.
  await api.stopSession(s.id)
  await expect(nateChat.locator('[data-chat-ended]')).toBeVisible({ timeout: 15_000 })
  await expect(nateChat.locator('[data-chat-composer]')).toHaveCount(0)
  await expect(guestChat.locator('[data-chat-ended]')).toBeVisible({ timeout: 15_000 })
  await guestCtx.close()
})

test('on a phone the header button carries the count and the chat opens as a sheet over the live terminal', async ({ page, api, state, browser }) => {
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'chat phone' })
  sessions.push(s.id)
  await page.addInitScript(() => localStorage.setItem('conductor.displayName', 'Nate'))
  await page.goto(`/sessions/${s.id}`)
  await page.locator('[data-inspector] [data-chat-tab]').click()
  const nateChat = page.locator('[data-chat]')
  const nateInput = nateChat.locator('[data-chat-input] textarea, textarea[data-chat-input]').first()
  // Priya, on her phone (design 2c): no inspector, the Chat button in the header.
  const phoneCtx = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true })
  const priya = await personPage(phoneCtx, state.token, 'Priya')
  await priya.goto(`/sessions/${s.id}`)
  const button = priya.locator('[data-chat-button]')
  await expect(button).toBeVisible({ timeout: 15_000 })
  await expect(priya.locator('[data-inspector]')).toBeHidden()
  await expect(priya.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })

  // A message while the sheet is closed raises the count, never a toast over the terminal.
  await nateInput.fill('ping from the desk')
  await page.keyboard.press('Enter')
  await expect(button.locator('[data-chat-unread="1"]')).toBeVisible({ timeout: 15_000 })
  await expect(priya.locator('[data-slot="viewport"] [data-slot="root"]')).toHaveCount(0)

  // Tapping it opens the sheet with the thread and clears the count; Return sends; the terminal stays live behind it.
  await button.tap()
  const sheet = priya.locator('[data-chat-sheet]')
  await expect(sheet).toBeVisible()
  await expect(sheet.locator('[data-chat-kind="message"]').first()).toContainText('ping from the desk')
  await expect(button.locator('[data-chat-unread]')).toHaveCount(0)
  await expect(sheet.locator('[data-chat-composer]')).not.toContainText('Shift+Enter')
  const sheetInput = sheet.locator('[data-chat-input] textarea, textarea[data-chat-input]').first()
  await sheetInput.fill('pong from the phone')
  await priya.keyboard.press('Enter')
  await expect(nateChat.locator('[data-chat-kind="message"]').nth(1)).toContainText('pong from the phone', { timeout: 15_000 })
  await expect(priya.locator('[data-transport-state]').first()).toHaveAttribute('data-transport-state', 'open')
  await expect(priya.locator('.xterm-screen').first()).toBeVisible()
  // × closes it; what arrives then counts again.
  await priya.locator('[data-chat-close]').tap()
  await expect(sheet).toBeHidden()
  await nateInput.fill('and again')
  await page.keyboard.press('Enter')
  await expect(button.locator('[data-chat-unread="1"]')).toBeVisible({ timeout: 15_000 })

  // A guest on a phone gets the same button and sheet on the bare page, with the view-only note.
  const { token } = await api.ok<{ token: string }>('POST', `/api/sessions/${s.id}/links`, { role: 'view' })
  const guestCtx = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true })
  await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Jane'))
  const jane = await guestCtx.newPage()
  await jane.goto(`/join/${token}`)
  await expect(jane.locator('[data-join-frame]')).toHaveAttribute('data-join-frame', 'bare')
  await jane.getByRole('button', { name: 'Join session' }).tap()
  const janeButton = jane.locator('[data-chat-button]')
  await expect(janeButton).toBeVisible({ timeout: 30_000 })
  await expect(jane.locator('[data-chat-aside]')).toBeHidden()
  await janeButton.tap()
  const janeSheet = jane.locator('[data-chat-sheet]')
  await expect(janeSheet).toBeVisible()
  await expect(janeSheet.locator('[data-chat-note]')).toContainText('You are view only')
  await expect(janeSheet.locator('[data-chat-to-agent]')).toHaveCount(0)
  await expect(janeSheet.locator('[data-chat-kind="message"]')).toHaveCount(3)
  await guestCtx.close()
  await phoneCtx.close()
})

test('a run has one chat for everyone on it: beside the tiles, typed into a member on request, open to a guest on the run link, kept when it ends', async ({ page, api, state, browser }) => {
  // A crew of two: lead runs, review holds the stub's trust question.
  crewId = (
    await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
      name: 'e2e chat crew',
      goal: 'talk',
      cwd: '',
      where: 'server',
      isolation: 'none',
      openAfterLaunch: false,
      members: [
        { name: 'lead', agentId: 'claude', prompt: 'say hello', start: { when: 'immediately' } },
        { name: 'review', agentId: 'codex-untrusted', prompt: 'review it', start: { when: 'immediately' } },
      ],
    })
  ).crew.id
  runId = (await api.launchCrew(crewId)).id
  let leadId = ''
  await expect
    .poll(
      async () => {
        const run = await api.run(runId)
        leadId = run.members.find((m) => m.name === 'lead')?.sessionId ?? ''
        const reviewId = run.members.find((m) => m.name === 'review')?.sessionId ?? ''
        if (!leadId || !reviewId) return ''
        return `${run.members.find((m) => m.name === 'lead')?.status}/${(await api.session(reviewId)).attention?.state ?? ''}`
      },
      { timeout: 30_000 },
    )
    .toBe('running/needs_input')

  // Nate opens the run chat from the run page; Priya, on the same page, sees the count, then the message.
  await page.addInitScript(() => localStorage.setItem('conductor.displayName', 'Nate'))
  await page.goto(`/runs/${runId}`)
  const nateButton = page.locator('[data-run-chat-button]')
  await expect(nateButton).toBeVisible({ timeout: 15_000 })
  other = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const priya = await personPage(other, state.token, 'Priya')
  await priya.goto(`/runs/${runId}`)
  // Her run chat connection is up before Nate writes: a message that arrives as history is read, not counted.
  await expect(priya.locator('[data-run-chat-button]:not([data-run-chat-offline])')).toBeVisible({ timeout: 15_000 })
  await nateButton.click()
  const nateChat = page.locator('[data-run-chat]')
  await expect(nateChat).toBeVisible()
  await expect(nateChat.locator('[data-chat-empty]')).toContainText("Everyone on this run's link sees this chat", { timeout: 15_000 })
  const nateInput = nateChat.locator('[data-chat-input] textarea, textarea[data-chat-input]').first()
  await nateInput.fill('hello run')
  await page.keyboard.press('Enter')
  await expect(nateChat.locator('[data-chat-kind="message"]').first()).toContainText('hello run', { timeout: 15_000 })
  await expect(priya.locator('[data-run-chat-button] [data-chat-unread="1"]')).toBeVisible({ timeout: 15_000 })
  await priya.locator('[data-run-chat-button]').click()
  const priyaChat = priya.locator('[data-run-chat]')
  await expect(priyaChat.locator('[data-chat-kind="message"]').first()).toContainText('hello run')
  await expect(priya.locator('[data-run-chat-button] [data-chat-unread]')).toHaveCount(0)
  await priyaChat.locator('[data-chat-input] textarea, textarea[data-chat-input]').first().fill('hi back')
  await priya.keyboard.press('Enter')
  await expect(nateChat.locator('[data-chat-kind="message"]').nth(1)).toContainText('hi back', { timeout: 15_000 })

  // The composer's menu: review waits on a prompt and is skipped; lead takes the line, marked for everyone.
  await nateChat.locator('[data-chat-scope-menu]').click()
  const skipped = page.getByRole('menuitem', { name: /Also send to review/ })
  await expect(skipped).toContainText('waiting on a prompt: skipped')
  await expect(skipped).toHaveAttribute('data-disabled', '')
  await page.getByRole('menuitem', { name: /Also send to lead/ }).click()
  await expect(nateChat.locator('[data-chat-scope-menu]')).toContainText('Also send to lead')
  await nateInput.fill('echo run-chat-to-lead')
  await page.keyboard.press('Enter')
  await expect(nateChat.locator('[data-chat-marker]').first()).toContainText('Sent to lead by Nate', { timeout: 15_000 })
  await expect(priyaChat.locator('[data-chat-marker]').first()).toContainText('Sent to lead by Nate', { timeout: 15_000 })
  const lead = await api.session(leadId)
  await expect.poll(() => transcript(state.home, lead), { timeout: 20_000, message: 'lead got the line' }).toContain('echo run-chat-to-lead')

  // A guest on the run link, in a browser with no workbench token: the tiles, the same chat, view only.
  const { token } = await api.ok<{ token: string }>('POST', `/api/runs/${runId}/links`, { role: 'view', ttlSeconds: 3600 })
  const guestCtx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  await guestCtx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Jane'))
  const jane = await guestCtx.newPage()
  await jane.goto(`/join/${token}`)
  await expect(jane.locator('[data-join-frame]')).toHaveAttribute('data-join-frame', 'bare')
  await jane.getByRole('button', { name: 'Join crew' }).click()
  await expect(jane.locator('[data-join-tiles] [data-member]')).toHaveCount(2, { timeout: 30_000 })
  await jane.locator('[data-run-chat-button]').click()
  const janeChat = jane.locator('[data-run-chat]')
  await expect(janeChat.locator('[data-chat-kind="message"]').first()).toContainText('hello run', { timeout: 15_000 })
  await expect(janeChat.locator('[data-chat-note]')).toHaveText('You are view only: you can talk here; nothing you write reaches a member.')
  await expect(janeChat.locator('[data-chat-scope-menu]')).toHaveCount(0)
  await janeChat.locator('[data-chat-input] textarea, textarea[data-chat-input]').first().fill('watching from the link')
  await jane.keyboard.press('Enter')
  // The fourth message: hello run, hi back, the line sent to lead, then Jane's.
  const fromJane = nateChat.locator('[data-chat-kind="message"]').nth(3)
  await expect(fromJane).toContainText('watching from the link', { timeout: 15_000 })
  await expect(fromJane.locator('[data-chat-view-tag]')).toBeVisible()
  await expect(nateChat.locator('[data-chat-system]').last()).toContainText('Jane joined · view')

  // Stopped, the run's chat is read-only and its record keeps it.
  await api.stopRun(runId)
  await expect(nateChat.locator('[data-chat-ended]')).toContainText('This run ended', { timeout: 20_000 })
  await expect.poll(async () => ((await api.run(runId)).chat ?? []).filter((m) => m.kind === 'message').length, { timeout: 15_000 }).toBe(4)
  await guestCtx.close()
})
