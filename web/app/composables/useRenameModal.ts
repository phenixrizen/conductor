/** The session the Rename dialog is open on (#318): its id, and its name as the dialog opened. */
export interface RenameTarget {
  id: string
  name: string
}

/** Open state of the Rename dialog, shared by a sidebar row's menu, the session page and the palette: one dialog, in the layout. */
export function useRenameModal() {
  const open = useState<boolean>('renameOpen', () => false)
  const target = useState<RenameTarget | null>('renameTarget', () => null)
  return {
    open,
    target,
    show: (s: RenameTarget) => {
      target.value = { id: s.id, name: s.name }
      open.value = true
    },
  }
}
