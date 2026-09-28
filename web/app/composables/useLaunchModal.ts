/** Open state of the Launch agent dialog, shared by the sidebar button and the N shortcut. */
export function useLaunchModal() {
  const open = useState<boolean>('launchOpen', () => false)
  return { open, show: () => (open.value = true) }
}
