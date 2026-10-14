import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Browser, BrowserContext, Page } from '@playwright/test'
import { expect, test, type Api, type Session } from './fixtures'

// A short pane (#189): the terminal's slot in the editor column can be 8rem tall, and the terminal fits the rows that show in
// it, never more: its last row and the cursor's row lie inside the pane, on the session page, in the Yard's focus and on a
// guest's join page, on a short window and with a file open in the editor above it. Measured by geometry, whatever the renderer:
// the screen's box over the session's rows, and the cursor where xterm keeps its input box; never the text.

/** A window a few lines tall: the terminal's slot is all the column has, under 192 px. */
const SHORT = { width: 1440, height: 230 }
/** With a file open the editor takes the column's top 60 % and the terminal's slot is under 192 px. */
const EDITOR = { width: 1440, height: 480 }

const sessions: string[] = []

test.afterAll(async ({ api }) => {
  for (const id of sessions) await api.stopSession(id)
})

/** A shell in a folder with a file to open. */
async function shell(api: Api, root: string, name: string): Promise<{ s: Session; cwd: string }> {
  const cwd = join(root, name)
  mkdirSync(cwd, { recursive: true })
  writeFileSync(join(cwd, 'notes.md'), '# notes\n')
  const s = await api.ok<Session>('POST', '/api/sessions', { agentId: 'shell', name, cwd })
  sessions.push(s.id)
  return { s, cwd }
}

async function rows(api: Api, id: string): Promise<number> {
  return ((await api.session(id)) as Session & { rows: number }).rows
}

let marks = 0

/**
 * Fills the terminal with output once the session's size has settled, so that the cursor is on its last row, then checks the
 * last row and the cursor's row lie inside the pane: what of the terminal shows, every box that clips it and the window.
 */
async function expectRowsInPane(page: Page, api: Api, id: string, cwd: string, where: string): Promise<void> {
  await expect(page.locator('.terminal-host.terminal-scale'), `${where}: this window sizes the session`).toHaveCount(0)
  let last = 0
  await expect
    .poll(
      async () => {
        const now = await rows(api, id)
        const same = now === last
        last = now
        return same
      },
      { timeout: 15_000, intervals: [700], message: `${where}: the size settles` },
    )
    .toBe(true)
  const mark = `mark-${++marks}`
  await page.locator('.terminal-host .xterm-helper-textarea').first().focus()
  await page.keyboard.type(`seq 300; touch ${mark}\n`)
  await expect.poll(() => existsSync(join(cwd, mark)), { timeout: 15_000, message: `${where}: the shell ran the line` }).toBe(true)

  const measure = async () => {
    const n = await rows(api, id)
    const g = await page.locator('.terminal-host').first().evaluate((host) => {
      // The pane: the host and every box around it that clips, and the window.
      let top = 0
      let bottom = window.innerHeight
      for (let el: Element | null = host; el; el = el.parentElement) {
        if (getComputedStyle(el).overflowY === 'visible') continue
        const r = el.getBoundingClientRect()
        top = Math.max(top, r.top)
        bottom = Math.min(bottom, r.bottom)
      }
      const screen = host.querySelector('.xterm-screen')!.getBoundingClientRect()
      const cursor = host.querySelector('.xterm-helper-textarea')!.getBoundingClientRect()
      return { pane: { top, bottom }, screen: { top: screen.top, height: screen.height }, cursorTop: cursor.top }
    })
    const cell = g.screen.height / n
    return { rows: n, cell, cursorRow: (g.cursorTop - g.screen.top) / cell, ...g }
  }
  // The output reached the screen: the cursor is on the session's last row (its distance from it, in rows, is 0).
  await expect
    .poll(
      async () => {
        const m = await measure()
        return Math.round((m.rows - 1 - m.cursorRow) * 100) / 100
      },
      { timeout: 15_000, message: `${where}: the cursor on the last row` },
    )
    .toBe(0)
  const m = await measure()
  const detail = `${where}: ${JSON.stringify(m)}`
  expect(m.rows, detail).toBeGreaterThan(2)
  expect(m.screen.top, `${detail}: the first row shows`).toBeGreaterThanOrEqual(m.pane.top - 0.5)
  expect(m.screen.top + m.rows * m.cell, `${detail}: the last row shows`).toBeLessThanOrEqual(m.pane.bottom + 0.5)
  expect(m.screen.top + (m.cursorRow + 1) * m.cell, `${detail}: the cursor's row shows`).toBeLessThanOrEqual(m.pane.bottom + 0.5)
}

type Where = 'the session page' | "the Yard's focus" | "a guest's join page"

/**
 * Opens the session where the test says, at a size, as the window that sizes it: the workbench's page, or a guest's on a control
 * link, which follows the size until Fit to my window (round 14).
 */
async function open(where: Where, page: Page, browser: Browser, api: Api, s: Session, size: { width: number; height: number }): Promise<Page> {
  if (where === "a guest's join page") {
    const { token } = await api.ok<{ token: string }>('POST', `/api/sessions/${s.id}/links`, { role: 'control', ttlSeconds: 3600 })
    const ctx = await browser.newContext({ viewport: size })
    contexts.push(ctx)
    await ctx.addInitScript(() => localStorage.setItem('conductor.displayName', 'Jane'))
    const jane = await ctx.newPage()
    await jane.goto(`/join/${token}`)
    await jane.getByRole('button', { name: 'Join session' }).click()
    await expect(jane.locator('.terminal-host .xterm-screen')).toBeVisible({ timeout: 30_000 })
    await jane.locator('[data-terminal-sizer] [data-fit-mine]').click({ timeout: 30_000 })
    return jane
  }
  await page.setViewportSize(size)
  await page.goto(where === 'the session page' ? `/sessions/${s.id}` : `/yard?focus=${encodeURIComponent(s.id)}`)
  await expect(page.locator('.terminal-host .xterm-screen')).toBeVisible({ timeout: 30_000 })
  return page
}

/** Opens the folder's file in the editor above the terminal, from the Files tab of the session page's inspector or the Files pane beside the column. */
async function openFile(where: Where, p: Page, cwd: string): Promise<void> {
  if (where === 'the session page') {
    await p.locator('[data-inspector] button', { hasText: 'Files' }).click()
    await p.locator(`[data-files-tree] [data-file-node="${cwd}/notes.md"]`).click()
  } else {
    await p.locator(`[data-files-aside] [data-file-node="${cwd}/notes.md"]`).click()
  }
  await expect(p.locator('[data-editor-area]')).toHaveAttribute('data-editor-area', 'open')
}

const contexts: BrowserContext[] = []
test.afterEach(async () => {
  for (const ctx of contexts.splice(0)) await ctx.close()
})

for (const where of ['the session page', "the Yard's focus", "a guest's join page"] as const) {
  test(`${where}, a short window: the terminal's last row and the cursor's row show`, async ({ page, browser, api, state }) => {
    const { s, cwd } = await shell(api, state.root, `short-${sessions.length}`)
    const p = await open(where, page, browser, api, s, SHORT)
    await expectRowsInPane(p, api, s.id, cwd, `${where}, a short window`)
  })

  test(`${where}, a file open above the terminal: its last row and the cursor's row show`, async ({ page, browser, api, state }) => {
    const { s, cwd } = await shell(api, state.root, `editor-${sessions.length}`)
    const p = await open(where, page, browser, api, s, EDITOR)
    await openFile(where, p, cwd)
    await expectRowsInPane(p, api, s.id, cwd, `${where}, a file open`)
  })
}
