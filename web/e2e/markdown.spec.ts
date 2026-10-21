import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The Markdown preview (#166): a `.md` file in the editor shown rendered by
// the header's Preview, a Mermaid block drawn, raw HTML shown as text, a
// relative link opening its file in a tab, the choice kept per tab, Alt+P
// bringing the source back. Mermaid draws SVG only in a browser (vitest's
// node environment has no DOM), so the drawing is checked here.
test('a Markdown file previews rendered with its diagram drawn; a link opens its file; Alt+P brings the source back', async ({ page, api, state }) => {
  const cwd = join(state.root, 'markdown-preview')
  mkdirSync(join(cwd, 'docs'), { recursive: true })
  writeFileSync(
    join(cwd, 'README.md'),
    [
      '# Signals',
      '',
      'The crew reads [the users guide](docs/users.md) first.',
      '',
      '<script>alert(1)</script>',
      '',
      '| Signal | Means |',
      '|---|---|',
      '| green | go |',
      '',
      '- [x] built',
      '- [ ] shipped',
      '',
      '```mermaid',
      'flowchart LR',
      '  A[Yard] --> B[Roundhouse]',
      '```',
      '',
      '```go',
      'package main',
      '```',
      '',
    ].join('\n'),
  )
  writeFileSync(join(cwd, 'docs', 'users.md'), '# Users\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'markdown', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    const box = pane.locator('[data-files-box] input, input[data-files-box]').first()
    await box.fill('README.md')
    await box.press('Enter')
    const area = page.locator('[data-editor-area]')
    await expect(area).toHaveAttribute('data-editor-area', 'open')
    await expect(area.locator('[data-editor-pos]')).toHaveText('Ln 1, Col 1', { timeout: 30_000 })
    // The source first; Preview renders it: the heading, the table, the task list, the script as text, the diagram drawn, the code highlighted.
    const toggle = area.locator('[data-editor-preview]')
    await expect(toggle).toHaveAttribute('data-editor-preview', 'off')
    await expect(toggle).toHaveText('Preview')
    await toggle.click()
    await expect(toggle).toHaveAttribute('data-editor-preview', 'on')
    const preview = area.locator('[data-md-preview]')
    await expect(preview.locator('h1')).toHaveText('Signals')
    await expect(preview.locator('td').first()).toHaveText('green')
    await expect(preview.locator('li.task input[type=checkbox]')).toHaveCount(2)
    await expect(preview.locator('li.task input:checked')).toHaveCount(1)
    await expect(preview).toContainText('<script>alert(1)</script>')
    await expect(preview.locator('script')).toHaveCount(0)
    await expect(preview.locator('[data-mermaid-rendered] svg')).toBeVisible({ timeout: 30_000 })
    await expect(preview.locator('[data-mermaid-rendered]')).toContainText('Roundhouse')
    await expect(preview.locator('pre.shiki')).toContainText('package main', { timeout: 30_000 })
    await expect(area.locator('[data-editor-pos]')).toHaveCount(0)
    // A relative link opens its file in a tab of its own; the README's tab keeps its preview.
    await preview.locator('a[data-md-link]').click()
    await expect(area.locator('[data-editor-tab]')).toHaveCount(2)
    await expect(area.locator('[data-editor-tab-active]')).toContainText('users.md')
    await expect(area.locator('[data-editor-preview]')).toHaveAttribute('data-editor-preview', 'off')
    await area.locator('[data-editor-tab]', { hasText: 'README.md' }).click()
    await expect(area.locator('[data-editor-preview]')).toHaveAttribute('data-editor-preview', 'on')
    await expect(preview.locator('h1')).toHaveText('Signals')
    // Alt+P in the preview brings the source back.
    await preview.click()
    await page.keyboard.press('Alt+KeyP')
    await expect(area.locator('[data-editor-preview]')).toHaveAttribute('data-editor-preview', 'off')
    await expect(area.locator('[data-md-preview]')).toHaveCount(0)
    await expect(area.locator('[data-code-editor]')).toBeVisible()
    await expect(area.locator('[data-editor-pos]')).toBeVisible()
  } finally {
    await api.stopSession(s.id)
  }
})
