import type { ActivityEntry } from '~/utils/protocol'

/** What the workbench offers when a session formed a crew around itself (POST /api/sessions/{id}/crew with open): the toast and where Open goes. */
export interface FormedCrew {
  title: string
  /** The run's path on this workbench, from the entry's URL. */
  path: string
}

const FORMED = /^formed crew (.+) \(open\)$/

/**
 * The toast for a `link` entry that says "formed crew <name> (open)": the
 * agent asked for the workbench to offer the run (conductor crew create
 * --open). The URL is the server's; only its path is navigated to, so a
 * workbench served under another host still opens its own run page. Any
 * other entry, or one whose URL is not a run, gives nothing.
 */
export function formedCrew(sessionName: string, entry: ActivityEntry): FormedCrew | null {
  if (entry.type !== 'link' || !entry.message || !entry.url) return null
  const m = FORMED.exec(entry.message)
  if (!m) return null
  let path: string
  try {
    path = new URL(entry.url).pathname
  } catch {
    return null
  }
  if (!/^\/runs\/[A-Za-z0-9][A-Za-z0-9._-]*$/.test(path)) return null
  return { title: `${sessionName} formed crew ${m[1]}`, path }
}
