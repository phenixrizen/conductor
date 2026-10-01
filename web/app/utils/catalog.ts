import type { AgentInfo } from '~/composables/useSessions'

/** What removing an agent on the Agents page does: hide a built-in or configured one, revert a saved change, or delete a saved addition. */
export type Removal = 'hide' | 'revert' | 'delete'

/** The removal DELETE /api/catalog/{id} makes of `a`, from where the catalog took it. */
export function removalOf(a: Pick<AgentInfo, 'source' | 'replaces'>): Removal {
  if (a.source !== 'saved') return 'hide'
  return a.replaces ? 'revert' : 'delete'
}

/** The words for a removal: its button, its dialog and the toast after it. */
export function removalText(r: Removal, name: string, id: string) {
  switch (r) {
    case 'revert':
      return {
        button: 'Revert',
        title: `Revert ${name}?`,
        description: 'Your changes go and the original definition comes back. Sessions already running keep going.',
        icon: 'i-lucide-undo-2',
        toast: { title: 'Change removed', description: `${id} is back to its original definition.` },
      }
    case 'delete':
      return {
        button: 'Delete',
        title: `Delete ${name}?`,
        description: 'The agent you added goes. Sessions already running keep going.',
        icon: 'i-lucide-trash-2',
        toast: { title: 'Removed', description: name },
      }
    default:
      return {
        button: 'Hide',
        title: `Hide ${name}?`,
        description: 'It leaves the agent list and the Launch dialog and is listed under Hidden, where Restore brings it back. Sessions already running keep going.',
        icon: 'i-lucide-eye-off',
        toast: { title: 'Hidden', description: `${name} can be restored from the Hidden list.` },
      }
  }
}
