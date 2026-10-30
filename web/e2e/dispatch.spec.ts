import type { BrowserContext, Page } from '@playwright/test'
import { expect, test, type Api } from './fixtures'

// Dispatch's pages (docs/plans/dispatch.md, PR-8): a project made from the projects page, a ticket made from the project page's New
// ticket button, the ticket's page editing its title, its description (the preview shown), its priority and its owner, its history
// listing the fields set, the counts; and a second browser that sees the title edited and the projects page's open count move with no
// reload, by the dispatch events. Then what review round 1 found: two assignees ticked while the first one's edit is held both kept; an
// editor's save that never replaces another person's text unseen; a filter whose first read fails read again; a ticket's page opened
// before its ticket is made. And review round 2's: an edit waiting behind one whose reply a later read overtook, made on the revision
// that one committed; each page's first read, failed with the stream connected already, read again; what is typed while an editor's
// save goes, kept. And review round 3's: an edit waiting behind one whose reply carries no ticket, made on the revision its receipt
// names; an owner picked back while the edit of another is on its way, kept; an acceptance made again for the same owner, in the
// history. And review round 4's: a title typed back to the one shown while its save goes, kept and saved after it; a new ticket's and
// a new project's dialog, which stay open while what they make is on its way and are closed by its answer alone. The spec names what
// it made: a project of its own per test (E2E, or E2E1, E2E2… when the server holds the earlier ones), and counts only the rows of
// that project.
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
  return (await api.ok<{ ticket: { title: string; description: string; acceptance: string; priority: number; owner: string; assignees: string[] } }>('GET', `/api/dispatch/tickets/${key}`)).ticket
}

/** An edit of a ticket as the page sent it. */
interface SentEdit {
  expectedRev?: number
  set?: Record<string, unknown>
  add?: { assignees?: string[] }
  remove?: { assignees?: string[] }
}

/** What the page's edits of a ticket sent, in order, by the request's body, the edit route held where `hold` says (from 1). */
async function recordEdits(page: Page, hold?: { n: number; until: Promise<void> }): Promise<SentEdit[]> {
  const sent: SentEdit[] = []
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

test('an edit waiting behind one whose reply a later read overtook is made on the revision that one committed', async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key, 'In turn')
  let release!: () => void
  const sent = await recordEdits(page, { n: 1, until: new Promise<void>((resolve) => (release = resolve)) })
  await page.goto(`/dispatch/${key}/1`)
  const rev = page.locator('[data-ticket-rev]')
  await expect(rev).toHaveAttribute('data-ticket-rev', '1')

  // The priority set: its edit held on its way, before the store has it.
  await page.locator('[data-ticket-priority]').click()
  await page.getByRole('option', { name: 'Urgent' }).click()
  await expect.poll(() => sent.length).toBe(1)
  // A read of the ticket sent after that edit and answered before its commit: another ticket of the project made, whose event reads the
  // page again after the window. Its reply, rev 1, is applied, so the held edit's reply, numbered before it, is dropped when it comes.
  const reread = page.waitForResponse((r) => r.request().method() === 'GET' && new URL(r.url()).pathname === `/api/dispatch/tickets/${key}-1`)
  await api.ok('POST', '/api/dispatch/tickets', { opId: opOf(key, 't2'), project: key, title: 'Another' })
  expect(((await (await reread).json()) as { ticket: { rev: number } }).ticket.rev).toBe(1)
  // The owner set meanwhile: its edit waits its turn behind the priority's.
  const owner = `Owner ${key}`
  await page.locator('[data-ticket-owner]').click()
  await page.getByPlaceholder('A name…', { exact: true }).fill(owner)
  await page.getByRole('option', { name: `Make ${owner} the owner` }).click()
  release()

  // The priority's edit commits rev 2, and the page reads the ticket at it before the owner's edit is made: that one expects rev 2 and
  // is done. Made on the rev 1 shown, it would be refused stale_revision.
  await expect(rev).toHaveAttribute('data-ticket-rev', '3')
  await expect(page.locator('[data-ticket-priority]')).toContainText('Urgent')
  await expect(page.locator('[data-ticket-owner]')).toContainText(owner)
  expect(sent.map((b) => [b.set, b.expectedRev])).toEqual([
    [{ priority: 4 }, 1],
    [{ owner }, 2],
  ])
  expect(await stored(api, `${key}-1`)).toMatchObject({ priority: 4, owner })
})

