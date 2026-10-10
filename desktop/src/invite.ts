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

/** How long an invite waits for the window's page to take it before the window loads the join page itself. */
export const INVITE_ACK_MS = 10_000

export interface InviteDeliveryDeps {
  /** Hands the invite to the page over the bridge (`conductor:invite`, with its id), which routes to its join page in place. */
  send(inv: Invite, id: number): void
  /** Loads the join page in the window: a full page load, for a page that never took the invite. */
  load(inv: Invite): void
  /** Asks the page whether it listens (`conductor:inviteAsk`); one that does says so again (`conductor:inviteReady`). */
  ask(): void
  /** Whether the window's main frame is loading a document: what a page says meanwhile may come from the one being replaced. */
  loading(): boolean
  setTimer(f: () => void, ms: number): unknown
  clearTimer(t: unknown): void
}

/**
 * InviteDelivery takes an invite to the open window's page without reloading it, so the workbench keeps its terminals and its live
 * store. The page says it listens (`conductor:inviteReady`) and, once it has routed an invite, that it took it
 * (`conductor:inviteTaken`, by id); an invite stays pending, the latest winning, until it is taken. While the main frame loads a
 * document neither word counts, since it may come from the page being replaced; when loading stops the page is asked again. An
 * invite not taken within INVITE_ACK_MS (a page from before the bridge, an error page, an invite the page refused) gets the join page
 * loaded instead, where the join page shows what is wrong with it.
 */
export class InviteDelivery {
  private listening = false
  private pending: { inv: Invite; id: number } | null = null
  private seq = 0
  private timer: unknown = null

  constructor(private readonly d: InviteDeliveryDeps) {}

  /** deliver sends the invite to a page that listens, or keeps it for the next one ('sent' or 'waiting'); either way it is pending until taken. */
  deliver(inv: Invite): 'sent' | 'waiting' {
    this.pending = { inv, id: ++this.seq }
    this.stopTimer()
    this.timer = this.d.setTimer(() => {
      this.timer = null
      const p = this.pending
      this.pending = null
      if (p) this.d.load(p.inv)
    }, INVITE_ACK_MS)
    if (this.listening && !this.d.loading()) {
      this.d.send(inv, this.pending.id)
      return 'sent'
    }
    return 'waiting'
  }

  /** pageReady is the page's word that it listens: the pending invite goes to it now. */
  pageReady(): void {
    if (this.d.loading()) return
    this.listening = true
    if (this.pending) this.d.send(this.pending.inv, this.pending.id)
  }

  /** pageTook is the page's word that it routed invite id: it is delivered. */
  pageTook(id: number): void {
    if (this.d.loading() || !this.pending || this.pending.id !== id) return
    this.pending = null
    this.stopTimer()
  }

  /** pageLeft is a main-frame navigation starting (or a new window): nothing listens until the next page says so. */
  pageLeft(): void {
    this.listening = false
  }

  /** settled is the main frame done loading: the page there is asked whether it listens when an invite is pending. */
  settled(): void {
    if (this.pending) this.d.ask()
  }

  private stopTimer(): void {
    if (this.timer !== null) this.d.clearTimer(this.timer)
    this.timer = null
  }
}
