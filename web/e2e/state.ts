import { readFileSync, writeFileSync } from 'node:fs'

/** Where the global setup writes the state of the server it started, for the specs and the teardown. */
export const STATE_ENV = 'CONDUCTOR_E2E_STATE'

/** The test server the global setup started, and its scratch directories. */
export interface E2EState {
  /** `http://127.0.0.1:<port>`, also the server's public URL. */
  baseURL: string
  /** The workbench token, a fresh one for each run of the suite. */
  token: string
  /** A host token the server accepts, for a `conductor host` a spec runs against it. */
  hostToken: string
  port: number
  /** The server's process, killed by pid at teardown. */
  pid: number
  /** The scratch root: the server's HOME, data directory, log and the repository live under it; it is the allowed root. */
  root: string
  home: string
  data: string
  /** The scratch git repository (one commit): the server's default working directory, so the example crews work in it. */
  repo: string
  /** The server's log. */
  log: string
}

export function readState(file = process.env[STATE_ENV]): E2EState {
  if (!file) throw new Error(`${STATE_ENV} is not set: run the suite with playwright test, whose global setup starts the server`)
  return JSON.parse(readFileSync(file, 'utf8')) as E2EState
}

export function writeState(file: string, s: E2EState): void {
  writeFileSync(file, JSON.stringify(s, null, 2) + '\n', { mode: 0o600 })
}
