/** What a launch opens with (#303): Launch <agent> here, Launch another like this, the `launch` verb. */
export interface LaunchPrefill {
  agentId?: string
  cwd?: string
  args?: string
  yolo?: boolean
}

/** Open state of the Launch agent dialog, shared by the sidebar button, the N shortcut and the palette; `prefill` is taken as it opens. */
export function useLaunchModal() {
  const open = useState<boolean>('launchOpen', () => false)
  const prefill = useState<LaunchPrefill | null>('launchPrefill', () => null)
  return {
    open,
    prefill,
    show: (p?: LaunchPrefill) => {
      prefill.value = p ?? null
      open.value = true
    },
  }
}
