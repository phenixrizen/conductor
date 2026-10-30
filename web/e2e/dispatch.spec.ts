import type { BrowserContext, Page } from '@playwright/test'
import { expect, test, type Api } from './fixtures'

// Dispatch's pages (docs/plans/dispatch.md, PR-8): a project made from the projects page, a ticket made from the project page's New
// ticket button, the ticket's page editing its title, its description (the preview shown), its priority and its owner, its history
// listing the fields set, the counts; and a second browser that sees the title edited and the projects page's open count move with no
// reload, by the dispatch events. Then what review round 1 found: two assignees ticked while the first one's edit is held both kept; an
// editor's save that never replaces another person's text unseen; a filter whose first read fails read again; a ticket's page opened
// before its ticket is made. The spec names what it made: a project of its own per test (E2E, or E2E1, E2E2… when the server holds the
// earlier ones), and counts only the rows of that project.
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

/** An op id of the spec's own, unique on the server since the project's key is: E2E3-t1. */
function opOf(key: string, what: string): string {
  return `${key.toLowerCase()}-${what}`
}

/** A project made through the API, and its first ticket when `title` is given (its key KEY-1). */
async function made(api: Api, key: string, title?: string): Promise<void> {
  await api.ok('POST', '/api/dispatch/projects', { opId: opOf(key, 'p'), key, name: `E2E project ${key}` })
  if (title) await api.ok('POST', '/api/dispatch/tickets', { opId: opOf(key, 't1'), project: key, title })
}

/** The ticket as the store holds it. */
async function stored(api: Api, key: string) {
  return (await api.ok<{ ticket: { title: string; description: string; acceptance: string; assignees: string[] } }>('GET', `/api/dispatch/tickets/${key}`)).ticket
}

