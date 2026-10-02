import { rmSync } from 'node:fs'
import { stopServer } from './server'
import { readState } from './state'

/** Stops the server by its pid and removes the scratch root; CONDUCTOR_E2E_KEEP=1 keeps the root (its server.log) to read. */
export default async function globalTeardown(): Promise<void> {
  const state = readState()
  await stopServer(state.pid)
  if (process.env.CONDUCTOR_E2E_KEEP === '1') {
    console.log(`conductor e2e: kept ${state.root}`)
    return
  }
  rmSync(state.root, { recursive: true, force: true })
}
