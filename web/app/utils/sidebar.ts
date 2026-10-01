import type { SessionInfo } from '~/composables/useSessions'
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

/** The mode saved for this browser. An old hidden flag is moved to the new key on the way. Anything unreadable is full. */
export function readSidebarMode(storage: KeyValueStore): SidebarMode {
  try {
    const mode = storage.getItem(SIDEBAR_KEY)
    if (mode === 'rail' || mode === 'full') return mode
    const legacy = storage.getItem(LEGACY_SIDEBAR_KEY)
    if (legacy !== null) {
      const migrated: SidebarMode = legacy === '1' ? 'rail' : 'full'
      storage.setItem(SIDEBAR_KEY, migrated)
      storage.removeItem(LEGACY_SIDEBAR_KEY)
      return migrated
    }
  } catch {
    /* no storage: full */
  }
  return 'full'
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

/**
 * The rail's avatars in the full sidebar's order (needs you, running,
 * exited), the members of one run kept together under a thin label, the
 * run's name from `runNames` or its crew's id. Within a section the
 * sessions of no run come first.
 */
export function railGroups(sessions: readonly SessionInfo[], runNames: Readonly<Record<string, string>> = {}): RailGroup[] {
  const g = groupSessions([...sessions])
  const out: RailGroup[] = []
  const item = (s: SessionInfo, dot: RailDot): RailItem => ({ id: s.id, name: s.name, agentId: s.agentId, dot, message: s.attention?.message || undefined })
  const section = (key: SessionGroupKey, list: SessionInfo[], dot: (s: SessionInfo) => RailDot) => {
    const loose = list.filter((s) => !s.crew)
    if (loose.length) out.push({ key: `${key}:`, items: loose.map((s) => item(s, dot(s))) })
    const byRun = new Map<string, SessionInfo[]>()
    for (const s of list) if (s.crew) byRun.set(s.crew.runId, [...(byRun.get(s.crew.runId) ?? []), s])
    for (const [runId, members] of byRun) out.push({ key: `${key}:${runId}`, runId, label: runNames[runId] || members[0]?.crew?.crewId, items: members.map((s) => item(s, dot(s))) })
  }
  section('needs', g.needs, () => 'needs')
  section('running', g.running, (s) => (s.status === 'running' ? 'running' : 'idle'))
  section('exited', g.exited, () => 'exited')
  return out
}
