import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// Touched (design 4e): the files the agent read, edited, wrote or deleted,
// newest first, each with the tool, the agent and the time, from the file
// events its hooks report; a dot on a touched file in the Explorer; the
// same events in the Activity tab and on the Events page, quiet there.
test('Touched lists the files the agent touched, newest first, marks them in the Explorer, and the feeds carry them quietly', async ({ page, api, state }) => {
  const cwd = join(state.root, 'files-touched')
  mkdirSync(join(cwd, 'internal', 'api'), { recursive: true })
  writeFileSync(join(cwd, 'internal', 'api', 'users.go'), 'package api\n')
  writeFileSync(join(cwd, 'internal', 'api', 'router.go'), 'package api\n')
  writeFileSync(join(cwd, 'README.md'), '# touched\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'touched', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] button', { hasText: 'Files' }).click()
    const pane = page.locator('[data-files-mode]')
    await expect(pane.locator('[data-files-tree] [data-file-node]').first()).toBeVisible({ timeout: 30_000 })
    // The agent's hooks report, in order: a read, an edit, a repeat of the edit (one line), a write.
    const post = (body: Record<string, unknown>) => api.ok('POST', `/api/sessions/${s.id}/events`, body)
    await post({ type: 'file', op: 'read', path: 'internal/api/router.go', tool: 'Read' })
    await post({ type: 'file', op: 'edit', path: `${cwd}/internal/api/users.go`, tool: 'Edit' })
    await post({ type: 'file', op: 'edit', path: `${cwd}/internal/api/users.go`, tool: 'Edit' })
    await post({ type: 'file', op: 'write', path: 'README.md', tool: 'Write' })
    await expect(pane.locator('[data-files-touched-count]')).toHaveText('3', { timeout: 15_000 })
    await pane.locator('[data-files-section="touched"]').click()
    const rows = pane.locator('[data-files-touched] [data-touched]')
    await expect(rows).toHaveCount(3)
    await expect(rows.nth(0)).toHaveAttribute('data-touched', `${cwd}/README.md`)
    await expect(rows.nth(0)).toHaveAttribute('data-touched-op', 'write')
    await expect(rows.nth(0)).toContainText('Write')
    await expect(rows.nth(1)).toHaveAttribute('data-touched', `${cwd}/internal/api/users.go`)
    await expect(rows.nth(2)).toHaveAttribute('data-touched-op', 'read')
    await expect(pane.locator('[data-files-touched-head]')).toContainText('3 since the session started')
    await expect(pane.locator('[data-files-touched-foot]')).toContainText('quiet there by default')
    // A touched row opens the file; the Explorer marks the touched files with a dot.
    await rows.nth(1).click()
    await expect(page.locator('[data-editor-area] [data-editor-crumbs]')).toContainText('users.go', { timeout: 30_000 })
    await pane.locator('[data-files-section="explorer"]').click()
    await expect(pane.locator(`[data-file-node="${cwd}/README.md"] [data-file-touched]`)).toBeVisible()
    await pane.locator(`[data-file-node="${cwd}/internal"]`).click()
    await pane.locator(`[data-file-node="${cwd}/internal/api"]`).click()
    await expect(pane.locator(`[data-file-node="${cwd}/internal/api/users.go"] [data-file-touched]`)).toBeVisible()
    // The Activity tab words it; the Events page lists what arrives in the feed (a page load starts the feed afresh) without a badge on the session.
    await page.locator('[data-inspector] button', { hasText: 'Activity' }).click()
    await expect(page.locator('[data-inspector] [data-activity="file"]').first()).toContainText('wrote README.md · Write')
    await page.goto('/events')
    await expect(page.locator('[data-events-feed-tab]')).toBeVisible()
    await post({ type: 'file', op: 'delete', path: 'internal/api/router.go', tool: 'Bash' })
    const row = page.locator('[data-event-feed] [data-event="file"]').filter({ hasText: 'router.go' }).first()
    await expect(row).toBeVisible({ timeout: 15_000 })
    await expect(row).toContainText('deleted internal/api/router.go · Bash')
    await expect(page.locator(`[data-sidebar-row="s:${s.id}"] [data-event-mark]`)).toHaveCount(0)
  } finally {
    await api.stopSession(s.id)
  }
})
