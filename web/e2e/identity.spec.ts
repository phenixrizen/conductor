import { expect, test } from './fixtures'

// The identity probe: each stub answers --version as its agent does, and the
// Agents page says so; the goose stub answers as the Go migrations tool, an
// impostor the server never offers as Goose (not installed, out of the
// Launch dialog) and every launch refuses.
test.describe.configure({ mode: 'serial' })

test('the Agents page names each agent by its version output, and the impostor is not installed, by what it printed', async ({ page }) => {
  await page.goto('/agents')
  const claude = page.locator('[data-agent="claude"] [data-identity]')
  await expect(claude).toHaveAttribute('data-identity', 'ok', { timeout: 30_000 })
  await expect(claude).toContainText('Claude Code 2.1.287')
  const codex = page.locator('[data-agent="codex"] [data-identity]')
  await expect(codex).toHaveAttribute('data-identity', 'ok', { timeout: 30_000 })
  await expect(codex).toContainText('Codex CLI 0.159.0')
  const goose = page.locator('[data-agent="goose"] [data-not-installed][data-not-agent]')
  await expect(goose).toBeVisible({ timeout: 30_000 })
  await expect(goose).toContainText('Not installed')
  await expect(goose).toHaveAttribute('title', /is another program, not Goose: it printed "goose version: v3\.22\.1"/)
  await expect(page.locator('[data-agent="goose"]')).toHaveAttribute('data-available', 'false')
  await expect(page.locator('[data-agent="goose"] [data-identity]')).toHaveCount(0)
})

test('the Launch dialog does not offer the impostor, and a plain launch of it is refused', async ({ page, api }) => {
  // The probe has answered (the catalog lists the impostor as not available).
  await expect
    .poll(async () => {
      const { agents } = await api.ok<{ agents: Array<{ id: string; available: boolean }> }>('GET', '/api/catalog')
      return agents.find((a) => a.id === 'goose')?.available
    }, { timeout: 30_000 })
    .toBe(false)
  await page.goto('/')
  await page.getByRole('button', { name: 'Launch agent' }).first().click()
  const agents = page.getByRole('dialog').getByRole('radiogroup', { name: 'Agent' })
  await expect(agents.getByRole('radio', { name: 'Claude Code (e2e stub)' })).toBeVisible()
  await expect(agents.getByRole('radio', { name: 'Goose (e2e stub)' })).toHaveCount(0)
  const r = await api.call<{ error: { code: string; message: string } }>('POST', '/api/sessions', { agentId: 'goose' })
  expect(r.status).toBe(400)
  expect(r.body.error.code).toBe('not_the_agent')
  expect(r.body.error.message).toContain('goose version: v3.22.1')
})

test('the catalog reports the identity, and the check runs the probe for an adapter', async ({ api, state }) => {
  const { agents } = await api.ok<{ agents: Array<{ id: string; command: string[]; available: boolean; identity?: { ran: boolean; identified: boolean; impostor?: boolean; verified: boolean; name?: string; version?: string; output?: string } }> }>('GET', '/api/catalog')
  const byId = Object.fromEntries(agents.map((a) => [a.id, a]))
  expect(byId.claude?.identity).toMatchObject({ ran: true, identified: true, verified: true, name: 'Claude Code', version: '2.1.287' })
  expect(byId.codex?.identity).toMatchObject({ ran: true, identified: true, verified: true, name: 'Codex CLI', version: '0.159.0' })
  expect(byId.goose?.identity).toMatchObject({ ran: true, identified: false, impostor: true, name: 'Goose', output: 'goose version: v3.22.1' })
  expect(byId.goose?.available).toBe(false)
  expect(byId.claude?.available).toBe(true)
  const stub = byId.claude!.command[1]!
  const check = await api.ok<{ found: boolean; identity?: { identified: boolean; output?: string } }>('POST', '/api/catalog/check', { command: ['/bin/bash', stub], adapter: 'claude', env: { STUB_IDENTITY: 'codex' } })
  expect(check.found).toBe(true)
  expect(check.identity).toMatchObject({ identified: false, output: 'codex-cli 0.159.0' })
  void state
})

test('a crew with the impostor is refused at launch, and the message names what the program printed', async ({ api }) => {
  const crew = await api.ok<{ crew: { id: string } }>('POST', '/api/crews', {
    name: 'e2e identity',
    goal: 'nothing',
    cwd: '',
    where: 'server',
    isolation: 'none',
    openAfterLaunch: false,
    members: [{ name: 'solo', agentId: 'goose', prompt: 'hello', start: { when: 'immediately' } }],
  })
  try {
    const r = await api.call<{ error: { code: string; message: string } }>('POST', `/api/crews/${encodeURIComponent(crew.crew.id)}/launch`)
    expect(r.status).toBe(400)
    expect(r.body.error.code).toBe('invalid_crew')
    expect(r.body.error.message).toContain('is not Goose')
    expect(r.body.error.message).toContain('goose version: v3.22.1')
  } finally {
    await api.call('DELETE', `/api/crews/${encodeURIComponent(crew.crew.id)}`)
  }
})
