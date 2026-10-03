import type { AgentInfo, Identity } from '~/composables/useSessions'
import { agentIcon } from './agentIcons'

export type IdentityState = 'ok' | 'impostor' | 'unidentified' | 'pending' | 'failed' | 'unprobed'

/** What the identity probe says of an agent, for a badge: the state, its words, and a longer title. */
export function identity(a: Pick<AgentInfo, 'identity' | 'command'>, host: string): { state: IdentityState; label: string; title: string } {
  const id = a.identity
  const program = a.command[0] ?? ''
  if (!id) return { state: 'unprobed', label: '', title: '' }
  if (id.pending) return { state: 'pending', label: 'Checking the version…', title: `${program} --version is being run on ${where(host)}` }
  if (!id.ran) return { state: 'failed', label: 'Version check failed', title: id.error ?? `${program} could not be run on ${where(host)}` }
  if (id.identified) return { state: 'ok', label: `${id.name ?? 'Identified'}${id.version ? ` ${id.version}` : ''}`, title: `${program} on ${where(host)} is ${id.name ?? 'the agent'}${id.version ? ` ${id.version}` : ''}` }
  if (id.impostor) return { state: 'impostor', label: `Not ${id.name ?? 'the agent'}`, title: `${program} on ${where(host)} is another program: it printed ${quote(id.output)}` }
  if (id.verified) return { state: 'impostor', label: `Not ${id.name ?? 'the agent'}`, title: `${program} on ${where(host)} did not identify as ${id.name ?? 'the agent'}: it printed ${quote(id.output)}` }
  return { state: 'unidentified', label: 'Version unrecognised', title: `${program} on ${where(host)} printed ${quote(id.output)}, which Conductor does not know as ${id.name ?? 'the agent'}'s yet` }
}

function quote(s?: string): string {
  return s ? `"${s}"` : 'nothing'
}

/** Whether the probe says the program is not the agent (a known impostor, or a verified check that matched nothing): a crew with it is refused. */
export function misidentified(id?: Identity): boolean {
  return !!id && id.ran && (!!id.impostor || (id.verified && !id.identified))
}

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
  if (!isAvailable(a)) return { label: `${a.name} · not installed on ${where(host)}`, value: a.id, icon: 'i-lucide-circle-off' }
  if (misidentified(a.identity)) return { label: `${a.name} · not ${a.identity?.name ?? a.name} on ${where(host)}`, value: a.id, icon: 'i-lucide-circle-off' }
  return { label: a.name, value: a.id, icon: agentIcon(a.icon) }
}