test("each page's first read, failed with the stream connected already, is read again with no event, no snapshot and no reload", async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key)
  // The projects page, its stream connected: the project's open count follows a ticket made through the API, by the ticket's event.
  await page.goto('/dispatch')
  const project = page.locator(`[data-project-row="${key}"]`)
  await expect(project.locator('[data-project-open]')).toHaveAttribute('data-count', '0')
  await api.ok('POST', '/api/dispatch/tickets', { opId: opOf(key, 't1'), project: key, title: 'Read again' })
  await expect(project.locator('[data-project-open]')).toHaveAttribute('data-count', '1')
  await mark(page)

  /**
   * Fails the first two reads `url` matches as a server busy for a moment answers (a read and the one retry of it that $fetch makes of a
   * GET by itself), lets the next go through, and counts them.
   */
  async function failTwice(url: RegExp): Promise<{ asked: number }> {
    const r = { asked: 0 }
    await page.route(url, async (route) => {
      if (route.request().method() !== 'GET') return route.fallback()
      if (++r.asked <= 2) return route.fulfill({ status: 503, contentType: 'application/json', body: JSON.stringify({ error: { code: 'unavailable', message: 'The store is busy.' } }) })
      return route.continue()
    })
    return r
  }

  // The project's page, opened from its row: its list's first read fails, and is asked again after the window by the page alone.
  const list = await failTwice(new RegExp(`/api/dispatch/tickets\\?project=${key}&`))
  await project.click()
  await expect(page).toHaveURL(new RegExp(`/dispatch/${key}$`))
  await expect.poll(() => list.asked).toBeGreaterThanOrEqual(3)
  const row = page.locator(`[data-ticket-row="${key}-1"]`)
  await expect(row.locator('[data-row-title]')).toHaveText('Read again')

  // The ticket's page, opened from its row: the same.
  const ticket = await failTwice(new RegExp(`/api/dispatch/tickets/${key}-1$`))
  await row.click()
  await expect(page).toHaveURL(new RegExp(`/dispatch/${key}/1$`))
  await expect.poll(() => ticket.asked).toBeGreaterThanOrEqual(3)
  await expect(page.locator('[data-ticket-title]')).toHaveText('Read again')

  // The projects page again, from the ticket page's Dispatch link: the same.
  const projects = await failTwice(/\/api\/dispatch\/projects(\?[^/]*)?$/)
  await page.locator('[data-ticket-header]').getByRole('link', { name: 'Dispatch', exact: true }).click()
  await expect(page).toHaveURL(/\/dispatch$/)
  await expect.poll(() => projects.asked).toBeGreaterThanOrEqual(3)
  await expect(project.locator('[data-project-open]')).toHaveAttribute('data-count', '1')
  // All in the one load of the page the test began with.
  expect(await marked(page)).toBe('kept')
})

test("what is typed while an editor's save goes is kept: the save is done, the newer text stays in the editor, and its own save follows", async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key, 'Typed on')
  let release!: () => void
  const sent = await recordEdits(page, { n: 1, until: new Promise<void>((resolve) => (release = resolve)) })
  await page.goto(`/dispatch/${key}/1`)
  const rev = page.locator('[data-ticket-rev]')
  await expect(rev).toHaveAttribute('data-ticket-rev', '1')

  // The description saved, its edit held on its way; more is written meanwhile.
  const description = page.locator('[data-ticket-markdown="description"]')
  await description.locator('[data-markdown-empty]').click()
  const input = description.locator('[data-markdown-input]')
  const save = description.locator('[data-markdown-save]')
  await input.fill('First version')
  await save.click()
  await expect.poll(() => sent.length).toBe(1)
  await input.fill('First version, and more')
  release()
  // The save is done (Save is offered again): the store has what was saved, and the editor what was written after it.
  await expect(save).toBeEnabled()
  await expect(input).toHaveValue('First version, and more')
  await expect(rev).toHaveAttribute('data-ticket-rev', '2')
  expect((await stored(api, `${key}-1`)).description).toBe('First version')
  // Its own save is made against the text saved, with no notice of another person's, and closes the editor.
  await save.click()
  await expect(description.locator('[data-markdown-view]')).toHaveText('First version, and more')
  expect((await stored(api, `${key}-1`)).description).toBe('First version, and more')
  expect(sent.map((b) => b.set)).toEqual([{ description: 'First version' }, { description: 'First version, and more' }])

  // The title, the same way: Enter saves it, held, and more is typed.
  await page.unroute('**/api/dispatch/tickets/*/edit')
  let releaseTitle!: () => void
  const titled = await recordEdits(page, { n: 1, until: new Promise<void>((resolve) => (releaseTitle = resolve)) })
  await page.locator('[data-title-edit]').click()
  const title = page.locator('[data-title-input]')
  await title.fill('Typed on, first')
  await title.press('Enter')
  await expect.poll(() => titled.length).toBe(1)
  await title.fill('Typed on, first and more')
  releaseTitle()
  await expect(page.locator('[data-title-save]')).toBeEnabled()
  await expect(title).toHaveValue('Typed on, first and more')
  await expect(rev).toHaveAttribute('data-ticket-rev', '4')
  expect((await stored(api, `${key}-1`)).title).toBe('Typed on, first')
  await expect(page.locator('[data-title-theirs]')).toHaveCount(0)
  await title.press('Enter')
  await expect(page.locator('[data-ticket-title]')).toHaveText('Typed on, first and more')
  expect((await stored(api, `${key}-1`)).title).toBe('Typed on, first and more')
  expect(titled.map((b) => b.set)).toEqual([{ title: 'Typed on, first' }, { title: 'Typed on, first and more' }])
})

