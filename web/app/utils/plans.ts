// The agent's plans outside the working directory (#289): Claude Code writes
// a plan under its config directory (~/.claude/plans/<slug>.md), which the
// session lists with the file request `op: plans` once the agent reported
// it, and reads and saves as the folder's files.
import type { FileHeader } from '~/utils/protocol'

export interface PlanRow {
  /** The plan's whole path, as a file request names it. */
  abs: string
  /** Its path from the plans directory (its file name, as Claude Code names plans). */
  name: string
  size: number
}

/** The rows of a `plans` reply: each plan by its whole path, in the reply's order; none from any other reply. */
export function planRows(header: FileHeader | null | undefined): PlanRow[] {
  if (!header || header.kind !== 'plans' || !header.entries?.length) return []
  const dir = header.path.replace(/[\\/]+$/, '')
  const sep = dir.includes('\\') && !dir.includes('/') ? '\\' : '/'
  return header.entries.filter((e) => !e.dir && e.name).map((e) => ({ abs: `${dir}${sep}${e.name.replace(/\//g, sep)}`, name: e.name, size: e.size }))
}
