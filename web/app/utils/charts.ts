import type { RunInfo, SessionInfo } from '~/composables/useSessions'
import type { FeedEntry } from '~/utils/events'

/**
 * The numbers behind the charts: activity per minute for the Events page,
 * a crew's last runs for the Crews page, sessions by attention state for
 * the Wall. Each is drawn only when it says something: a chart needs at
 * least two points (enough), and the empty state says what will appear.
 */

/** The least points a chart is drawn with. */
export const MIN_POINTS = 2

export function enough(points: number): boolean {
  return points >= MIN_POINTS
}

/** The event groups of the activity chart, and which feed events each counts. */
export const ACTIVITY_GROUPS = {
  attention: ['needs_input', 'done', 'working'],
  reports: ['progress', 'artifact'],
  handoff: ['handoff'],
  tool: ['tool_use', 'tool_denied'],
  error: ['error', 'exit_nonzero'],
} as const satisfies Record<string, readonly FeedEntry['event'][]>

export type ActivityGroup = keyof typeof ACTIVITY_GROUPS

export interface ActivityBucket {
  /** ms: the minute's start. */
  minute: number
  attention: number
  reports: number
  handoff: number
  tool: number
  error: number
}

function groupOf(event: FeedEntry['event']): ActivityGroup | undefined {
  for (const [g, events] of Object.entries(ACTIVITY_GROUPS)) if ((events as readonly string[]).includes(event)) return g as ActivityGroup
  return undefined
}

/**
 * The feed's events per minute over the last `minutes`, oldest first, one
 * bucket per minute whether or not anything happened (a flat line is the
 * truth). Entries older than the window, or in the future, are left out.
 */
export function activityBuckets(entries: ReadonlyArray<Pick<FeedEntry, 'at' | 'event'>>, now: number, minutes = 60): ActivityBucket[] {
  const end = Math.floor(now / 60_000) * 60_000
  const start = end - (minutes - 1) * 60_000
  const buckets: ActivityBucket[] = []
  for (let t = start; t <= end; t += 60_000) buckets.push({ minute: t, attention: 0, reports: 0, handoff: 0, tool: 0, error: 0 })
  for (const e of entries) {
    const at = Date.parse(e.at)
    if (Number.isNaN(at) || at < start || at >= end + 60_000) continue
    const g = groupOf(e.event)
    if (!g) continue
    const b = buckets[Math.floor((at - start) / 60_000)]
    if (b) b[g]++
  }
  return buckets
}

/** How many of the feed's events the buckets hold, to decide whether the chart says anything. */
export function activityTotal(buckets: readonly ActivityBucket[]): number {
  return buckets.reduce((n, b) => n + b.attention + b.reports + b.handoff + b.tool + b.error, 0)
}

export type RunOutcome = 'finished' | 'stopped' | 'running'

export interface RunBar {
  id: string
  /** The run's start, ms. */
  startedAt: number
  /** Minutes the run lasted, or has lasted. */
  minutes: number
  outcome: RunOutcome
  needsInput: number
  /** How many members ended with an error. */
  errors: number
}

/** The outcome a run's record or live state shows. */
export function outcomeOf(run: Pick<RunInfo, 'state' | 'stoppedAt'>): RunOutcome {
  if (run.stoppedAt || run.state === 'stopped') return 'stopped'
  if (run.state === 'finished') return 'finished'
  return 'running'
}

/** A run's length: to its stop, to its last member's end, or to now while it goes. */
export function runLength(run: Pick<RunInfo, 'startedAt' | 'stoppedAt' | 'members' | 'state'>, now: number): number {
  const start = Date.parse(run.startedAt)
  if (Number.isNaN(start)) return 0
  let end: number | undefined = run.stoppedAt ? Date.parse(run.stoppedAt) : undefined
  if (end === undefined && run.state === 'finished') {
    for (const m of run.members) {
      const t = m.endedAt ? Date.parse(m.endedAt) : NaN
      if (!Number.isNaN(t) && (end === undefined || t > end)) end = t
    }
  }
  return Math.max(0, (end ?? now) - start)
}

/** The last `limit` runs of a crew as bars, oldest first, from the runs newest first (live and recorded). */
export function runBars(runs: readonly RunInfo[], now: number, limit = 20): RunBar[] {
  return runs
    .slice(0, limit)
    .map((r) => ({
      id: r.id,
      startedAt: Date.parse(r.startedAt) || 0,
      minutes: Math.round((runLength(r, now) / 60_000) * 10) / 10,
      outcome: outcomeOf(r),
      needsInput: r.needsInput ?? 0,
      errors: r.members.filter((m) => m.error).length,
    }))
    .reverse()
}

/** The bars as the bar chart takes them: one series per outcome, so each bar is coloured by its outcome. */
export function runBarSeries(bars: readonly RunBar[]): Array<{ label: string; finished: number; stopped: number; running: number; id: string; needsInput: number; errors: number }> {
  return bars.map((b) => ({
    label: new Date(b.startedAt).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' }),
    finished: b.outcome === 'finished' ? b.minutes : 0,
    stopped: b.outcome === 'stopped' ? b.minutes : 0,
    running: b.outcome === 'running' ? b.minutes : 0,
    id: b.id,
    needsInput: b.needsInput,
    errors: b.errors,
  }))
}

export type AttentionSlice = 'needs_input' | 'working' | 'done' | 'idle'

/** Sessions by attention state, for the Wall's donut: active sessions only, in a fixed order. */
export function attentionSlices(sessions: readonly SessionInfo[], active: (s: SessionInfo) => boolean): Record<AttentionSlice, number> {
  const out: Record<AttentionSlice, number> = { needs_input: 0, working: 0, done: 0, idle: 0 }
  for (const s of sessions) {
    if (!active(s)) continue
    const st = s.attention?.state
    if (st === 'needs_input' || st === 'working' || st === 'done') out[st]++
    else out.idle++
  }
  return out
}

/** The role colours the charts draw with, read from the theme's variables at runtime (brand.md anchors as the fallback). */
export const ROLE_FALLBACK = {
  primary: '#4a6a5a',
  success: '#3f8f5f',
  warning: '#c98a1b',
  info: '#245d85',
  error: '#a1332f',
  neutral: '#71717a',
} as const

export type RoleColors = Record<keyof typeof ROLE_FALLBACK, string>

/** Reads `--ui-<role>` from the document, falling back to the brand anchors; `--ui-text-muted` stands for neutral. */
export function roleColors(read: (name: string) => string = (n) => (typeof getComputedStyle === 'function' ? getComputedStyle(document.documentElement).getPropertyValue(n) : '')): RoleColors {
  const pick = (name: string, fallback: string) => {
    const v = read(name).trim()
    return v && !v.includes('var(') ? v : fallback
  }
  return {
    primary: pick('--ui-primary', ROLE_FALLBACK.primary),
    success: pick('--ui-success', ROLE_FALLBACK.success),
    warning: pick('--ui-warning', ROLE_FALLBACK.warning),
    info: pick('--ui-info', ROLE_FALLBACK.info),
    error: pick('--ui-error', ROLE_FALLBACK.error),
    neutral: pick('--ui-text-muted', ROLE_FALLBACK.neutral),
  }
}