test("an edit waiting behind one whose reply carries no ticket is made on the revision that reply's receipt names", async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key, 'No ticket')
  const ticketRead = new RegExp(`/api/dispatch/tickets/${key}-1$`)
  // The page's reads of its ticket from the first edit's commit on, held until two are asked or the second edit is sent. The commit's
  // event reads the page again at once: answered then, it could show the revision committed before the second edit is made, which would
  // expect it whether the page read it after the reply or not. With the page's own read after the reply, two are asked.
  let holding = false
  let reads = 0
  let letReadsGo!: () => void
  const readsGo = new Promise<void>((resolve) => (letReadsGo = resolve))
  await page.route(ticketRead, async (route) => {
    if (route.request().method() !== 'GET' || !holding) return route.fallback()
    if (++reads >= 2) letReadsGo()
    await readsGo
    await route.fallback()
  })
  // The page's edits, in order: the first held on its way, then answered as the server answers when its read after the commit fails,
  // its receipt done and no ticket.
  const sent: SentEdit[] = []
  let release!: () => void
  const held = new Promise<void>((resolve) => (release = resolve))
  let receiptRev: number | undefined
  await page.route('**/api/dispatch/tickets/*/edit', async (route) => {
    sent.push(route.request().postDataJSON())
    if (sent.length > 1) {
      letReadsGo()
      return route.continue()
    }
    await held
    holding = true
    const response = await route.fetch()
    const reply = (await response.json()) as { receipt: { rev?: number }; ticket: unknown }
    receiptRev = reply.receipt.rev
    await route.fulfill({ response, json: { ...reply, ticket: null } })
  })
  await page.goto(`/dispatch/${key}/1`)
  const rev = page.locator('[data-ticket-rev]')
  await expect(rev).toHaveAttribute('data-ticket-rev', '1')
  // The page's stream is connected and its snapshot taken: the ticket's description set through the API, whose event reads the page
  // again at once.
  await api.ok('POST', `/api/dispatch/tickets/${key}-1/edit`, { opId: opOf(key, 'd'), set: { description: 'Set elsewhere' } })
  await expect(rev).toHaveAttribute('data-ticket-rev', '2')

  // The priority set: its edit held on its way. The owner set meanwhile: its edit waits its turn behind the priority's.
  await page.locator('[data-ticket-priority]').click()
  await page.getByRole('option', { name: 'Urgent' }).click()
  await expect.poll(() => sent.length).toBe(1)
  const owner = `Owner ${key}`
  await page.locator('[data-ticket-owner]').click()
  await page.getByPlaceholder('A name…', { exact: true }).fill(owner)
  await page.getByRole('option', { name: `Make ${owner} the owner` }).click()
  release()

  // The priority's edit commits rev 3, which its reply says in its receipt alone: the page reads the ticket at it before the owner's edit
  // is made, which expects rev 3 and is done. Made on the rev 2 shown, it would be refused stale_revision.
  await expect(rev).toHaveAttribute('data-ticket-rev', '4')
  expect(receiptRev).toBe(3)
  await expect(page.locator('[data-ticket-priority]')).toContainText('Urgent')
  await expect(page.locator('[data-ticket-owner]')).toContainText(owner)
  expect(sent.map((b) => [b.set, b.expectedRev])).toEqual([
    [{ priority: 4 }, 2],
    [{ owner }, 3],
  ])
  expect(await stored(api, `${key}-1`)).toMatchObject({ priority: 4, owner })
})

