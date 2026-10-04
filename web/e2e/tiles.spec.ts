import { expect, logged, member, test, type Run } from './fixtures'

// The interactive tiles: on the run page and the wall every tile is a live
// terminal that fills its pane (no blank strip beside the grid of cells), is
// drawn with the renderer this browser has (data-renderer), takes typing in
// place, and hands the Alt chords to the page. By-hand item 1 of round 4.
test.describe.configure({ mode: 'serial' })

let runId = ''
let leadSession = ''

test.beforeAll(async ({ api }) => {
  const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
    name: 'e2e tiles',
    goal: 'tiles',
    cwd: '',
    where: 'server',
    isolation: 'none',
    openAfterLaunch: false,
    members: [
      { name: 'lead', agentId: 'claude', prompt: 'say hello', start: { when: 'immediately' } },
      { name: 'second', agentId: 'codex', prompt: 'say hi', start: { when: 'immediately' } },
    ],
  })
  const run = await api.launchCrew(crew.crew.id)
  runId = run.id
  await expect.poll(async () => logged(await api.run(runId), "typed lead's prompt"), { timeout: 60_000 }).toBe(true)
  leadSession = member(await api.run(runId), 'lead').sessionId ?? ''
})

test.afterAll(async ({ api }) => {
  if (runId) await api.stopRun(runId)
})

/**
 * How each tile terminal fills its pane: the xterm element's content width (its padding off) against the width of the grid of cells
 * (`.xterm-screen`), and the width of one cell. A filled tile leaves less than a cell beside its grid.
 */
async function fills(page: import('@playwright/test').Page, cols: number) {
  return page.locator('[data-session-tile] .terminal-host').evaluateAll(
    (hosts, cols) =>
      hosts.map((host) => {
        const xterm = host.querySelector('.xterm') as HTMLElement | null
        const screen = host.querySelector('.xterm-screen') as HTMLElement | null
        const cs = xterm ? getComputedStyle(xterm) : null
        const hostW = host.clientWidth
        const xtermW = xterm && cs ? xterm.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight) : 0
        const screenW = screen?.getBoundingClientRect().width ?? 0
        return { hostW, xtermW, screenW, gap: xtermW - screenW, cell: screenW / cols, cols, renderer: host.getAttribute('data-renderer') }
      }),
    cols,
  )
}

test('every tile on the run page is a live terminal that fills its pane', async ({ page, api }) => {
  const webgl2 = await (async () => {
    await page.goto('/agents')
    return page.evaluate(() => !!document.createElement('canvas').getContext('webgl2'))
  })()
  await page.goto(`/runs/${encodeURIComponent(runId)}`)
  const tiles = page.locator('[data-run-grid] [data-session-tile]')
  await expect(tiles).toHaveCount(2, { timeout: 30_000 })
  await expect(page.locator('[data-run-grid] .terminal-host[data-renderer]')).toHaveCount(2, { timeout: 30_000 })
  for (const r of await page.locator('[data-run-grid] .terminal-host').evaluateAll((els) => els.map((e) => e.getAttribute('data-renderer')))) {
    expect(r, 'the renderer this browser has').toBe(webgl2 ? 'webgl' : 'dom')
  }
  const run: Run = await api.run(runId)
  const lead = await api.session(member(run, 'lead').sessionId ?? '')
  const cols = (lead as unknown as { cols?: number }).cols ?? 80
  let measured = await fills(page, cols)
  await expect
    .poll(async () => {
      measured = await fills(page, cols)
      return measured.every((f) => f.screenW > 0 && f.gap >= 0 && f.gap < f.cell + 1)
    }, { timeout: 15_000, message: `tiles fill their panes: ${JSON.stringify(measured)}` })
    .toBe(true)
  expect(measured.length).toBe(2)
  for (const f of measured) expect(f.hostW).toBeGreaterThan(200)
})

test('the wall tiles fill too, and typing into a focused tile reaches the agent without leaving the wall', async ({ page, api }) => {
  await page.goto('/wall')
  const tile = page.locator('[data-session-tile]').filter({ has: page.getByText('lead', { exact: true }) })
  await expect(tile).toHaveCount(1, { timeout: 30_000 })
  const lead = await api.session(leadSession)
  const cols = (lead as unknown as { cols?: number }).cols ?? 80
  let measured = await fills(page, cols)
  await expect
    .poll(async () => {
      measured = await fills(page, cols)
      return measured.every((f) => f.screenW > 0 && f.gap < f.cell + 1)
    }, { timeout: 15_000, message: `wall tiles fill: ${JSON.stringify(measured)}` })
    .toBe(true)
  await tile.locator('.xterm-screen').click()
  await page.keyboard.type('jk')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/wall$/)
  await expect.poll(async () => (await api.session(leadSession)).attention?.message ?? '', { timeout: 30_000 }).toContain('got: jk')
})

test('the Alt chords leave a focused terminal for the page: Alt+A to Agents, Alt+Esc back to the grid, Alt+H the shortcuts', async ({ page, api }) => {
  await page.goto('/wall')
  const tile = page.locator('[data-session-tile]').filter({ has: page.getByText('lead', { exact: true }) })
  await expect(tile).toHaveCount(1, { timeout: 30_000 })
  await tile.locator('.xterm-screen').click()
  await page.keyboard.press('Alt+A')
  await expect(page).toHaveURL(/\/agents$/)
  // Focus mode on the wall, then Alt+Esc from inside the terminal goes back to the grid.
  await page.goto(`/wall?focus=${encodeURIComponent(leadSession)}`)
  const full = page.locator('.terminal-host').first()
  await expect(full).toBeVisible()
  await full.locator('.xterm-screen').click()
  await page.keyboard.press('Alt+Escape')
  await expect(page).toHaveURL(/\/wall$/)
  await expect(tile).toHaveCount(1)
  // Alt+H opens the shortcuts modal over the terminal; Escape closes it; the next keys are the agent's again.
  await tile.locator('.xterm-screen').click()
  await page.keyboard.press('Alt+H')
  const modal = page.getByRole('dialog')
  await expect(modal).toBeVisible()
  await expect(modal).toContainText('Keyboard shortcuts')
  // The terminal keeps the focus while the modal opens; its own close button closes it.
  await modal.getByRole('button', { name: /close/i }).first().click()
  await expect(modal).toBeHidden()
  await tile.locator('.xterm-screen').click()
  await page.keyboard.type('w')
  await page.keyboard.press('Enter')
  await expect.poll(async () => (await api.session(leadSession)).attention?.message ?? '', { timeout: 30_000 }).toContain('got: w')
})
