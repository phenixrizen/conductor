import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// Comments on lines (design round 12, F7): select lines in the editor, the
// bar offers Comment, Ask the agent and Copy; the composer carries the quote
// and the words to the session's chat, a quote card there with the lines;
// Ask the agent (Ctrl+Shift+A) also types the location, the lines and the
// words into the agent; when the lines move on disk the card says where
// they are now, and Open goes there.
test('a comment on lines lands in the chat as a quote card; asked of the agent it is typed with its location; moved lines are found', async ({ page, api, state }) => {
  const cwd = join(state.root, 'files-comments')
  mkdirSync(cwd, { recursive: true })
  const file = join(cwd, 'users.go')
  const body = ['package api', '', 'func A() {}', 'func B() {}', 'func C() {}', 'func D() {}', ''].join('\n')
  writeFileSync(file, body)
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'comments', cwd })
  const area = page.locator('[data-editor-area]')
  const editorLine = (text: string) => area.locator('.monaco-editor .view-line', { hasText: text })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    await page.locator(`[data-files-mode] [data-file-node="${file}"]`).click()
    await expect(editorLine('func A()')).toBeVisible({ timeout: 30_000 })
    // Select lines 3 and 4: the bar shows over them.
    await editorLine('func A()').click()
    await page.keyboard.press('Home')
    await page.keyboard.press('Shift+ArrowDown')
    await page.keyboard.press('Shift+End')
    const bar = area.locator('[data-editor-selection-bar]')
    await expect(bar).toBeVisible()
    await expect(bar.locator('[data-editor-ask]')).toBeVisible()
    // Comment: the composer names the lines, the words go with the quote to the chat.
    await bar.locator('[data-editor-comment]').click()
    const composer = area.locator('[data-editor-composer]')
    await expect(composer).toHaveAttribute('data-editor-composer-mode', 'comment')
    await expect(composer).toContainText('Comment on lines 3–4')
    await expect(composer.locator('[data-editor-composer-quote]')).toContainText('func B() {}')
    await page.keyboard.type('why are these two separate?')
    await page.keyboard.press('Enter')
    await expect(composer).toHaveCount(0)
    const card = page.locator('[data-chat-thread] [data-chat-quote="users.go:3–4"]').first()
    await expect(card).toBeVisible({ timeout: 15_000 })
    await expect(card.locator('[data-chat-quote-lines]')).toContainText('func A() {}')
    await expect(card.locator('[data-chat-quote-lines]')).toContainText('func B() {}')
    await expect(page.locator('[data-chat-thread] [data-chat-kind="message"]', { hasText: 'why are these two separate?' })).toBeVisible()
    await expect(card.locator('[data-chat-quote-moved]')).toHaveCount(0)
    // Ask the agent with its keys: the message, the marker, and the location and lines typed into the agent.
    await editorLine('func D()').click()
    await page.keyboard.press('Home')
    await page.keyboard.press('Shift+End')
    await page.keyboard.press('Control+Shift+A')
    await expect(composer).toHaveAttribute('data-editor-composer-mode', 'ask')
    await expect(composer).toContainText('Ask the agent about line 6')
    await page.keyboard.type('explain D')
    await page.keyboard.press('Enter')
    await expect(page.locator('[data-chat-thread] [data-chat-quote="users.go:6"]')).toBeVisible({ timeout: 15_000 })
    await expect(page.locator('[data-chat-thread] [data-chat-marker]').first()).toBeVisible({ timeout: 15_000 })
    // The stub agent's transcript holds what was typed into it: the location, the quoted line, the words.
    const info = await api.session(s.id)
    const transcript = () => {
      const f = join(state.home, '.stub-sessions', `${info.agentSession?.id ?? ''}.txt`)
      return existsSync(f) ? readFileSync(f, 'utf8') : ''
    }
    await expect.poll(transcript, { timeout: 20_000, message: 'the agent got the quote' }).toContain('users.go:6')
    expect(transcript()).toContain('> func D() {}')
    expect(transcript()).toContain('explain D')
    // The agent adds two lines above: the first card's lines moved, and Open goes to where they are now.
    writeFileSync(file, ['package api', '', '// Users.', '// The handlers.', 'func A() {}', 'func B() {}', 'func C() {}', 'func D() {}', ''].join('\n'))
    await page.reload()
    await page.locator('[data-inspector] button', { hasText: 'Chat' }).click()
    const moved = page.locator('[data-chat-thread] [data-chat-quote="users.go:3–4"]').first()
    await expect(moved.locator('[data-chat-quote-moved]')).toHaveText('Lines moved since · now 5–6', { timeout: 15_000 })
    await moved.locator('[data-chat-quote-open]').click()
    await expect(area.locator('[data-editor-pos]')).toHaveText(/^Ln 5, Col 1$/, { timeout: 30_000 })
    await expect(area.locator('.conductor-line-target').first()).toBeVisible()
  } finally {
    await api.stopSession(s.id)
  }
})