test('an owner picked back while the edit setting another is on its way is the owner the ticket keeps', async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key, 'Owned')
  const first = `Alice ${key}`
  const second = `Bob ${key}`
  await api.ok('POST', `/api/dispatch/tickets/${key}-1/edit`, { opId: opOf(key, 'o1'), set: { owner: first } })
  await api.ok('POST', '/api/dispatch/people', { opId: opOf(key, 'b'), name: second })
  let release!: () => void
  const sent = await recordEdits(page, { n: 1, until: new Promise<void>((resolve) => (release = resolve)) })
  await page.goto(`/dispatch/${key}/1`)
  const rev = page.locator('[data-ticket-rev]')
  await expect(rev).toHaveAttribute('data-ticket-rev', '2')
  const owner = page.locator('[data-ticket-owner]')
  await expect(owner).toContainText(first)

  // Bob picked: the edit held on its way, and the picker still shows Alice, the owner the store last answered. Alice picked back.
  await owner.click()
  await page.getByRole('option', { name: second, exact: true }).click()
  await expect.poll(() => sent.length).toBe(1)
  await expect(owner).toContainText(first)
  await owner.click()
  await page.getByRole('option', { name: first, exact: true }).click()
  release()

  // Bob's edit is done, then Alice's, made on the revision Bob's left: the ticket keeps the owner picked last.
  await expect(rev).toHaveAttribute('data-ticket-rev', '4')
  await expect(owner).toContainText(first)
  expect(sent.map((b) => [b.set, b.expectedRev])).toEqual([
    [{ owner: second }, 2],
    [{ owner: first }, 3],
  ])
  expect((await stored(api, `${key}-1`)).owner).toBe(first)
})

test('an acceptance made again for the same owner is in the history, each acceptance said once', async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key, 'Accepted twice')
  const owner = `Owner ${key}`
  const ticket = `${key}-1`
  await api.ok('POST', `/api/dispatch/tickets/${ticket}/edit`, { opId: opOf(key, 'e1'), set: { owner, acceptance: 'It works' } })
  await api.ok('POST', `/api/dispatch/tickets/${ticket}/accept`, { opId: opOf(key, 'a1') })
  await api.ok('POST', `/api/dispatch/tickets/${ticket}/edit`, { opId: opOf(key, 'e2'), set: { acceptance: 'It works, and fast' } })
  // Accepted again for the same owner, by the same person: the store records its time and the criteria it was made against, no owner.
  await api.ok('POST', `/api/dispatch/tickets/${ticket}/accept`, { opId: opOf(key, 'a2') })
  await page.goto(`/dispatch/${key}/1`)
  const lines = page.locator('[data-ticket-history] [data-history-line]')
  await expect.poll(async () => (await lines.allTextContents()).filter((l) => l.includes('accept'))).toEqual([
    'wrote the acceptance criteria',
    `accepted it for ${owner}`,
    'edited the acceptance criteria',
    'accepted it again',
  ])
})

test('a title typed back to the one shown while its save goes stays in the editor, and its own Enter saves it after that save', async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key, 'Original')
  let release!: () => void
  const sent = await recordEdits(page, { n: 1, until: new Promise<void>((resolve) => (release = resolve)) })
  await page.goto(`/dispatch/${key}/1`)
  const rev = page.locator('[data-ticket-rev]')
  await expect(rev).toHaveAttribute('data-ticket-rev', '1')

  // A title saved, its edit held on its way; the title shown typed back, and Enter pressed again while the save goes.
  await page.locator('[data-title-edit]').click()
  const title = page.locator('[data-title-input]')
  const save = page.locator('[data-title-save]')
  await title.fill('First save')
  await title.press('Enter')
  await expect.poll(() => sent.length).toBe(1)
  await expect(save).toBeDisabled()
  await title.fill('Original')
  await title.press('Enter')
  // Nothing is made of it while the save goes: the editor stays, with the title typed back.
  await expect(title).toHaveValue('Original')
  release()

  // The save is done: the store has the title saved, and the editor the one typed back, with no notice of another person's.
  await expect(rev).toHaveAttribute('data-ticket-rev', '2')
  await expect(save).toBeEnabled()
  await expect(title).toHaveValue('Original')
  await expect(page.locator('[data-title-theirs]')).toHaveCount(0)
  expect((await stored(api, `${key}-1`)).title).toBe('First save')
  // Its own Enter saves it, made on the revision that save left, and closes the editor.
  await title.press('Enter')
  await expect(page.locator('[data-ticket-title]')).toHaveText('Original')
  await expect(rev).toHaveAttribute('data-ticket-rev', '3')
  expect((await stored(api, `${key}-1`)).title).toBe('Original')
  expect(sent.map((b) => [b.set, b.expectedRev])).toEqual([
    [{ title: 'First save' }, 1],
    [{ title: 'Original' }, 2],
  ])
})

