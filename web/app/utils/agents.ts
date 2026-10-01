import type { AgentInfo } from '~/composables/useSessions'
import { agentIcon } from './agentIcons'

/** Whether the server can launch the agent now. A server that does not say (older) is taken to mean yes. */
export function isAvailable(a: Pick<AgentInfo, 'available'>): boolean {
  return a.available !== false
}

/** Where an agent is not installed: the server's host name as the server gives it, or "the server" when it is unknown. */
function where(host: string): string {
  return host || 'the server'
}

/** The note on an agent whose program the server did not find; host is the server's host name, or empty. */
export function notInstalled(host: string): string {
  return `Not installed on ${where(host)}`
}

/** The note's tooltip: how the server judged it. A bare name is looked up on the server's PATH, where any program of that name counts. */
export function notInstalledTitle(program: string): string {
  if (program.includes('/')) return `${program} was not found on the server`
  return `No program named ${program} is on the server's PATH (any program of that name counts as installed)`
}

/** The agents the Launch dialog's server tab offers. */
export function serverAgents<T extends Pick<AgentInfo, 'available'>>(list: readonly T[]): T[] {
  return list.filter(isAvailable)
}

/** A select item for an agent, marked when it is not installed; never disabled, since a crew may be edited before its agents are installed. */
export function agentItem(a: AgentInfo, host: string): { label: string; value: string; icon: string } {
  if (isAvailable(a)) return { label: a.name, value: a.id, icon: agentIcon(a.icon) }
  return { label: `${a.name} · not installed on ${where(host)}`, value: a.id, icon: 'i-lucide-circle-off' }
}