/** What the page's edits of a ticket sent, in order, by the request's body, the edit route held where `hold` says (from 1). */
async function recordEdits(page: Page, hold?: { n: number; until: Promise<void> }): Promise<Array<{ set?: Record<string, unknown>; add?: { assignees?: string[] }; remove?: { assignees?: string[] } }>> {
  const sent: Array<{ set?: Record<string, unknown>; add?: { assignees?: string[] }; remove?: { assignees?: string[] } }> = []
  await page.route('**/api/dispatch/tickets/*/edit', async (route) => {
    sent.push(route.request().postDataJSON())
    if (hold && sent.length === hold.n) await hold.until
    await route.continue()
  })
  return sent
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

  // The assignees, the first tick's edit held on its way: Codex ticked meanwhile, and Goose ticked and unticked. The picker shows the
  // picks while they wait; both agents are kept, each one operation made on the ticket as the edit before it left it, and Goose, ticked
  // and unticked before its edit went, is no operation. Then a person typed, the next operation, with nothing sent between.
  let release!: () => void
  const sent = await recordEdits(page, { n: 1, until: new Promise<void>((resolve) => (release = resolve)) })
  const assignees = page.locator('[data-ticket-assignees]')
  const chips = assignees.locator('[data-assignee]')
  const revAt = Number(await page.locator('[data-ticket-rev]').getAttribute('data-ticket-rev'))
  await assignees.click()
  await page.getByRole('option', { name: 'Claude Code (e2e stub)', exact: true }).click()
  await expect.poll(() => sent.length).toBe(1)
  await page.getByRole('option', { name: 'Codex CLI (e2e stub)', exact: true }).click()
  await page.getByRole('option', { name: 'Goose (e2e stub)', exact: true }).click()
  await expect(assignees.locator('[data-assignee="agent:goose"]')).toHaveCount(1)
  await page.getByRole('option', { name: 'Goose (e2e stub)', exact: true }).click()
  await expect(assignees.locator('[data-assignee="agent:goose"]')).toHaveCount(0)
  await expect(chips).toHaveCount(2)
  release()
  await expect(page.locator('[data-ticket-rev]')).toHaveAttribute('data-ticket-rev', String(revAt + 2))
  expect([...(await stored(api, `${key}-1`)).assignees].sort()).toEqual(['agent:claude', 'agent:codex'])
  const helper = `Helper ${key}`
  await page.getByPlaceholder('A name or an agent…').fill(helper)
  await page.getByRole('option', { name: `Assign ${helper}`, exact: true }).click()
  await page.keyboard.press('Escape')
  await expect(page.locator('[data-ticket-rev]')).toHaveAttribute('data-ticket-rev', String(revAt + 3))
  await page.unroute('**/api/dispatch/tickets/*/edit')
  expect(sent.map((b) => [b.add?.assignees ?? [], b.remove?.assignees ?? []])).toEqual([
    [['agent:claude'], []],
    [['agent:codex'], []],
    [[`person:${helper}`], []],
  ])
  await expect(chips).toHaveCount(3)
  await expect(assignees.locator(`[data-assignee="person:${helper}"]`)).toContainText(helper)
  await expect(assignees.locator('[data-assignee="agent:claude"]')).toContainText('Claude Code (e2e stub)')
  await expect(assignees.locator('[data-assignee="agent:codex"]')).toContainText('Codex CLI (e2e stub)')

  // The history lists every field set, under who set it.
  const history = page.locator('[data-ticket-history]')
  await expect(history).toContainText('made the ticket')
  await expect(history).toContainText('set the title to "Fix the login page"')
  await expect(history).toContainText('wrote the description')
  await expect(history).toContainText('set the priority to Urgent')
  await expect(history).toContainText(`made ${owner} the owner`)
  await expect(history).toContainText('assigned Claude Code (e2e stub)')
  await expect(history).toContainText('assigned Codex CLI (e2e stub)')
  await expect(history).toContainText(`assigned ${helper}`)
  await expect(history).not.toContainText('Goose')
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

test("an editor's save never replaces another person's edit unseen: their text is said, the draft kept, and a second save replaces it", async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key, 'The login')
  const sent = await recordEdits(page)
  await page.goto(`/dispatch/${key}/1`)
  await expect(page.locator('[data-ticket-title]')).toHaveText('The login')
  const rev = page.locator('[data-ticket-rev]')
  await expect(rev).toHaveAttribute('data-ticket-rev', '1')

  // The description, opened and written; another person saves theirs meanwhile, which the page reads live behind the editor.
  const description = page.locator('[data-ticket-markdown="description"]')
  await description.locator('[data-markdown-empty]').click()
  await description.locator('[data-markdown-input]').fill('Mine')
  await api.ok('POST', `/api/dispatch/tickets/${key}-1/edit`, { opId: opOf(key, 'theirs-d'), byName: 'Priya', set: { description: 'Theirs' } })
  await expect(rev).toHaveAttribute('data-ticket-rev', '2')
  // Save sends nothing: their text is shown and the draft kept.
  await description.locator('[data-markdown-save]').click()
  await expect(description.locator('[data-markdown-theirs-text]')).toHaveText('Theirs')
  await expect(description.locator('[data-markdown-input]')).toHaveValue('Mine')

  // The title, the same way.
  await page.locator('[data-title-edit]').click()
  await page.locator('[data-title-input]').fill('My login')
  await api.ok('POST', `/api/dispatch/tickets/${key}-1/edit`, { opId: opOf(key, 'theirs-t'), byName: 'Priya', set: { title: 'Their login' } })
  await expect(rev).toHaveAttribute('data-ticket-rev', '3')
  await page.locator('[data-title-input]').press('Enter')
  await expect(page.locator('[data-title-theirs]')).toContainText('Their login')
  await expect(page.locator('[data-title-input]')).toHaveValue('My login')
  expect(await stored(api, `${key}-1`)).toMatchObject({ title: 'Their login', description: 'Theirs' })

  // A second save replaces each, knowingly; the page sent those two and nothing before them.
  await page.locator('[data-title-input]').press('Enter')
  await expect(page.locator('[data-ticket-title]')).toHaveText('My login')
  await description.locator('[data-markdown-save]').click()
  await expect(description.locator('[data-markdown-view]')).toHaveText('Mine')
  await expect(rev).toHaveAttribute('data-ticket-rev', '5')
  expect(await stored(api, `${key}-1`)).toMatchObject({ title: 'My login', description: 'Mine' })
  expect(sent.map((b) => b.set)).toEqual([{ title: 'My login' }, { description: 'Mine' }])

  // The acceptance criteria, saved before the page has read another person's: a read of the ticket that carries their edit (rev 6) is
  // held until the save has gone, so the save goes on rev 5 and the store refuses it stale_revision; the page reads the ticket again,
  // and says theirs the same way.
  let saving = false
  const held: Array<() => void> = []
  await page.route(`**/api/dispatch/tickets/${key}-1`, async (route) => {
    const response = await route.fetch()
    const read = (await response.json()) as { ticket: { rev: number } }
    if (!saving && read.ticket.rev >= 6) await new Promise<void>((resolve) => held.push(resolve))
    await route.fulfill({ response })
  })
  const acceptance = page.locator('[data-ticket-markdown="acceptance"]')
  await acceptance.locator('[data-markdown-empty]').click()
  await acceptance.locator('[data-markdown-input]').fill('Mine to accept')
  await api.ok('POST', `/api/dispatch/tickets/${key}-1/edit`, { opId: opOf(key, 'theirs-a'), byName: 'Priya', set: { acceptance: 'Theirs to accept' } })
  // Their event's read is on its way, held.
  await expect.poll(() => held.length).toBeGreaterThanOrEqual(1)
  await expect(rev).toHaveAttribute('data-ticket-rev', '5')
  saving = true
  await acceptance.locator('[data-markdown-save]').click()
  await expect(acceptance.locator('[data-markdown-theirs-text]')).toHaveText('Theirs to accept')
  await expect(acceptance.locator('[data-markdown-input]')).toHaveValue('Mine to accept')
  await expect(rev).toHaveAttribute('data-ticket-rev', '6')
  expect((await stored(api, `${key}-1`)).acceptance).toBe('Theirs to accept')
  for (const release of held) release()
  await acceptance.locator('[data-markdown-save]').click()
  await expect(acceptance.locator('[data-markdown-view]')).toHaveText('Mine to accept')
  expect((await stored(api, `${key}-1`)).acceptance).toBe('Mine to accept')
  // The refused save and the second one.
  expect(sent.map((b) => b.set).slice(2)).toEqual([{ acceptance: 'Mine to accept' }, { acceptance: 'Mine to accept' }])
})

