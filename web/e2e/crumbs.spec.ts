import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Session } from './fixtures'

// The breadcrumb strip (#315): a working directory whose name overflows the
// strip shows its end, scrolled there by the watch that reads the crumbs,
// which used to throw at mount (its source read `crumbs` above the line
// declaring it) and so never scrolled; the pane mounts with no such error
// reaching the console.
test('a long working directory shows the end of its crumbs: the strip is scrolled to its end, and the pane mounts without a reference error', async ({ page, api, state }) => {
  // The working directory's own folder keeps its whole name in the strip (#169): one long enough overflows the pane.
  const cwd = join(state.root, 'crumbs-deep', 'a-working-directory-whose-own-folder-name-is-long-enough-to-overflow-the-breadcrumb-strip-of-the-pane')
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'notes.txt'), 'deep\n')
  const errors: string[] = []
  page.on('console', (m) => {
    if (m.type() === 'error') errors.push(m.text())
  })
  page.on('pageerror', (e) => errors.push(e.message))
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'claude', name: 'e2e crumbs', cwd })
  try {
    await page.goto(`/sessions/${s.id}`)
    await page.locator('[data-inspector] [data-rail-item="explorer"]').click()
    const pane = page.locator('[data-files-mode]')
    await expect(pane.locator('[data-files-tree] [data-file-node]').first()).toBeVisible({ timeout: 30_000 })
    const nav = pane.locator('[data-files-crumbs]')
    await expect(nav.locator('[data-files-crumb]').last()).toHaveText(/^a-working-directory/)
    // The strip overflows its pane: it is scrolled to its end, the last crumbs in view.
    await expect.poll(() => nav.evaluate((el) => el.scrollWidth - el.clientWidth), { timeout: 10_000 }).toBeGreaterThan(20)
    await expect.poll(() => nav.evaluate((el) => el.scrollWidth - el.clientWidth - el.scrollLeft), { timeout: 10_000 }).toBeLessThanOrEqual(1)
    expect(errors.filter((e) => /before initialization/i.test(e))).toEqual([])
  } finally {
    await api.stopSession(s.id)
  }
})
