import type { AgentInfo, BroadcastResult, BroadcastSkipReason, CrewInput, CrewMember, CrewStart, JoinRunMember, RunInfo, RunMember, SessionInfo } from '~/composables/useSessions'
import type { ActivityEntry } from './protocol'
import { joinArgv, splitArgs } from './argv'
import { isEnded } from './attention'
import { EVENT_INFO, feedTime, markOf, type EventColor, type FeedEntry } from './events'

/**
 * The body of POST /api/crews and PUT /api/crews/{id}: the fields the server accepts and nothing else, since it rejects unknown fields. A crew as
 * listed (a CrewInfo, with id, createdAt and updatedAt) or members carrying page state can be passed as they are.
 */
export function toCrewInput(c: CrewInput): CrewInput {
  return {
    name: c.name,
    goal: c.goal,
    cwd: c.cwd,
    where: c.where,
    isolation: c.isolation,
    openAfterLaunch: c.openAfterLaunch,
    viewLinkTtlSeconds: c.viewLinkTtlSeconds,
    members: c.members.map(toCrewMember),
  }
}

/** A member as the server accepts it, in a crew or on its own (POST /api/runs/{run}/members): its fields and nothing else. */
export function toCrewMember(m: CrewMember): CrewMember {
  return {
    name: m.name,
    agentId: m.agentId,
    prompt: m.prompt,
    args: m.args,
    start: { when: m.start.when, member: m.start.member },
  }
}

/** A member's name as the server accepts it (internal/crew memberNamePattern): it becomes a branch and a worktree name. */
export const MEMBER_NAME = /^[a-z0-9][a-z0-9._-]{0,39}$/

/** Why the server would refuse a member name, or '' when it is fine: its pattern, what git refuses in a branch name, and a name another member has. */
export function memberNameError(name: string, taken: readonly string[] = []): string {
  if (!name) return 'Give the member a name'
  if (!MEMBER_NAME.test(name)) return 'Lower-case letters, digits, ".", "_" and "-", starting with a letter or digit, at most 40'
  if (name.includes('..') || name.endsWith('.') || name.endsWith('.lock')) return 'git refuses this as a branch name: no "..", and no "." or ".lock" at the end'
  if (taken.includes(name)) return 'Another member has this name'
  return ''
}

/** Drops what may not end a member name: ".", "-", "_" and ".lock". */
function trimNameEnd(s: string): string {
  let out = s
  for (;;) {
    const next = out.replace(/\.lock$/, '').replace(/[._-]+$/, '')
    if (next === out) return out
    out = next
  }
}

/** A valid member name made from free text (a session's name, an agent id), not one of `taken`: `-2`, `-3`… are added when it is. */
export function memberNameFrom(text: string, taken: readonly string[] = []): string {
  const cleaned = text
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '-')
    .replace(/\.{2,}/g, '.')
    .replace(/^[^a-z0-9]+/, '')
  const base = trimNameEnd(cleaned.slice(0, 40)) || 'agent'
  if (!taken.includes(base)) return base
  for (let i = 2; ; i++) {
    const suffix = `-${i}`
    const name = (trimNameEnd(base.slice(0, 40 - suffix.length)) || 'agent') + suffix
    if (!taken.includes(name)) return name
  }
}

/** A start condition as one value of the Starts select: `immediately`, `manual` or `after:<member>`. */
export function startValue(s: CrewStart): string {
  return s.when === 'after' ? `after:${s.member ?? ''}` : s.when
}

/** The start condition a value of the Starts select stands for (startValue). */
export function startFrom(value: string): CrewStart {
  if (value.startsWith('after:')) return { when: 'after', member: value.slice('after:'.length) }
  return { when: value === 'manual' ? 'manual' : 'immediately' }
}

/** What "New crew" starts from: on the server, a worktree per agent, the run view opened after launch, and one member on the first catalog agent. */
export function defaultCrew(catalog: readonly AgentInfo[]): CrewInput {
  const first = catalog[0]
  return {
    name: 'New crew',
    goal: '',
    cwd: '',
    where: 'server',
    isolation: 'worktree',
    openAfterLaunch: true,
    members: first ? [{ name: 'lead', agentId: first.id, prompt: '', start: { when: 'immediately' } }] : [],
  }
}

/** A member in the editor: its fields, a key that stays while it is renamed or moved, and its extra arguments as typed. */
export interface DraftMember extends CrewMember {
  key: number
  /** What the Extra args field shows; `args` is it split with splitArgs. */
  argsText: string
}

/** A crew in the editor; no id until it is saved. */
export interface DraftCrew extends CrewInput {
  id?: string
  members: DraftMember[]
}