test('a filter whose first read fails is read again, with no further event and nothing done', async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key, 'Waiting')
  await page.goto(`/dispatch/${key}`)
  const row = page.locator(`[data-ticket-row="${key}-1"]`)
  await expect(row).toBeVisible()
  // The filtered list's read fails, and so does the one retry of it that $fetch makes of a GET by itself, as a server busy for a moment
  // answers; the next goes through.
  let asked = 0
  await page.route(/\/api\/dispatch\/tickets\?.*status=backlog/, async (route) => {
    if (++asked <= 2) return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'unavailable', message: 'The store is busy.' } }) })
    return route.continue()
  })
  await page.locator('[data-filter-status]').click()
  await page.getByRole('option', { name: 'Backlog', exact: true }).click()
  await page.keyboard.press('Escape')
  await expect(page).toHaveURL(/status=backlog/)
  // Asked again after the window, by the page alone.
  await expect.poll(() => asked).toBeGreaterThanOrEqual(3)
  await expect(row).toBeVisible()
  await expect(page.locator('[data-list-total]')).toHaveAttribute('data-count', '1')
})

test("a ticket's page opened before its ticket is made says so, and shows it once it is made, with no reload", async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key)
  // A project key no project has says so too.
  const none = await freeKey(api)
  await page.goto(`/dispatch/${none}`)
  await expect(page.locator('[data-project-missing]')).toBeVisible()

  await page.goto(`/dispatch/${key}/1`)
  await expect(page.locator('[data-ticket-missing]')).toBeVisible()
  await mark(page)
  await api.ok('POST', '/api/dispatch/tickets', { opId: opOf(key, 't1'), project: key, title: 'Made since' })
  await expect(page.locator('[data-ticket-title]')).toHaveText('Made since')
  await expect(page.locator('[data-ticket-missing]')).toHaveCount(0)
  expect(await marked(page)).toBe('kept')
})
