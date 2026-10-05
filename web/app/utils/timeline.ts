import type { RunInfo, SessionInfo } from '~/composables/useSessions'
import type { FeedEntry } from '~/utils/events'
import { handoffsOf, type HandoffSeen } from './crewGraph'

/**
 * The run timeline: one row per member, a bar from its start to its end or
 * now, amber segments while its session waited for input, markers where it
 * handed work to another member, and the run's stopped line. Built from the
 * run as last read and the live feed's attention entries of the members'
 * sessions, so it follows the stream without a read of its own.
 */

export interface WaitSegment {
  /** ms since the epoch. */
  from: number
  /** ms; `to` is now while the wait goes on. */
  to: number
  /** Why it waited, as the attention entry said (a trust question, the agent's own). */
  reason?: string
  /** Whether the wait has not ended yet. */
  open: boolean
}

export interface HandoffMark {
  at: number
  to: string
  message?: string
}

export interface TimelineRow {
  name: string
  agentId: string
  /** ms; absent while the member has not started. */
  start?: number
  /** ms; absent while it goes on (then the bar ends at now). */
  end?: number
  status: 'pending' | 'starting' | 'running' | 'ended'
  error?: string
  waits: WaitSegment[]
  handoffs: HandoffMark[]
}

export interface Timeline {
  rows: TimelineRow[]
  /** ms: the run's start, the earliest moment drawn. */
  from: number
  /** ms: the latest moment drawn: the run's stop, the last end, or now. */
  to: number
  /** ms: when the run was stopped, if it was. */
  stoppedAt?: number
}

const ms = (iso?: string): number | undefined => {
  if (!iso) return undefined
  const t = Date.parse(iso)
  return Number.isNaN(t) ? undefined : t
}

/**
 * The wait segments of one session from its attention entries, in order: a
 * needs_input opens one; the next working, done or clear entry of that
 * session closes it. An open wait ends at now. Entries before `since` (the
 * member's start) are left out.
 */
export function waitsOf(entries: ReadonlyArray<Pick<FeedEntry, 'at' | 'event' | 'detail' | 'message'>>, since: number, now: number): WaitSegment[] {
  const out: WaitSegment[] = []
  let open: WaitSegment | null = null
  const sorted = [...entries].sort((a, b) => a.at.localeCompare(b.at))
  for (const e of sorted) {
    const at = ms(e.at)
    if (at === undefined || at < since) continue
    if (e.event === 'needs_input') {
      if (!open) {
        open = { from: at, to: now, reason: e.detail || e.message || undefined, open: true }
        out.push(open)
      }
    } else if ((e.event === 'working' || e.event === 'done') && open) {
      open.to = at
      open.open = false
      open = null
    }
  }
  return out
}

/** The rows of a run's timeline, from the run, its members' live sessions, the feed and the handoffs seen (handoffsOf). */
export function timelineOf(run: RunInfo, sessions: readonly SessionInfo[], feed: readonly FeedEntry[], now: number): Timeline {
  const memberOf = new Map<string, string>()
  for (const m of run.members) if (m.sessionId) memberOf.set(m.sessionId, m.name)
  for (const s of sessions) if (s.crew?.runId === run.id && s.crew.member) memberOf.set(s.id, s.crew.member)
  const handoffs: HandoffSeen[] = handoffsOf(run.log, feed, memberOf)
  const from = ms(run.startedAt) ?? now
  const stoppedAt = ms(run.stoppedAt)
  let to = stoppedAt ?? from
  const rows: TimelineRow[] = run.members.map((m) => {
    const sessionIds = [...memberOf.entries()].filter(([, name]) => name === m.name).map(([id]) => id)
    const start = ms(m.startedAt)
    const live = sessions.find((s) => (m.sessionId ? s.id === m.sessionId : s.crew?.runId === run.id && s.crew.member === m.name))
    let end = ms(m.endedAt)
    if (end === undefined && live?.endedAt && m.status !== 'pending') end = ms(live.endedAt)
    if (end === undefined && m.status === 'ended') end = stoppedAt ?? now
    const status = m.status === 'ended' || (end !== undefined && m.status !== 'pending') ? 'ended' : m.status
    const own = feed.filter((e) => sessionIds.includes(e.sessionId))
    const waits = start === undefined ? [] : waitsOf(own, start, end ?? stoppedAt ?? now).filter((w) => w.from <= (end ?? now))
    for (const w of waits) if (end !== undefined && w.to > end) w.to = end
    // A session waiting now, as the live store shows it: the feed holds
    // only what arrived while this page was open, so a wait that began
    // before is drawn from the attention state's `since`.
    if (start !== undefined && end === undefined && live?.attention?.state === 'needs_input' && !waits.some((w) => w.open)) {
      const since = ms(live.attention.since) ?? now
      waits.push({ from: Math.max(start, Math.min(since, now)), to: now, reason: live.attention.message || undefined, open: true })
    }
    const marks: HandoffMark[] = handoffs
      .filter((h) => h.from === m.name)
      .map((h) => ({ at: ms(h.at) ?? now, to: h.to, message: h.message }))
      .sort((a, b) => a.at - b.at)
    const last = end ?? (start !== undefined ? now : undefined)
    if (last !== undefined && last > to) to = last
    return { name: m.name, agentId: m.agentId, start, end, status, error: m.error, waits, handoffs: marks }
  })
  if (!stoppedAt && rows.some((r) => r.start !== undefined && r.end === undefined)) to = Math.max(to, now)
  return { rows, from, to: Math.max(to, from + 1), stoppedAt }
}

/** The x of a moment within a width: `from` at 0, `to` at `width`. */
export function scaleX(t: number, from: number, to: number, width: number): number {
  if (to <= from) return 0
  return Math.max(0, Math.min(width, ((t - from) / (to - from)) * width))
}

/** Tick moments between from and to: every minute, 5, 15 or 60 minutes, so at most ~8 ticks. */
export function ticks(from: number, to: number): number[] {
  const span = Math.max(1, to - from)
  const steps = [60_000, 5 * 60_000, 15 * 60_000, 60 * 60_000, 6 * 3_600_000, 24 * 3_600_000]
  const step = steps.find((s) => span / s <= 8) ?? steps[steps.length - 1]!
  const out: number[] = []
  for (let t = Math.ceil(from / step) * step; t <= to; t += step) out.push(t)
  return out
}

/** A moment as the timeline labels it: HH:MM (and :SS under five minutes of span). */
export function tickLabel(t: number, span: number): string {
  const d = new Date(t)
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  if (span < 5 * 60_000) return `${hh}:${mm}:${String(d.getSeconds()).padStart(2, '0')}`
  return `${hh}:${mm}`
}

/** A duration in words: 12s, 4m 05s, 1h 12m. */
export function durationLabel(msSpan: number): string {
  const s = Math.max(0, Math.round(msSpan / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`
}