/** Holds the first creation `url` is sent (a POST) until `until` is done, and counts the creations sent. */
async function holdCreation(page: Page, url: string, until: Promise<void>): Promise<{ sent: number }> {
  const r = { sent: 0 }
  await page.route(url, async (route) => {
    if (route.request().method() !== 'POST') return route.fallback()
    if (++r.sent === 1) await until
    await route.continue()
  })
  return r
}

test("a new ticket's dialog stays open while its ticket is made, and only that ticket's answer closes it", async ({ page, api }) => {
  const key = await freeKey(api)
  await made(api, key)
  let release!: () => void
  const creations = await holdCreation(page, '**/api/dispatch/tickets', new Promise<void>((resolve) => (release = resolve)))
  await page.goto(`/dispatch/${key}`)
  await expect(page.locator('[data-list-empty]')).toBeVisible()

  // New ticket, made: its creation held on its way.
  await page.locator('[data-new-ticket-open]').click()
  const dialog = page.getByRole('dialog')
  const title = page.locator('[data-new-ticket-title]')
  await title.fill('Made while held')
  await page.locator('[data-new-ticket-submit]').click()
  await expect.poll(() => creations.sent).toBe(1)
  // Nothing closes it while the ticket is made: Escape and a click outside do nothing, its close button and Cancel are off, and it
  // keeps what was sent.
  await page.keyboard.press('Escape')
  await page.mouse.click(5, 5)
  await expect(dialog.getByRole('button', { name: 'Close', exact: true })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeDisabled()
  await expect(title).toHaveValue('Made while held')
  release()
  // Answered: the dialog it was made from closes, and the list shows the ticket.
  await expect(dialog).toHaveCount(0)
  await expect(page.locator(`[data-ticket-row="${key}-1"] [data-row-title]`)).toHaveText('Made while held')

  // Opened again, with nothing on its way: an empty draft, which Cancel closes; and the next opening's own ticket, made.
  await page.locator('[data-new-ticket-open]').click()
  await expect(title).toHaveValue('')
  await title.fill('Not made')
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await page.locator('[data-new-ticket-open]').click()
  await expect(title).toHaveValue('')
  await title.fill('The next one')
  await page.locator('[data-new-ticket-submit]').click()
  await expect(dialog).toHaveCount(0)
  await expect(page.locator(`[data-ticket-row="${key}-2"] [data-row-title]`)).toHaveText('The next one')
  await expect(page.locator('[data-list-total]')).toHaveAttribute('data-count', '2')
  expect(creations.sent).toBe(2)
})

test("a new project's dialog stays open while its project is made, and that project's answer opens its page", async ({ page, api }) => {
  const key = await freeKey(api)
  const name = `E2E project ${key}`
  let release!: () => void
  const creations = await holdCreation(page, '**/api/dispatch/projects', new Promise<void>((resolve) => (release = resolve)))
  await page.goto('/dispatch')

  // New project, made: its creation held on its way.
  await page.locator('[data-new-project-open]').click()
  const dialog = page.getByRole('dialog')
  await page.locator('[data-new-project-key]').fill(key)
  await page.locator('[data-new-project-name]').fill(name)
  await page.locator('[data-new-project-submit]').click()
  await expect.poll(() => creations.sent).toBe(1)
  // Nothing closes it while the project is made, and the page stays: Escape and a click outside do nothing, its close button and
  // Cancel are off, and it keeps what was sent.
  await page.keyboard.press('Escape')
  await page.mouse.click(5, 5)
  await expect(dialog.getByRole('button', { name: 'Close', exact: true })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeDisabled()
  await expect(page.locator('[data-new-project-name]')).toHaveValue(name)
  await expect(page).toHaveURL(/\/dispatch$/)
  release()
  // Answered: the project's page opens, from the dialog it was made from.
  await expect(page).toHaveURL(new RegExp(`/dispatch/${key}$`))
  await expect(page.locator('[data-project-name]')).toHaveText(name)
  await expect(dialog).toHaveCount(0)
  expect(creations.sent).toBe(1)
})
