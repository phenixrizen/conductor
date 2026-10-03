/**
 * Invites and the server a join page signals to. An invite is a share link
 * as the desktop app opens it, `conductor://<host>/join/<token>`: the app
 * renders the join page from its own bundle and talks to the server named,
 * a switchyard or any Conductor, for the join route and the session's
 * WebSocket alone. https is implied; `?http=1` names a plain-http server,
 * which only a loopback host may be (a development server).
 */

const LOOPBACK = /^(127\.(\d{1,3}\.){2}\d{1,3}|localhost|\[::1\])(:\d{1,5})?$/i
const TOKEN = /^[A-Za-z0-9_-]{16,256}$/
const HOST = /^(\[[0-9a-f:]+\]|[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*)(:\d{1,5})?$/i

export interface Invite {
  /** `https://host[:port]`, or `http://` for a loopback host. */
  server: string
  token: string
}

/** parseInvite reads a `conductor://` invite; null for anything else. */
export function parseInvite(text: string): Invite | null {
  const m = /^conductor:\/\/([^/?#]+)((?:\/[^/?#]+)*)\/join\/([^/?#]+)(?:\?([^#]*))?$/i.exec(text.trim())
  if (!m) return null
  const host = m[1]!
  const path = m[2] ?? ''
  const token = decodeURIComponent(m[3]!)
  if (!HOST.test(host) || !TOKEN.test(token)) return null
  const http = new URLSearchParams(m[4] ?? '').get('http') === '1'
  if (http && !LOOPBACK.test(host)) return null
  return { server: `${http ? 'http' : 'https'}://${host}${path}`, token }
}

/**
 * joinServer is the server a join page may signal to from `?server=`: an
 * `https://` origin (with an optional path), or `http://` on loopback;
 * anything else, a path with `..`, a query or credentials included, is ''
 * (the page's own server).
 */
export function joinServer(value: string): string {
  if (!value || value.includes('..')) return ''
  let u: URL
  try {
    u = new URL(value)
  } catch {
    return ''
  }
  if (u.username || u.password || u.search || u.hash) return ''
  if (u.protocol !== 'https:' && !(u.protocol === 'http:' && LOOPBACK.test(u.host))) return ''
  return `${u.protocol}//${u.host}${u.pathname.replace(/\/+$/, '')}`
}

/** wsBaseOf turns a server base into its WebSocket base. */
export function wsBaseOf(server: string): string {
  return server.replace(/^http/, 'ws')
}
