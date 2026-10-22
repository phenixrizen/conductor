import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, showView, test, type Session } from './fixtures'

// A tab's menu (#330), as VS Code's: Close, Close others, Close to the
// right, Close to the left and Close all, each through the close that asks
// about unsaved changes.
test('the tab menu closes to the right, others and all, asking about an unsaved file', async ({ page, api, state }) => {
  const cwd = join(state.root, 'tabs')
  mkdirSync(cwd, { recursive: true })
  for (const n of ['a', 'b', 'c']) writeFileSync(join(cwd, `${n}.txt`), `${n} one\n${n} two\n`)
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e tabs', cwd })
  const area = page.locator('[data-editor-area]')
  const tab = (n: string) => area.locator(`[data-editor-tab="file:${join(cwd, `${n}.txt`)}"]`)
  const openAll = async () => {
    await showView(page, 'explorer')
    for (const n of ['a', 'b', 'c']) {
      await page.locator(`[data-files-mode] [data-file-node="${join(cwd, `${n}.txt`)}"]`).click()
      await expect(tab(n)).toBeVisible({ timeout: 30_000 })
    }
    await expect(area.locator('[data-editor-tab]')).toHaveCount(3)
    await expect(tab('c')).toHaveAttribute('data-editor-tab-active', '')
  }
  const choose = async (n: string, item: string) => {
    await tab(n).click({ button: 'right' })
    await page.getByRole('menuitem', { name: item }).click()
  }
  try {
    await page.goto(`/sessions/${s.id}`)
    await expect(page.locator('.terminal-host .xterm-helper-textarea').first()).toBeFocused({ timeout: 30_000 })
    await openAll()
    // Close to the right from the middle: c goes, b (the nearest kept) takes the front.
    await choose('b', 'Close to the right')
    await expect(area.locator('[data-editor-tab]')).toHaveCount(2)
    await expect(tab('c')).toHaveCount(0)
    await expect(tab('b')).toHaveAttribute('data-editor-tab-active', '')
    // Close others from a: only a stays, in front.
    await choose('a', 'Close others')
    await expect(area.locator('[data-editor-tab]')).toHaveCount(1)
    await expect(tab('a')).toHaveAttribute('data-editor-tab-active', '')
    // Three again; an unsaved edit in b; Close all asks, Don't save closes them all and the editor goes.
    await showView(page, 'explorer')
    for (const n of ['b', 'c']) {
      await page.locator(`[data-files-mode] [data-file-node="${join(cwd, `${n}.txt`)}"]`).click()
      await expect(tab(n)).toBeVisible({ timeout: 30_000 })
    }
    await tab('b').locator('button').first().click()
    await expect(tab('b')).toHaveAttribute('data-editor-tab-active', '')
    const lines = area.locator('.monaco-editor .view-lines')
    await expect(lines).toContainText('b one', { timeout: 30_000 })
    await lines.click()
    await page.keyboard.press('Control+End')
    await page.keyboard.type('b three\n')
    await expect(tab('b').locator('[data-editor-dirty]')).toHaveCount(1)
    await choose('c', 'Close all')
    const prompt = page.locator('[role="dialog"]', { hasText: 'Save b.txt?' })
    await expect(prompt).toBeVisible()
    await prompt.locator('[data-editor-discard]').click()
    await expect(area).toHaveCount(0)
    // Close to the left: nothing to the left of the first does nothing; a Shift+F10 on the focused tab opens the same menu.
    await openAll()
    await tab('a').locator('button').first().focus()
    await page.keyboard.press('Shift+F10')
    await page.getByRole('menuitem', { name: 'Close to the left' }).click()
    await expect(area.locator('[data-editor-tab]')).toHaveCount(3)
    await choose('c', 'Close to the left')
    await expect(area.locator('[data-editor-tab]')).toHaveCount(1)
    await expect(tab('c')).toHaveAttribute('data-editor-tab-active', '')
  } finally {
    await api.stopSession(s.id)
  }
})
