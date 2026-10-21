import { execFileSync } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The Markdown preview (#166): a `.md` file in the editor shown rendered by
// the header's Preview, a Mermaid block drawn, raw HTML shown as text, a
// relative link opening its file in a tab, the choice kept per tab, Alt+P
// bringing the source back (with Neovim holding the keys too, where nvim is
// on PATH). A repository file is untrusted: an image on the web, a Mermaid
// image node, an HTML image in a Mermaid label and a url( in a diagram's
// CSS all stay unloaded, and the page makes no request outside its own
// origin while it shows them. Mermaid draws SVG only in a browser (vitest's
// node environment has no DOM), so the drawing is checked here.
const hasNvim = (() => {
  try {
    execFileSync('sh', ['-c', 'command -v nvim'], { stdio: 'ignore' })
    return true
  } catch {
    return false
  }
})()

test('a Markdown file previews rendered with its diagram drawn and nothing fetched from outside; a link opens its file; Alt+P brings the source back', async ({ page, api, state }) => {
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
      '![tracker](https://attacker.invalid/pixel.png)',
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
      '```mermaid',
      'flowchart LR',
      '  A@{ img: "https://attacker.invalid/node.png", h: 60 }',
      '```',
      '',
      '```mermaid',
      'flowchart LR',
      '  A --> B',
      '  style B fill:url(https://attacker.invalid/fill.svg#p)',
      '```',
      '',
      '```mermaid',
      'flowchart LR',
      '  A["<img src=https://attacker.invalid/label.png>"] --> B',
      '```',
      '',
      '```go',
      'package main',
      '```',
      '',
    ].join('\n'),
  )
  writeFileSync(join(cwd, 'docs', 'users.md'), '# Users\n\n```mermaid\nsequenceDiagram\n  Yard->>Roundhouse: who is free?\n```\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'markdown', cwd })
  // Every request the page makes to another origin: there must be none (the file names attacker.invalid four ways).
  const outside: string[] = []
  let origin = ''
  page.on('request', (r) => {
    const u = new URL(r.url())
    if (u.protocol === 'data:' || u.protocol === 'blob:') return
    // The first request is the page itself, from the server: its origin is the one everything else must have.
    if (!origin) origin = u.origin
    else if (u.origin !== origin) outside.push(r.url())
  })
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
    // The source first; Preview renders it: the heading, the table, the task list, the script as text, the code highlighted.
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
    await expect(preview.locator('pre.shiki')).toContainText('package main', { timeout: 30_000 })
    await expect(area.locator('[data-editor-pos]')).toHaveCount(0)
    // The image on the web is held as its alt text; the plain diagram is drawn; the three that would fetch are held with why.
    await expect(preview.locator('.md-image-held')).toHaveText('tracker')
    await expect(preview.locator('img')).toHaveCount(0)
    await expect(preview.locator('[data-mermaid-rendered] svg')).toBeVisible({ timeout: 30_000 })
    await expect(preview.locator('[data-mermaid-rendered]')).toHaveCount(1)
    await expect(preview.locator('[data-mermaid-rendered]')).toContainText('Roundhouse')
    const held = preview.locator('[data-mermaid-held]')
    await expect(held).toHaveCount(3)
    await expect(held.nth(0)).toContainText('not drawn: an image node')
    await expect(held.nth(1)).toContainText('not drawn: a url(')
    await expect(held.nth(2)).toContainText('not drawn: an HTML element')
    await expect(held.nth(2)).toContainText('attacker.invalid/label.png')
    expect(outside).toEqual([])
    // A relative link opens its file in a tab of its own, previewable in turn; the README's tab keeps its preview; switching
    // between the two previews (each a diagram of its own) leaves both drawn.
    await preview.locator('a[data-md-link]').click()
    await expect(area.locator('[data-editor-tab]')).toHaveCount(2)
    await expect(area.locator('[data-editor-tab-active]')).toContainText('users.md')
    await expect(toggle).toHaveAttribute('data-editor-preview', 'off')
    await toggle.click()
    await expect(preview.locator('h1')).toHaveText('Users')
    await expect(preview.locator('[data-mermaid-rendered] svg')).toBeVisible({ timeout: 30_000 })
    await area.locator('[data-editor-tab]', { hasText: 'README.md' }).click()
    await expect(toggle).toHaveAttribute('data-editor-preview', 'on')
    await expect(preview.locator('h1')).toHaveText('Signals')
    await expect(preview.locator('[data-mermaid-rendered] svg')).toBeVisible({ timeout: 30_000 })
    await area.locator('[data-editor-tab]', { hasText: 'users.md' }).click()
    await expect(preview.locator('h1')).toHaveText('Users')
    await expect(preview.locator('[data-mermaid-rendered] svg')).toBeVisible({ timeout: 30_000 })
    await expect(preview.locator('[data-mermaid-rendered]')).toContainText('who is free?')
    // Alt+P in the preview brings the source back; Alt+P in the text (Monaco's own action: Monaco keeps a chord it sees from the
    // page) shows the preview again, and once more the source.
    await preview.click()
    await page.keyboard.press('Alt+KeyP')
    await expect(toggle).toHaveAttribute('data-editor-preview', 'off')
    await expect(area.locator('[data-md-preview]')).toHaveCount(0)
    await expect(area.locator('[data-code-editor]')).toBeVisible()
    await expect(area.locator('[data-editor-pos]')).toBeVisible()
    await area.locator('.monaco-editor .view-lines').click()
    await page.keyboard.press('Alt+KeyP')
    await expect(toggle).toHaveAttribute('data-editor-preview', 'on')
    await expect(preview.locator('h1')).toHaveText('Users')
    await preview.click()
    await page.keyboard.press('Alt+KeyP')
    await expect(toggle).toHaveAttribute('data-editor-preview', 'off')
    // With Neovim holding the keys, Alt+P is still the preview's: it never reaches Neovim.
    if (hasNvim) {
      await area.locator('[data-editor-keymap]').click()
      await expect(area.locator('[data-editor-keymap]')).toHaveAttribute('data-editor-keymap', 'nvim')
      await expect(area.locator('[data-nvim-status]')).toHaveAttribute('data-nvim-mode', /^(n|normal)$/, { timeout: 30_000 })
      await area.locator('.monaco-editor .view-lines').click()
      await page.keyboard.press('Alt+KeyP')
      await expect(toggle).toHaveAttribute('data-editor-preview', 'on')
      await expect(preview.locator('h1')).toHaveText('Users')
      await preview.click()
      await page.keyboard.press('Alt+KeyP')
      await expect(toggle).toHaveAttribute('data-editor-preview', 'off')
      await expect(area.locator('[data-nvim-status]')).toHaveAttribute('data-nvim-mode', /^(n|normal)$/)
    }
    expect(outside).toEqual([])
  } finally {
    await api.stopSession(s.id)
  }
})
