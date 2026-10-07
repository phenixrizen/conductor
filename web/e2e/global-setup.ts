import { randomBytes } from 'node:crypto'
import { mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { renderConfig, scratchRepo, startServer, stopServer } from './server'
import { STATE_ENV, writeState, type E2EState } from './state'

const here = dirname(fileURLToPath(import.meta.url))

/**
 * Starts the server the specs drive: bin/conductor with web/e2e/conductor.e2e.json (claude and codex are the stub agent), a HOME,
 * a data directory and a scratch git repository of its own under one temporary root, which is its only allowed root. The state goes
 * to a file the specs and the teardown read (CONDUCTOR_E2E_STATE). On any failure what was started is stopped and the root removed.
 */
export default async function globalSetup(): Promise<void> {
  const root = mkdtempSync(join(tmpdir(), 'conductor-e2e-'))
  let pid: number | undefined
  try {
    const home = join(root, 'home')
    const data = join(root, 'data')
    const repo = join(root, 'repo')
    mkdirSync(home)
    mkdirSync(repo)
    scratchRepo(repo)
    const config = join(root, 'conductor.json')
    renderConfig(join(here, 'conductor.e2e.json'), config)
    const log = join(root, 'server.log')
    const hostToken = 'e2e-host-' + randomBytes(12).toString('hex')
    const started = await startServer({ config, home, data, allowedRoot: root, defaultCwd: repo, log, hostToken })
    pid = started.pid
    const state: E2EState = { ...started, hostToken, root, home, data, repo, log }
    const file = join(root, 'state.json')
    writeState(file, state)
    process.env[STATE_ENV] = file
    console.log(`conductor e2e: server ${state.baseURL} (pid ${pid}), scratch ${root}`)
  } catch (e) {
    if (pid !== undefined) await stopServer(pid)
    rmSync(root, { recursive: true, force: true })
    throw e
  }
}
