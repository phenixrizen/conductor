import { expect, test } from './fixtures'

// The identity probe: each stub answers --version as its agent does, and the
// Agents page says so; the goose stub answers as the Go migrations tool, an
// impostor the page names and a crew launch refuses.
test.describe.configure({ mode: 'serial' })

test('the Agents page names each agent by its version output and the impostor by what it printed', async ({ page }) => {
  await page.goto('/agents')
  const claude = page.locator('[data-agent="claude"] [data-identity]')
  await expect(claude).toHaveAttribute('data-identity', 'ok', { timeout: 30_000 })
  await expect(claude).toContainText('Claude Code 2.1.287')
  const codex = page.locator('[data-agent="codex"] [data-identity]')
  await expect(codex).toHaveAttribute('data-identity', 'ok', { timeout: 30_000 })
  await expect(codex).toContainText('Codex CLI 0.159.0')
  const goose = page.locator('[data-agent="goose"] [data-identity]')
  await expect(goose).toHaveAttribute('data-identity', 'impostor', { timeout: 30_000 })
  await expect(goose).toContainText('Not Goose')
  await expect(goose).toHaveAttribute('title', /goose version: v3\.22\.1/)
})

test('the catalog reports the identity, and the check runs the probe for an adapter', async ({ api, state }) => {
  const { agents } = await api.ok<{ agents: Array<{ id: string; command: string[]; identity?: { ran: boolean; identified: boolean; impostor?: boolean; verified: boolean; name?: string; version?: string; output?: string } }> }>('GET', '/api/catalog')
  const byId = Object.fromEntries(agents.map((a) => [a.id, a]))
  expect(byId.claude?.identity).toMatchObject({ ran: true, identified: true, verified: true, name: 'Claude Code', version: '2.1.287' })
  expect(byId.codex?.identity).toMatchObject({ ran: true, identified: true, verified: true, name: 'Codex CLI', version: '0.159.0' })
  expect(byId.goose?.identity).toMatchObject({ ran: true, identified: false, impostor: true, name: 'Goose', output: 'goose version: v3.22.1' })
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