let memberKeys = 0

export function draftMember(m: CrewMember): DraftMember {
  return { ...toCrewMember(m), key: ++memberKeys, argsText: joinArgv(m.args ?? []) }
}

export function toDraft(c: CrewInput & { id?: string }): DraftCrew {
  return { ...toCrewInput(c), id: c.id, members: c.members.map(draftMember) }
}

/** The extra arguments typed for a member, split as the Launch dialog splits them; undefined when there are none. */
export function argsFrom(text: string): string[] | undefined {
  const args = splitArgs(text)
  return args.length ? args : undefined
}

/** A crew as the server would store it, for telling a draft from what is saved. */
export function crewKey(c: CrewInput): string {
  return JSON.stringify(toCrewInput(c))
}

/** A member's state as the run view shows it (memberStatus). */
export type MemberStatus = RunMember['status'] | 'needs_input'

/** The live session of a member: by its id once the run names it, else by the crew tag the session carries. */
function memberSession(run: RunInfo, member: RunMember, sessions: readonly SessionInfo[]): SessionInfo | undefined {
  if (member.sessionId) {
    const s = sessions.find((x) => x.id === member.sessionId)
    if (s) return s
  }
  return sessions.find((x) => x.crew?.runId === run.id && x.crew.member === member.name)
}

/**
 * A member's state from the run as last read, brought up to date by its live session: a running member whose session waits on a prompt
 * `needs_input`, a member whose session ended `ended`, and a pending member whose session exists `starting`. A starting member is never
 * `needs_input`: its prompt is still to be typed.
 */
export function memberStatus(run: RunInfo, member: RunMember, sessions: readonly SessionInfo[]): MemberStatus {
  if (member.status === 'ended') return 'ended'
  const s = memberSession(run, member, sessions)
  if (s && isEnded(s.status)) return 'ended'
  if (member.status === 'running') return s?.attention?.state === 'needs_input' ? 'needs_input' : 'running'
  if (member.status === 'pending' && s) return 'starting'
  return member.status
}

/** How many members wait on a prompt and how many others run (memberStatus). */
export function runCounts(run: RunInfo, sessions: readonly SessionInfo[]): { needs: number; running: number } {
  let needs = 0
  let running = 0
  for (const m of run.members) {
    const st = memberStatus(run, m, sessions)
    if (st === 'needs_input') needs++
    else if (st === 'running') running++
  }
  return { needs, running }
}

/** Whether a run still goes: not stopped, and a member has not ended. */
export function runActive(run: RunInfo): boolean {
  return !run.stoppedAt && run.members.some((m) => m.status !== 'ended')
}

/** One line of the crew activity card. */
export interface CrewFeedItem {
  key: string
  at: string
  /** The time of day, as the Events feed shows it. */
  time: string
  /** The member it is about; empty for a line of the run's own log. */
  who: string
  what: string
  color: EventColor
  /** An artifact's URL, shown as text; `link` is set only when linkableUrl allows it. */
  url?: string
  link: string | null
  /** The member's session, for its events. */
  sessionId?: string
}

/**
 * The crew activity card: the Events feed entries of the run's member sessions (`members` maps each session id to its member name) and
 * the run's own log, newest first, at most `limit` lines. Text only, as the Events feed: nothing is rendered as HTML.
 */
export function crewFeed(feed: readonly FeedEntry[], log: readonly ActivityEntry[], members: ReadonlyMap<string, string>, limit = 200): CrewFeedItem[] {
  const items: Array<CrewFeedItem & { ms: number; order: number }> = []
  for (const e of feed) {
    const who = members.get(e.sessionId)
    if (who === undefined) continue
    const label = markOf(e.event, e).label
    items.push({
      key: `e${e.seq}`,
      at: e.at,
      time: e.time,
      who,
      what: e.detail ? `${label}: ${e.detail}` : label,
      color: EVENT_INFO[e.event].color,
      url: e.url,
      link: e.link,
      sessionId: e.sessionId,
      ms: Date.parse(e.at),
      order: e.seq,
    })
  }
  log.forEach((e, i) => {
    items.push({ key: `l${i}`, at: e.at, time: feedTime(e.at), who: '', what: e.message ?? e.type, color: e.type === 'error' ? 'error' : 'neutral', link: null, ms: Date.parse(e.at), order: i })
  })
  items.sort((a, b) => (b.ms || 0) - (a.ms || 0) || b.order - a.order)
  return items.slice(0, limit).map(({ ms: _ms, order: _order, ...item }) => item)
}

const SKIP_REASON: Record<BroadcastSkipReason, string> = {
  needs_input: 'waiting on a prompt',
  not_running: 'not running',
  unknown: 'not a member',
}

