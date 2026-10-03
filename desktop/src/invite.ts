/**
 * An invite, `conductor://<host>/join/<token>` (web/app/utils/invite.ts has
 * the same rules): the app opens its own workbench's join page for it,
 * which signals to the server named. https is implied; `?http=1` names a
 * plain-http server, allowed for a loopback host alone.
 */
const LOOPBACK = /^(127\.(\d{1,3}\.){2}\d{1,3}|localhost|\[::1\])(:\d{1,5})?$/i
const TOKEN = /^[A-Za-z0-9_-]{16,256}$/
const HOST = /^(\[[0-9a-f:]+\]|[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*)(:\d{1,5})?$/i

export interface Invite {
  server: string
  token: string
}

export function parseInvite(text: string): Invite | null {
  const m = /^conductor:\/\/([^/?#]+)((?:\/[^/?#]+)*)\/join\/([^/?#]+)(?:\?([^#]*))?$/i.exec(text.trim())
  if (!m) return null
  const host = m[1]!
  const path = m[2] ?? ''
  let token: string
  try {
    token = decodeURIComponent(m[3]!)
  } catch {
    return null
  }
  if (!HOST.test(host) || !TOKEN.test(token)) return null
  const http = new URLSearchParams(m[4] ?? '').get('http') === '1'
  if (http && !LOOPBACK.test(host)) return null
  return { server: `${http ? 'http' : 'https'}://${host}${path}`, token }
}

/** The workbench path the app opens for an invite: its own join page, told which server to signal to. */
export function invitePath(inv: Invite): string {
  return `/join/${encodeURIComponent(inv.token)}?server=${encodeURIComponent(inv.server)}`
}

/** inviteInArgv is the invite among a process's arguments (Windows and Linux hand the URL that way), or null. */
export function inviteInArgv(argv: readonly string[]): Invite | null {
  for (const a of argv) {
    const inv = parseInvite(a)
    if (inv) return inv
  }
  return null
}
