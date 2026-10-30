import type { BrowserContext, Page } from '@playwright/test'
import { expect, test, type Api } from './fixtures'

// Dispatch's pages (docs/plans/dispatch.md, PR-8): a project made from the projects page, a ticket made from the project page's New
// ticket button, the ticket's page editing its title, its description (the preview shown), its priority and its owner, its history
// listing the fields set, the counts; and a second browser that sees the title edited and the projects page's open count move with no
// reload, by the dispatch events. The spec names what it made: a project of its own (E2E, or E2E1, E2E2… when a repeat on the same
// server finds the earlier ones), and counts only the rows of that project.
test.describe.configure({ mode: 'serial' })

let other: BrowserContext | null = null

test.afterEach(async () => {
  await other?.close()
  other = null
})

/** The first of E2E, E2E1, E2E2… that no project, archived or not, holds on this server. */
async function freeKey(api: Api): Promise<string> {
  const taken = new Set<string>()
  let cursor = ''
  do {
    const page = await api.ok<{ projects: Array<{ key: string }>; next?: string }>('GET', `/api/dispatch/projects?archived=1${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`)
    for (const p of page.projects) taken.add(p.key)
    cursor = page.next ?? ''
  } while (cursor)
  for (let i = 0; i < 1000; i++) {
    const key = i === 0 ? 'E2E' : `E2E${i}`
    if (!taken.has(key)) return key
  }
  throw new Error('every E2E key is taken on this server')
}

/** A second person's workbench on the same server. */
async function secondBrowser(context: BrowserContext, token: string): Promise<Page> {
  await context.addInitScript((t) => {
    localStorage.setItem('conductor.workbenchToken', t)
    localStorage.setItem('conductor.displayName', 'Priya')
  }, token)
  return context.newPage()
}

/** Marks the page's window, to tell later that it was not loaded again. */
async function mark(page: Page): Promise<void> {
  await page.evaluate(() => ((window as unknown as { dispatchMark?: string }).dispatchMark = 'kept'))
}
async function marked(page: Page): Promise<string | undefined> {
  return page.evaluate(() => (window as unknown as { dispatchMark?: string }).dispatchMark)
}

