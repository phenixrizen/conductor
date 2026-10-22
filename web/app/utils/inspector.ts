// The inspector rail (design 4h, #301): one vertical rail on the inspector's
// outer edge replaces the two tab rows (People · Files · Activity · Chat,
// then Explorer · Changes · Commits · Touched). Its items, their count
// pills and their tooltips are pure, as `utils/sidebar.ts` backs the
// sidebar's rail; Touched is an Activity filter now (useTouched).

export type InspectorView = 'people' | 'explorer' | 'changes' | 'commits' | 'activity' | 'chat'
/** The Files pane's views, the three the Yard's aside and the join page use alone. */
export type FilesView = 'explorer' | 'changes' | 'commits'

export interface InspectorItem {
  id: InspectorView
  label: string
  icon: string
  /** The count pill, neutral: viewers, changed files, commits, unread chat; none on Explorer and Activity. */
  count?: number
  /** The tooltip's second part: the view's own sub-line, as its head row shows it. */
  sub?: string
}

/** A pill's text: nothing for none, `99+` past the cap. */
export function pillText(n: number | undefined): string {
  if (!n || n <= 0) return ''
  return n > 99 ? '99+' : String(n)
}

/** The tooltip, the view named and its count or sub-line: "Commits · 9 on feat/scoping-suggestions", "Chat · 2 unread", "Explorer". */
export function itemTooltip(it: InspectorItem): string {
  if (it.sub) return `${it.label} · ${it.sub}`
  if (it.count) return `${it.label} · ${it.count}${it.id === 'chat' ? ' unread' : it.id === 'people' ? (it.count === 1 ? ' here' : ' here') : ''}`
  return it.label
}

export interface InspectorCounts {
  viewers: number
  changes: number
  commits: number
  unread: number
  /** The chat item shows once the owner said it takes chat. */
  chat: boolean
  /** The sub-lines the views compute, for the tooltips. */
  subs?: Partial<Record<InspectorView, string>>
}

/** The rail's items in order: People, Explorer, Changes, Commits, Activity, Chat (when capable). */
export function inspectorItems(c: InspectorCounts): InspectorItem[] {
  const sub = (id: InspectorView) => c.subs?.[id]
  const items: InspectorItem[] = [
    { id: 'people', label: 'People', icon: 'i-lucide-users', count: c.viewers, sub: sub('people') },
    { id: 'explorer', label: 'Explorer', icon: 'i-lucide-folder', sub: sub('explorer') },
    { id: 'changes', label: 'Changes', icon: 'i-lucide-file-diff', count: c.changes, sub: sub('changes') },
    { id: 'commits', label: 'Commits', icon: 'i-lucide-git-commit-horizontal', count: c.commits, sub: sub('commits') },
    { id: 'activity', label: 'Activity', icon: 'i-lucide-activity', sub: sub('activity') },
  ]
  if (c.chat) items.push({ id: 'chat', label: 'Chat', icon: 'i-lucide-message-circle', count: c.unread, sub: sub('chat') })
  return items
}

/** The Files pane's three items, for an aside without People, Activity or Chat. */
export function filesItems(c: Pick<InspectorCounts, 'changes' | 'commits' | 'subs'>): InspectorItem[] {
  return inspectorItems({ ...c, viewers: 0, unread: 0, chat: false }).filter((it): it is InspectorItem & { id: FilesView } => it.id === 'explorer' || it.id === 'changes' || it.id === 'commits')
}

/**
 * The git polling cadence (the one behaviour change of 4h): the status every
 * 5 s while Changes shows, the log every 10 s while Commits shows, each every
 * 30 s otherwise so the rail's pills stay right while the panel shows another
 * view or is collapsed; nothing while the document is hidden (the caller
 * checks visibility on each tick).
 */
export const POLL_SHOWN = { changes: 5000, commits: 10_000 } as const
export const POLL_IDLE = 30_000

export function pollInterval(kind: 'changes' | 'commits', shown: InspectorView | null): number {
  return shown === kind ? POLL_SHOWN[kind] : POLL_IDLE
}

/** What a click on a rail item does: the active one, shown, collapses the panel; any other opens the panel on its view. */
export function pickItem(id: InspectorView, view: InspectorView, open: boolean, collapsible = true): { view: InspectorView; open: boolean } {
  if (open && view === id && collapsible) return { view, open: false }
  return { view: id, open: true }
}