/** The toast after a broadcast: how many got the line, who, and who was skipped and why. */
export function broadcastSummary(r: BroadcastResult): { title: string; description: string; color: 'success' | 'warning' | 'error' } {
  const total = r.sent.length + r.skipped.length
  const parts: string[] = []
  if (r.sent.length) parts.push(`Sent to ${r.sent.join(', ')}.`)
  if (r.skipped.length) parts.push(`Skipped ${r.skipped.map((s) => `${s.member} (${SKIP_REASON[s.reason] ?? s.reason})`).join(', ')}.`)
  const color = !r.skipped.length ? 'success' : r.sent.length ? 'warning' : 'error'
  return { title: `Sent to ${r.sent.length} of ${total}`, description: parts.join(' '), color }
}

/**
 * The name a broadcast is recorded under: the display name, or else the server's OS user (`whoami`, GET /api/whoami), which the display
 * name defaults to. Undefined, which the server records as `guest`, only when both are empty or the server cannot say.
 */
export async function broadcastByName(displayName: string, whoami: () => Promise<{ user: string }>): Promise<string | undefined> {
  const name = displayName.trim()
  if (name) return name
  try {
    return (await whoami()).user?.trim() || undefined
  } catch {
    return undefined
  }
}

/**
 * The run whose members the sidebar lists alone (its group variant): the run of a crew view (`/runs/<id>`), or of the member session a
 * page shows (`/sessions/<id>`), however that page was reached. Undefined everywhere else.
 */
export function sidebarRunFor(path: string, sessions: readonly SessionInfo[]): string | undefined {
  const m = /^\/(runs|sessions)\/([^/]+)\/?$/.exec(path)
  if (!m) return undefined
  let id: string
  try {
    id = decodeURIComponent(m[2]!)
  } catch {
    return undefined
  }
  if (m[1] === 'runs') return id
  return sessions.find((s) => s.id === id)?.crew?.runId
}

/** How long a launch's view link waits for its crew view to take it. */
const VIEW_LINK_HOLD_MS = 60_000

let heldViewLink: { runId: string; url: string; ttlSeconds: number; at: number } | null = null
let heldTimer: ReturnType<typeof setTimeout> | undefined

/**
 * Hands the view link a launch returned to the crew view the launch opens, in memory only: the crew view takes it once (takeViewLink), and
 * a timer drops it once the hold is over, so a token nobody took does not stay in memory.
 */
export function holdViewLink(runId: string, url: string, ttlSeconds: number, now = Date.now()) {
  clearTimeout(heldTimer)
  const held = { runId, url, ttlSeconds, at: now }
  heldViewLink = held
  heldTimer = setTimeout(() => {
    if (heldViewLink === held) heldViewLink = null
  }, VIEW_LINK_HOLD_MS)
}

/** The view link held for this run, once; any call drops what is held, as does a minute nobody took it in. */
export function takeViewLink(runId: string, now = Date.now()): { url: string; ttlSeconds: number } | null {
  const held = heldViewLink
  heldViewLink = null
  if (!held || held.runId !== runId || now - held.at > VIEW_LINK_HOLD_MS) return null
  return { url: held.url, ttlSeconds: held.ttlSeconds }
}

/** How long a run name that could not be read waits before it is asked for again. */
export const RUN_NAME_RETRY_MS = 5_000

/** Which run names the layout reads: each once, and again after a failure once RUN_NAME_RETRY_MS has passed. */
export class RunNameAsks {
  /** By run: when it may be asked again; Infinity while asked or read. */
  private next = new Map<string, number>()
  shouldAsk(id: string, now = Date.now()): boolean {
    const at = this.next.get(id)
    if (at !== undefined && now < at) return false
    this.next.set(id, Infinity)
    return true
  }
  failed(id: string, now = Date.now()): void {
    this.next.set(id, now + RUN_NAME_RETRY_MS)
  }
}

/** A run link's member tile: its label and classes, from what its terminal last reported (`heard`), else the run's own status. */
export function joinTileStatus(m: Pick<JoinRunMember, 'status'>, heard?: { status?: string; attention?: string }): { label: string; cls: string; dot: string } {
  const st = heard?.status ?? m.status
  if (heard?.attention === 'needs_input' && (st === 'running' || st === 'starting')) return { label: 'Needs input', cls: 'text-warning', dot: 'bg-warning' }
  if (st === 'running') return { label: 'Running', cls: 'text-success', dot: 'bg-success' }
  return { label: st.replace('_', ' '), cls: 'text-muted', dot: 'bg-neutral-400' }
}
