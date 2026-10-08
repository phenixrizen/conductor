import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The Files tab as the Explorer (design 4a): the working directory listed as
// soon as the tab opens, folders expanding in place, the breadcrumb from the
// top, the box filtering the tree or opening a typed path:line, the hint
// that paths in the terminal are clickable; a crumb brings the tree back.
test('the Files tab opens on the working directory as a tree; the box filters it or opens a path at a line', async ({ page, api, state }) => {
  // A working directory of this test's own (the shared scratch repository gains files from other specs).
  const cwd = join(state.root, 'files-explorer')
  mkdirSync(join(cwd, 'internal', 'api'), { recursive: true })
  mkdirSync(join(cwd, 'docs'), { recursive: true })
  mkdirSync(join(cwd, 'empty'), { recursive: true })
  writeFileSync(join(cwd, 'README.md'), '# files\n')
  writeFileSync(join(cwd, 'internal', 'api', 'users.go'), 'package api\n\n// ListUsers answers GET /v1/users.\nfunc ListUsers() {}\n')
  writeFileSync(join(cwd, 'internal', 'api', 'router.go'), 'package api\n')
  writeFileSync(join(cwd, 'docs', 'users.md'), '# users\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'files', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    await expect(pane).toHaveAttribute('data-files-mode', 'tree')
    // The working directory, listed: folders first, then files; the breadcrumb ends with its folder.
    const tree = pane.locator('[data-files-tree]')
    await expect(tree.locator('[data-file-node]')).toHaveCount(4, { timeout: 30_000 })
    await expect(tree.locator('[data-file-node]')).toHaveText([/docs/, /empty/, /internal/, /README\.md/])
    await expect(pane.locator('[data-files-crumbs] [data-files-crumb]').last()).toHaveText(cwd.split('/').pop()!)
    await expect(pane.locator('[data-files-hint]')).toContainText('Paths the agent prints in the terminal are clickable')
    // A folder expands in place with its own listing; an empty one says so.
    await tree.locator(`[data-file-node="${cwd}/internal"]`).click()
    await expect(tree.locator(`[data-file-node="${cwd}/internal/api"]`)).toBeVisible()
    await tree.locator(`[data-file-node="${cwd}/internal/api"]`).click()
    await expect(tree.locator(`[data-file-node="${cwd}/internal/api/users.go"]`)).toBeVisible()
    await tree.locator(`[data-file-node="${cwd}/empty"]`).click()
    await expect(tree.locator(`[data-file-empty="${cwd}/empty"]`)).toHaveText('Empty directory')
    // The filter narrows the loaded tree to the names that match, their folders kept.
    const box = pane.locator('[data-files-box] input, input[data-files-box]').first()
    await box.fill('users')
    await expect(tree.locator('[data-file-node]')).toHaveText([/internal/, /api/, /users\.go/])
    await box.press('Escape')
    await expect(tree.locator('[data-file-node]')).toHaveCount(7)
    // A typed path:line opens the file at that line.
    await box.fill('internal/api/users.go:4')
    await box.press('Enter')
    await expect(pane).toHaveAttribute('data-files-mode', 'file')
    await expect(pane.locator('.line.target')).toContainText('func ListUsers')
    await expect(pane.locator('[data-files-crumbs]')).toContainText('users.go')
    // A crumb brings the tree back, opened down to that folder.
    await pane.locator(`[data-files-crumb="${cwd}/internal/api"]`).click()
    await expect(pane).toHaveAttribute('data-files-mode', 'tree')
    await expect(tree.locator(`[data-file-node="${cwd}/internal/api/router.go"]`)).toBeVisible()
    await expect(tree.locator(`[data-file-node="${cwd}/internal/api"]`)).toHaveAttribute('data-file-expanded', '')
    // A file in the tree opens it; Back returns to the tree.
    await tree.locator(`[data-file-node="${cwd}/docs"]`).click()
    await tree.locator(`[data-file-node="${cwd}/docs/users.md"]`).click()
    await expect(pane).toHaveAttribute('data-files-mode', 'file')
    await pane.locator('[data-files-back]').click()
    await expect(pane).toHaveAttribute('data-files-mode', 'tree')
  } finally {
    await api.stopSession(s.id)
  }
})
