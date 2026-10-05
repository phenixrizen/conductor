/**
 * The agent icons the workbench carries. It fetches no icon at runtime: the
 * client bundle holds the icons named in the app's sources and in nuxt.config,
 * which takes this list. It has the built-in catalog's icons
 * (internal/catalog/defaults.go; a Go test checks) and the example config's.
 * A catalog icon outside it would show nothing, so it shows
 * AGENT_ICON_FALLBACK instead.
 */
export const AGENT_ICONS: readonly string[] = [
  'i-lucide-sparkles',
  'i-lucide-code-xml',
  'i-lucide-rocket',
  'i-lucide-github',
  'i-lucide-mouse-pointer-2',
  'i-lucide-braces',
  'i-lucide-pi',
  'i-lucide-pi-square',
  'i-lucide-git-commit',
  'i-lucide-feather',
  'i-lucide-zap',
  'i-lucide-cpu',
  'i-lucide-terminal',
  'i-lucide-wrench',
  'i-lucide-bot',
]

/** The icon for an agent without one, or with one the workbench does not carry. */
export const AGENT_ICON_FALLBACK = 'i-lucide-bot'

/** `icon` when the workbench carries it, the generic agent icon otherwise. */
export function agentIcon(icon?: string): string {
  return icon && AGENT_ICONS.includes(icon) ? icon : AGENT_ICON_FALLBACK
}
