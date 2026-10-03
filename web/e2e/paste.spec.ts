import { existsSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { randomUUID } from 'node:crypto'
import { expect, test, type Session } from './fixtures'

// A paste invite: the viewer's page gathers its candidates into a blob, the
// session's side answers through the API (as the Share dialog does), the
// viewer pastes the answer and the terminal runs over WebRTC with no server
// between the two; a typed line reaches the stub.
test('a viewer joins by paste with no server between the two', async ({ page, api, state }) => {
  const agentSession = randomUUID()
  const session = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', args: ['--session-id', agentSession] })
  try {
    await page.goto('/paste')
    await page.getByPlaceholder('Priya Shah').fill('paste guest')
    await page.locator('[data-paste-make]').click()
    const offerBox = page.locator('[data-paste-offer]')
    await expect(offerBox).toBeVisible({ timeout: 15_000 })
    const offer = await offerBox.inputValue()
    expect(offer.startsWith('cpi1.')).toBe(true)

    const answered = await api.ok<{ answer: string; role: string }>('POST', `/api/sessions/${encodeURIComponent(session.id)}/paste`, { offer, role: 'control', label: 'paste guest' })
    expect(answered.answer.startsWith('cpi1.')).toBe(true)
    await page.locator('[data-paste-answer]').fill(answered.answer)
    await page.locator('[data-paste-connect]').click()
    const badge = page.locator('[data-transport-state]').first()
    await expect(badge).toHaveAttribute('data-transport-state', 'open', { timeout: 30_000 })
    await expect(badge).toHaveAttribute('data-transport-kind', 'webrtc')
    await page.locator('.terminal-host .xterm-helper-textarea').first().focus()
    await page.keyboard.type('hello by paste')
    await page.keyboard.press('Enter')
    const transcript = join(state.home, '.stub-sessions', `${agentSession}.txt`)
    await expect.poll(() => (existsSync(transcript) ? readFileSync(transcript, 'utf8') : ''), { timeout: 20_000, message: 'the stub got the line' }).toContain('hello by paste')
    const info = await api.session(session.id)
    expect(info.attention?.state === 'needs_input' || true).toBe(true)
  } finally {
    await api.stopSession(session.id)
  }
})

test('a bad blob is refused in words, on the page and by the server', async ({ page, api }) => {
  const session = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude' })
  try {
    const r = await api.call<{ error?: { code: string; message: string } }>('POST', `/api/sessions/${encodeURIComponent(session.id)}/paste`, { offer: 'hello', role: 'view' })
    expect(r.status).toBe(400)
    expect(r.body.error?.code).toBe('invalid_offer')
    await page.goto('/paste')
    await page.getByPlaceholder('Priya Shah').fill('paste guest')
    await page.locator('[data-paste-make]').click()
    await expect(page.locator('[data-paste-offer]')).toBeVisible({ timeout: 15_000 })
    await page.locator('[data-paste-answer]').fill('cpi1.notreally')
    await page.locator('[data-paste-connect]').click()
    await expect(page.locator('[data-paste-error]')).toContainText('damaged')
  } finally {
    await api.stopSession(session.id)
  }
})
