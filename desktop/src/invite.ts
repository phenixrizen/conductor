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

/** How long an invite waits for the window's page to say it listens before the window loads the join page itself. */
export const INVITE_ACK_MS = 10_000

export interface InviteDeliveryDeps {
  /** Hands the invite to the page over the bridge (`conductor:invite`), which routes to its join page in place. */
  send(inv: Invite): void
  /** Loads the join page in the window: a full page load, for a page that never said it listens. */
  load(inv: Invite): void
  setTimer(f: () => void, ms: number): unknown
  clearTimer(t: unknown): void
}

/**
 * InviteDelivery takes an invite to the open window's page without reloading it, so the workbench keeps its terminals and its live
 * store. The page says it listens once loaded (`conductor:inviteReady`); until then, and from the moment a navigation starts, an
 * invite waits, the latest winning; a page that has not said so within INVITE_ACK_MS (a server page from before the bridge, an error
 * page) gets the join page loaded instead.
 */
export class InviteDelivery {
  private listening = false
  private waiting: Invite | null = null
  private timer: unknown = null

  constructor(private readonly d: InviteDeliveryDeps) {}

  /** deliver sends the invite to a page that listens, or keeps it for the next one ('sent' or 'waiting'). */
  deliver(inv: Invite): 'sent' | 'waiting' {
    if (this.listening) {
      this.d.send(inv)
      return 'sent'
    }
    this.waiting = inv
    if (this.timer === null) {
      this.timer = this.d.setTimer(() => {
        this.timer = null
        const w = this.waiting
        this.waiting = null
        if (w) this.d.load(w)
      }, INVITE_ACK_MS)
    }
    return 'waiting'
  }

  /** pageReady is the page's word that it listens: a waiting invite goes to it now. */
  pageReady(): void {
    this.listening = true
    this.stopTimer()
    const w = this.waiting
    this.waiting = null
    if (w) this.d.send(w)
  }

  /** pageLeft is a main-frame navigation starting (or a new window): nothing listens until the next page says so. */
  pageLeft(): void {
    this.listening = false
  }

  private stopTimer(): void {
    if (this.timer !== null) this.d.clearTimer(this.timer)
    this.timer = null
  }
}
