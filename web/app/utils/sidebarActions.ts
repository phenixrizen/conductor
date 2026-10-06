import type { DropdownMenuItem } from '@nuxt/ui'

/**
 * What a row of the sidebar offers without opening it (design 3c): the same
 * list behind More, the right-click menu, a long press on a phone, and the
 * keys. A session: Open, Open the run (a member), Share…, Show in the Yard,
 * Stop…; a run's header: Open run, Share run, Stop run…; a link shared with
 * you: Open, Forget.
 */
export interface RowActionTarget {
  kind: 'session' | 'run' | 'shared'
  name: string
  /** Running or starting: it can be stopped, and shared. */
  live: boolean
  /** A session that is a run's member: its run can be opened. */
  inRun?: boolean
}

export interface RowActionHandlers {
  open: () => void
  openRun?: () => void
  share?: () => void
  yard?: () => void
  stop?: () => void
  forget?: () => void
}

export function rowMenuItems(t: RowActionTarget, on: RowActionHandlers): DropdownMenuItem[][] {
  if (t.kind === 'shared') {
    return [[{ label: 'Open', icon: 'i-lucide-external-link', kbds: ['enter'], onSelect: on.open }], [{ label: 'Forget', icon: 'i-lucide-x', onSelect: on.forget }]]
  }
  if (t.kind === 'run') {
    return [
      [{ label: 'Open run', icon: 'i-lucide-play', kbds: ['enter'], onSelect: on.open }],
      [{ label: 'Share run', icon: 'i-lucide-share-2', kbds: ['S'], disabled: !t.live, onSelect: on.share }],
      [{ label: 'Stop run…', icon: 'i-lucide-square', kbds: ['X'], color: 'error', disabled: !t.live, onSelect: on.stop }],
    ]
  }
  const first: DropdownMenuItem[] = [{ label: 'Open', icon: 'i-lucide-terminal', kbds: ['enter'], onSelect: on.open }]
  if (t.inRun) first.push({ label: 'Open the run', icon: 'i-lucide-play', kbds: ['R'], onSelect: on.openRun })
  return [
    first,
    [
      { label: 'Share…', icon: 'i-lucide-share-2', kbds: ['S'], disabled: !t.live, onSelect: on.share },
      { label: 'Show in the Yard', icon: 'i-lucide-layout-grid', onSelect: on.yard },
    ],
    [{ label: 'Stop…', icon: 'i-lucide-square', kbds: ['X'], color: 'error', disabled: !t.live, onSelect: on.stop }],
  ]
}

/** The question a row asks before it stops something, in the row itself. */
export function stopQuestion(t: Pick<RowActionTarget, 'kind' | 'name'>): { title: string; detail: string } {
  if (t.kind === 'run') return { title: `Stop ${t.name}?`, detail: "Every member's terminal closes. The run can be resumed as a new run." }
  return { title: `Stop ${t.name}?`, detail: 'Its terminal closes. Resume brings the conversation back from Exited.' }
}

/** Whether a session in this state can be stopped (and so shared). */
export function sessionLive(status: string): boolean {
  return status === 'running' || status === 'starting'
}
