import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The editor area (design 4b, 4c, 4f): a file chosen in the Files pane opens
// in Monaco above a shrunk terminal, with a tab strip, the breadcrumb, the
// position, copy path and open raw; several files are tabs; T folds the
// editor to its strip and brings it back; closing the last tab takes the
// area away; a typed path:line opens at the line; an image and a binary
// file show their states.
test('a file opens in the editor above the terminal; tabs, folding, closing, a line, an image and a binary file', async ({ page, api, state }) => {
  const cwd = join(state.root, 'files-editor')
  mkdirSync(join(cwd, 'internal', 'api'), { recursive: true })
  writeFileSync(join(cwd, 'internal', 'api', 'users.go'), 'package api\n\n// ListUsers answers GET /v1/users with a page of users.\nfunc ListUsers() {}\n\n// GetUser answers GET /v1/users/{id}.\nfunc GetUser() {}\n')
  writeFileSync(join(cwd, 'README.md'), '# editor\n')
  writeFileSync(join(cwd, 'logo.png'), Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAC0lEQVQI12NgQAcAABIAAe+JVKQAAAAASUVORK5CYII=', 'base64'))
  writeFileSync(join(cwd, 'fixtures.bin'), Buffer.from([0, 1, 2, 3, 255, 254, 0, 0, 7, 9]))
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'editor', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const tree = page.locator('[data-files-tree]')
    await expect(tree.locator(`[data-file-node="${cwd}/internal"]`)).toBeVisible({ timeout: 30_000 })
    await expect(page.locator('[data-editor-area]')).toHaveCount(0)
    // A file from the tree: Monaco above the terminal, the pane still the tree.
    await tree.locator(`[data-file-node="${cwd}/internal"]`).click()
    await tree.locator(`[data-file-node="${cwd}/internal/api"]`).click()
    await tree.locator(`[data-file-node="${cwd}/internal/api/users.go"]`).click()
    const area = page.locator('[data-editor-area]')
    await expect(area).toHaveAttribute('data-editor-area', 'open')
    await expect(area.locator('[data-editor-tab]')).toHaveCount(1)
    await expect(area.locator('[data-editor-state]')).toHaveAttribute('data-editor-state', 'text', { timeout: 30_000 })
    await expect(area.locator('.monaco-editor .view-lines')).toContainText('ListUsers', { timeout: 30_000 })
    await expect(area.locator('[data-editor-crumbs]')).toContainText('users.go')
    await expect(area.locator('[data-editor-pos]')).toHaveText('Ln 1, Col 1')
    await expect(area.locator('[data-editor-raw]')).toBeVisible()
    await expect(page.locator('[data-files-mode]')).toHaveAttribute('data-files-mode', 'tree')
    await expect(page.locator('.terminal-host .xterm-screen').first()).toBeVisible()
    // A second file: two tabs, the new one active; the first by its tab.
    await tree.locator(`[data-file-node="${cwd}/README.md"]`).click()
    await expect(area.locator('[data-editor-tab]')).toHaveCount(2)
    await expect(area.locator('[data-editor-tab-active]')).toContainText('README.md')
    await area.locator(`[data-editor-tab="file:${cwd}/internal/api/users.go"] button`).first().click()
    await expect(area.locator('[data-editor-tab-active]')).toContainText('users.go')
    // T folds the editor to its strip; T again brings it back.
    await page.locator('body').click({ position: { x: 5, y: 5 } })
    await page.keyboard.press('t')
    await expect(area).toHaveAttribute('data-editor-area', 'folded')
    await expect(area.locator('[data-editor-count]')).toHaveText('2 files open')
    await page.keyboard.press('t')
    await expect(area).toHaveAttribute('data-editor-area', 'open')
    // A typed path:line opens at the line, marked.
    const box = page.locator('[data-files-box] input, input[data-files-box]').first()
    await box.fill('internal/api/users.go:7')
    await box.press('Enter')
    await expect(area.locator('[data-editor-pos]')).toHaveText('Ln 7, Col 1', { timeout: 15_000 })
    await expect(area.locator('[data-editor-tab]')).toHaveCount(2)
    // An image on a checker with its size; a binary file says so.
    await tree.locator(`[data-file-node="${cwd}/logo.png"]`).click()
    await expect(area.locator('[data-editor-state]')).toHaveAttribute('data-editor-state', 'image', { timeout: 15_000 })
    await tree.locator(`[data-file-node="${cwd}/fixtures.bin"]`).click()
    await expect(area.locator('[data-editor-state]')).toHaveAttribute('data-editor-state', 'binary', { timeout: 15_000 })
    await expect(area).toContainText('Binary file; nothing to show')
    // Closing tabs, the last close takes the area away.
    await expect(area.locator('[data-editor-tab]')).toHaveCount(4)
    await area.locator(`[data-editor-close="file:${cwd}/fixtures.bin"]`).click({ force: true })
    await expect(area.locator('[data-editor-tab]')).toHaveCount(3)
    await area.locator('[data-editor-close-all]').click()
    await expect(page.locator('[data-editor-area]')).toHaveCount(0)
  } finally {
    await api.stopSession(s.id)
  }
})