test('a project, a ticket made from its page and edited on its own page, its history and counts, and a second browser that follows', async ({ page, api, state, browser }) => {
  const key = await freeKey(api)
  const name = `E2E project ${key}`
  const owner = `Owner ${key}`
  await page.addInitScript(() => localStorage.setItem('conductor.displayName', 'Nate'))

  // New project, from the projects page: the key is upper case as it is typed.
  await page.goto('/dispatch')
  await page.locator('[data-new-project-open]').click()
  const dialog = page.locator('[data-new-project]')
  await expect(dialog).toBeVisible()
  await dialog.locator('[data-new-project-key]').fill(key.toLowerCase())
  await expect(dialog.locator('[data-new-project-key]')).toHaveValue(key)
  await dialog.locator('[data-new-project-name]').fill(name)
  await page.locator('[data-new-project-submit]').click()
  // Its page opens, with no tickets yet.
  await expect(page).toHaveURL(new RegExp(`/dispatch/${key}$`))
  await expect(page.locator('[data-project-name]')).toHaveText(name)
  await expect(page.locator('[data-list-empty]')).toBeVisible()

  // A second browser on the projects page: the project's row, nothing open.
  other = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const second = await secondBrowser(other, state.token)
  await second.goto('/dispatch')
  const secondRow = second.locator(`[data-project-row="${key}"]`)
  await expect(secondRow.locator('[data-project-name]')).toHaveText(name)
  await expect(secondRow.locator('[data-project-open]')).toHaveAttribute('data-count', '0')
  await mark(second)

  // New ticket, from the project page's button: the project's first, its key KEY-1.
  await page.locator('[data-new-ticket-open]').click()
  await page.locator('[data-new-ticket-title]').fill('Fix the login')
  await page.locator('[data-new-ticket-submit]').click()
  const row = page.locator(`[data-ticket-row="${key}-1"]`)
  await expect(row.locator('[data-row-title]')).toHaveText('Fix the login')
  await expect(row.locator('[data-row-key]')).toHaveText(`${key}-1`)
  await expect(page.locator('[data-list-total]')).toHaveAttribute('data-count', '1')

  // The second browser's projects page counts it, with no reload.
  await expect(secondRow.locator('[data-project-open]')).toHaveAttribute('data-count', '1')
  expect(await marked(second)).toBe('kept')

  // The ticket's page, from its row.
  await row.click()
  await expect(page).toHaveURL(new RegExp(`/dispatch/${key}/1$`))
  await expect(page.locator('[data-ticket-key]')).toHaveText(`${key}-1`)
  await expect(page.locator('[data-ticket-title]')).toHaveText('Fix the login')

  // The second browser on the same ticket.
  await second.goto(`/dispatch/${key}/1`)
  await expect(second.locator('[data-ticket-title]')).toHaveText('Fix the login')
  await mark(second)

  // The title, edited in place: the second browser sees it with no reload.
  await page.locator('[data-title-edit]').click()
  await page.locator('[data-title-input]').fill('Fix the login page')
  await page.locator('[data-title-input]').press('Enter')
  await expect(page.locator('[data-ticket-title]')).toHaveText('Fix the login page')
  await expect(second.locator('[data-ticket-title]')).toHaveText('Fix the login page')
  expect(await marked(second)).toBe('kept')

  // The description, written as Markdown: its preview while it is written, and rendered once saved.
  const description = page.locator('[data-ticket-markdown="description"]')
  await description.locator('[data-markdown-empty]').click()
  await description.locator('[data-markdown-input]').fill('## Steps\n\n1. Open the **login** page\n2. See [the notes](notes.md)')
  await description.getByRole('tab', { name: 'Preview' }).click()
  await expect(description.locator('[data-markdown-draft-preview] h2')).toHaveText('Steps')
  await description.locator('[data-markdown-save]').click()
  const view = description.locator('[data-markdown-view]')
  await expect(view.locator('h2')).toHaveText('Steps')
  await expect(view.locator('strong')).toHaveText('login')
  // A relative link names no file of a ticket's: its words stay, and nothing opens.
  await expect(view.locator('.md-link-held')).toHaveText('the notes')
  await expect(view.locator('a')).toHaveCount(0)

  // The priority.
  await page.locator('[data-ticket-priority]').click()
  await page.getByRole('option', { name: 'Urgent' }).click()
  await expect(page.locator('[data-ticket-priority]')).toContainText('Urgent')

  // The owner: a name typed, which the directory gains as it is set.
  await page.locator('[data-ticket-owner]').click()
  await page.getByPlaceholder('A name…', { exact: true }).fill(owner)
  await page.getByRole('option', { name: `Make ${owner} the owner` }).click()
  await expect(page.locator('[data-ticket-owner]')).toContainText(owner)
  const people = await api.ok<{ people: Array<{ name: string }> }>('GET', '/api/dispatch/people')
  expect(people.people.map((p) => p.name)).toContain(owner)

  // The assignees: one of the catalog's agents and a person typed, ticked in a row, each one operation on the revision the one before
  // left, both kept.
  const helper = `Helper ${key}`
  await page.locator('[data-ticket-assignees]').click()
  await page.getByRole('option', { name: 'Claude Code (e2e stub)', exact: true }).click()
  await page.getByPlaceholder('A name or an agent…').fill(helper)
  await page.getByRole('option', { name: `Assign ${helper}`, exact: true }).click()
  await page.keyboard.press('Escape')
  const chips = page.locator('[data-ticket-assignees] [data-assignee]')
  await expect(chips).toHaveCount(2)
  await expect(page.locator(`[data-ticket-assignees] [data-assignee="person:${helper}"]`)).toContainText(helper)
  await expect(page.locator('[data-ticket-assignees] [data-assignee="agent:claude"]')).toContainText('Claude Code (e2e stub)')

  // The history lists every field set, under who set it.
  const history = page.locator('[data-ticket-history]')
  await expect(history).toContainText('made the ticket')
  await expect(history).toContainText('set the title to "Fix the login page"')
  await expect(history).toContainText('wrote the description')
  await expect(history).toContainText('set the priority to Urgent')
  await expect(history).toContainText(`made ${owner} the owner`)
  await expect(history).toContainText('assigned Claude Code (e2e stub)')
  await expect(history).toContainText(`assigned ${helper}`)
  await expect(history.locator('[data-history-who]').first()).toHaveText('Nate')

  // The second browser followed every edit, still with no reload.
  await expect(second.locator('[data-ticket-priority]')).toContainText('Urgent')
  await expect(second.locator('[data-ticket-owner]')).toContainText(owner)
  await expect(second.locator('[data-ticket-markdown="description"] [data-markdown-view] h2')).toHaveText('Steps')
  expect(await marked(second)).toBe('kept')

  // The counts: the project's list holds its one ticket, and the projects page counts it open, none done.
  await page.goto(`/dispatch/${key}`)
  await expect(page.locator(`[data-ticket-row="${key}-1"] [data-row-title]`)).toHaveText('Fix the login page')
  await expect(page.locator('[data-list-total]')).toHaveAttribute('data-count', '1')
  await page.goto('/dispatch')
  const mine = page.locator(`[data-project-row="${key}"]`)
  await expect(mine.locator('[data-project-open]')).toHaveAttribute('data-count', '1')
  await expect(mine.locator('[data-project-done]')).toHaveAttribute('data-count', '0')
})
