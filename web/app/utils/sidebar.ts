import type { SessionInfo } from '~/composables/useSessions'
import type { EventType, RouteRow } from './events'
import { groupSessions, type SessionGroupKey } from './sessions'

/** The desktop sidebar's two modes: everything, or an icon rail. */
export type SidebarMode = 'full' | 'rail'
/** The one place the mode is kept: localStorage, per browser. Nuxt UI's own collapse cookie is off (the layout's UDashboardGroup). */
export const SIDEBAR_KEY = 'conductor.sidebar.mode'
/** The key before the rail existed: '1' meant hidden, which is now the rail. */
export const LEGACY_SIDEBAR_KEY = 'conductor.sidebar.hidden'

/** What localStorage offers; a fake in tests. */
export interface KeyValueStore {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

/**
 * The mode saved for this browser. An old hidden flag is moved to the new key
 * on the way; when the new key cannot be written, the old choice still holds
 * and its key stays for a later try. Anything unreadable is full.
 */
export function readSidebarMode(storage: KeyValueStore): SidebarMode {
  let legacy: string | null
  try {
    const mode = storage.getItem(SIDEBAR_KEY)
    if (mode === 'rail' || mode === 'full') return mode
    legacy = storage.getItem(LEGACY_SIDEBAR_KEY)
  } catch {
    return 'full'
  }
  if (legacy === null) return 'full'
  const migrated: SidebarMode = legacy === '1' ? 'rail' : 'full'
  try {
    storage.setItem(SIDEBAR_KEY, migrated)
    storage.removeItem(LEGACY_SIDEBAR_KEY)
  } catch {
    /* not saved: the old key stays */
  }
  return migrated
}

export function writeSidebarMode(storage: KeyValueStore, mode: SidebarMode): void {
  try {
    storage.setItem(SIDEBAR_KEY, mode)
  } catch {
    /* ignore */
  }
}

/** The full sidebar's width in percent of the window: the bounds and the default the layout's UDashboardSidebar declares. */
export const SIDEBAR_SIZE = { min: 14, default: 18, max: 26 } as const
/** The width a drag left, per browser, beside the mode: with Nuxt UI's persistence off, nothing else keeps it over a reload. */
export const SIDEBAR_SIZE_KEY = 'conductor.sidebar.size'

export function clampSidebarSize(size: number): number {
  return Math.min(SIDEBAR_SIZE.max, Math.max(SIDEBAR_SIZE.min, size))
}

/** The width saved for this browser, within the sidebar's bounds; the default when there is none or it is not a number. */
export function readSidebarSize(storage: KeyValueStore): number {
  try {
    const size = Number(storage.getItem(SIDEBAR_SIZE_KEY) || Number.NaN)
    if (Number.isFinite(size)) return clampSidebarSize(size)
  } catch {
    /* no storage: the default */
  }
  return SIDEBAR_SIZE.default
}

/** Saves a width within the sidebar's bounds, to two decimals; a value that is not a number is not saved. */
export function writeSidebarSize(storage: KeyValueStore, size: number): void {
  if (!Number.isFinite(size)) return
  try {
    storage.setItem(SIDEBAR_SIZE_KEY, String(Math.round(clampSidebarSize(size) * 100) / 100))
  } catch {
    /* ignore */
  }
}

/** The sessions a sidebar lists, full or rail: every one, or with `runId` only that run's members. */
export function sidebarSessions(sessions: SessionInfo[], runId?: string): SessionInfo[] {
  return runId ? sessions.filter((s) => s.crew?.runId === runId) : sessions
}

/** Whether `path` is the page of session `id`: its sidebar entry is marked as the current page. */
export function sessionOpen(path: string, id: string): boolean {
  return path === `/sessions/${id}`
}

/** Whether `path` is the crew view of run `runId`. */
export function runOpen(path: string, runId: string): boolean {
  return path === `/runs/${runId}`
}

/** The amber dot on a session needing input follows the Events page's Badge route for needs_input; the group and its text stay. */
export function needsDotShown(routes: Readonly<Record<EventType, RouteRow>>): boolean {
  return routes.needs_input.badge
}

/** The sidebar's three sections, in its order. */
export const SIDEBAR_SECTIONS = ['needs', 'running', 'exited'] as const satisfies readonly SessionGroupKey[]

/** One group of a sidebar section: the sessions of no run (no `runId`), or the members of one run under its name. */
export interface SidebarGroup {
  /** Unique in the sidebar: a run's members can sit in several sections (one needs you, one runs), each its own group. */
  key: string
  runId?: string
  /** The run's name from `runNames`, else its crew's id. */
  label?: string
  sessions: SessionInfo[]
}

/**
 * The sidebar's sessions by section (needs you, running, exited, in groupSessions' order), and in each section the sessions of no run first,
 * then one group per run, in the order its first member comes, named from `runNames` (the live store's runs) or its crew's id. The full
 * sidebar and the rail both draw from it.
 */
export function sidebarGroups(sessions: readonly SessionInfo[], runNames: Readonly<Record<string, string>> = {}): Record<SessionGroupKey, SidebarGroup[]> {
  const g = groupSessions([...sessions])
  const out = { needs: [], running: [], exited: [] } as Record<SessionGroupKey, SidebarGroup[]>
  for (const key of SIDEBAR_SECTIONS) {
    const list = g[key]
    const loose = list.filter((s) => !s.crew)
    if (loose.length) out[key].push({ key: `${key}:`, sessions: loose })
    const byRun = new Map<string, SessionInfo[]>()
    for (const s of list) if (s.crew) byRun.set(s.crew.runId, [...(byRun.get(s.crew.runId) ?? []), s])
    for (const [runId, members] of byRun) out[key].push({ key: `${key}:${runId}`, runId, label: runNames[runId] || members[0]?.crew?.crewId, sessions: members })
  }
  return out
}

export type RailDot = 'needs' | 'running' | 'idle' | 'exited'
export interface RailItem {
  id: string
  name: string
  agentId: string
  dot: RailDot
  message?: string
}
export interface RailGroup {
  /** Unique in the list: a run's members can sit in several sections (one needs you, one runs), each its own group. */
  key: string
  runId?: string
  label?: string
  items: RailItem[]
}

/** The rail's avatars: sidebarGroups' groups in order, each session as an avatar with its dot. */
export function railGroups(sessions: readonly SessionInfo[], runNames: Readonly<Record<string, string>> = {}): RailGroup[] {
  const sections = sidebarGroups(sessions, runNames)
  const dot = (key: SessionGroupKey, s: SessionInfo): RailDot => (key === 'needs' ? 'needs' : key === 'exited' ? 'exited' : s.status === 'running' ? 'running' : 'idle')
  return SIDEBAR_SECTIONS.flatMap((key) =>
    sections[key].map((g) => ({
      key: g.key,
      runId: g.runId,
      label: g.label,
      items: g.sessions.map((s) => ({ id: s.id, name: s.name, agentId: s.agentId, dot: dot(key, s), message: s.attention?.message || undefined })),
    })),
  )
}
