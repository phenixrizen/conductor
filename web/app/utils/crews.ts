import type { AgentInfo, BroadcastResult, BroadcastSkipReason, CrewInfo, CrewInput, CrewMember, CrewStart, CrewSummary, JoinRunMember, RunInfo, RunMember, SessionInfo } from '~/composables/useSessions'
import type { ActivityEntry } from './protocol'
import { joinArgv, splitArgs } from './argv'
import { isEnded } from './attention'
import { EVENT_INFO, feedTime, markOf, type EventColor, type FeedEntry } from './events'

/**
 * The body of POST /api/crews and PUT /api/crews/{id}: the fields the server accepts and nothing else, since it rejects unknown fields. A crew read
 * in full (a CrewInfo from GET /api/crews/{id}, with id, createdAt and updatedAt) or members carrying page state can be passed as they are.
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
    yolo: c.yolo,
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

/** The page of the Crews list to show after a delete left `left` crews on page `page`: the one before, when the delete emptied a page past the first. */
export function pageAfterDelete(page: number, left: number): number {
  return left === 0 && page > 1 ? page - 1 : page
}

/** The summary GET /api/crews lists for `c`: what the list shows after a save, until the list is read again. */
export function summaryOf(c: CrewInfo): CrewSummary {
  return { id: c.id, name: c.name, goal: c.goal, cwd: c.cwd, where: c.where, isolation: c.isolation, members: c.members.map((m) => ({ name: m.name, agentId: m.agentId, start: m.start })), updatedAt: c.updatedAt }
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
 * A member's state from the run as last read, brought up to date by its live session: a member whose session ended `ended`, a starting or
 * running member whose session waits on a prompt `needs_input` (a starting one waits on its trust question: its prompt is held until a
 * person answers), and a pending member whose session exists `starting`. Without a live session the run's own `needsInput` decides.
 */
export function memberStatus(run: RunInfo, member: RunMember, sessions: readonly SessionInfo[]): MemberStatus {
  if (member.status === 'ended') return 'ended'
  const s = memberSession(run, member, sessions)
  if (s && isEnded(s.status)) return 'ended'
  const waiting = s ? s.attention?.state === 'needs_input' : !!member.needsInput
  if (member.status === 'pending') return s ? (waiting ? 'needs_input' : 'starting') : 'pending'
  return waiting ? 'needs_input' : member.status
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
  no_enter: 'typed, no Enter: a question came up',
}

/** Why a member was skipped, in words: a broadcast's reason, or a run chat's `not_sent`. */
export function skipWords(reason: string): string {
  return (SKIP_REASON as Record<string, string>)[reason] ?? reason
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
 * The run whose members the sidebar lists alone (its group variant): the run of a run page (`/runs/<id>`), or of the member session a
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

/** How long a launch's view link waits for its run page to take it. */
const VIEW_LINK_HOLD_MS = 60_000

let heldViewLink: { runId: string; url: string; ttlSeconds: number; at: number } | null = null
let heldTimer: ReturnType<typeof setTimeout> | undefined

/**
 * Hands the view link a launch returned to the run page the launch opens, in memory only: the run page takes it once (takeViewLink), and
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
  // The timer's closure holds the link too: it goes with it.
  clearTimeout(heldTimer)
  heldTimer = undefined
  if (!held || held.runId !== runId || now - held.at > VIEW_LINK_HOLD_MS) return null
  return { url: held.url, ttlSeconds: held.ttlSeconds }
}

/** A run link's member tile: its label and classes, from what its terminal last reported (`heard`), else the run's own status. */
export function joinTileStatus(m: Pick<JoinRunMember, 'status'>, heard?: { status?: string; attention?: string }): { label: string; cls: string; dot: string } {
  const st = heard?.status ?? m.status
  if (heard?.attention === 'needs_input' && (st === 'running' || st === 'starting')) return { label: 'Needs input', cls: 'text-warning', dot: 'bg-warning' }
  if (st === 'running') return { label: 'Running', cls: 'text-success', dot: 'bg-success' }
  return { label: st.replace('_', ' '), cls: 'text-muted', dot: 'bg-neutral-400' }
}

/** A member as the broadcast bar weighs it: whether its session runs, and whether that session waits on a prompt. */
export interface BroadcastMember {
  name: string
  live: boolean
  waiting: boolean
}

/**
 * The run page's broadcast selection. Every member whose session runs is selected unless the person unticked it (`choices`, by name: the
 * person's own ticks and unticks, which outlast every read of the run and every run event); a member that starts later is selected as it
 * appears. `selected` is what the bar sends, in the run's order; `sending` the selected members the server will type into; `waiting` the
 * selected members it will skip because their sessions wait on a prompt.
 */
export function broadcastSelection(members: readonly BroadcastMember[], choices: Readonly<Record<string, boolean>>): { selected: string[]; sending: string[]; waiting: string[] } {
  const selected = members.filter((m) => choices[m.name] ?? m.live)
  return {
    selected: selected.map((m) => m.name),
    sending: selected.filter((m) => m.live && !m.waiting).map((m) => m.name),
    waiting: selected.filter((m) => m.live && m.waiting).map((m) => m.name),
  }
}
