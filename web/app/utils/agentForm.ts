import type { AgentInfo, AgentInput, AgentSignal } from '~/composables/useSessions'
import { hasOpenQuote, splitArgs } from './argv'

/** What GET /api/catalog shows in place of every stored env value (catalog.RedactedValue). Sent back, it keeps the stored value. */
export const MASK = '***'

/** The server's rule for an agent ID: idPattern in internal/catalog/catalog.go. */
export const AGENT_ID_PATTERN = /^[a-z0-9-]{1,32}$/

export type SignalKind = AgentSignal['kind']

/** One row of the environment editor. A `masked` row is a value stored on the server, which the form never sees. */
export interface EnvRow {
  uid: number
  key: string
  value: string
  masked: boolean
}

/** The add-agent form. `pendingCommand` is what is typed in the command field and not yet an argument. */
export interface AgentForm {
  name: string
  id: string
  command: string[]
  pendingCommand: string
  description: string
  /** The agent's website: an https:// address, or empty. */
  site: string
  env: EnvRow[]
  allowArgs: boolean
  signal: SignalKind
  pattern: string
}

export type Field = 'name' | 'id' | 'command' | 'pattern' | 'env' | 'site'

/** The form for `a`, or an empty one. Stored env values come in masked, by name; passthrough names follow as rows without a value. */
export function formFromAgent(a: AgentInfo | undefined, uid: () => number): AgentForm {
  return {
    name: a?.name ?? '',
    id: a?.id ?? '',
    command: [...(a?.command ?? [])],
    pendingCommand: '',
    description: a?.description ?? '',
    site: a?.site ?? '',
    env: [
      ...Object.keys(a?.env ?? {})
        .sort()
        .map((key) => ({ uid: uid(), key, value: '', masked: true })),
      ...(a?.envPassthrough ?? []).map((key) => ({ uid: uid(), key, value: '', masked: false })),
    ],
    allowArgs: a?.allowArgs ?? true,
    signal: a?.signal?.kind ?? 'bell',
    pattern: a?.signal?.pattern ?? '',
  }
}

/** The argv the form holds: its arguments, then what is typed and not yet one. */
export function commandOf(f: Pick<AgentForm, 'command' | 'pendingCommand'>): string[] {
  return [...f.command, ...splitArgs(f.pendingCommand)]
}

/** The server's rule for an agent's site (validateSite in internal/catalog), in words: '' when empty or an https URL with a host, a port from 1 to 65535 if any, and no user info. */
export function siteError(site: string): string {
  const s = site.trim()
  if (!s) return ''
  try {
    const u = new URL(s)
    if (u.protocol === 'https:' && u.hostname && u.port !== '0' && !u.username && !u.password && s.length <= 200 && !/\s/.test(s)) return ''
  } catch {
    /* not a URL */
  }
  return 'An https:// address, or nothing'
}

/** What is wrong with the form, by field; empty when it can be saved. */
export function formErrors(f: AgentForm): Partial<Record<Field, string>> {
  const e: Partial<Record<Field, string>> = {}
  if (!f.name.trim()) e.name = 'Give the agent a name'
  if (!AGENT_ID_PATTERN.test(f.id)) e.id = f.id ? 'Use lowercase letters, digits and dashes, up to 32' : 'An ID is required'
  if (hasOpenQuote(f.pendingCommand)) e.command = 'Close the quote, or remove it'
  else if (!commandOf(f)[0]?.trim()) e.command = 'Add the command to run'
  // Spaces count in a regex (a "> " prompt), so trim only to tell whether anything was typed.
  if (f.signal === 'pattern' && !f.pattern.trim()) e.pattern = 'Enter the pattern to look for'
  const site = siteError(f.site)
  if (site) e.site = site
  const seen = new Set<string>()
  for (const r of f.env) {
    const key = r.key.trim()
    if (!key) {
      if (r.value) e.env = 'Give every variable a name'
      continue
    }
    if (seen.has(key)) e.env = `${key} is listed twice`
    seen.add(key)
    if (!r.masked && r.value === MASK) e.env = `${key}: ${MASK} stands for a stored value; type the real value`
  }
  return e
}

/** The signal to save: the bell is what an agent without a signal gets, so it is left out unless the agent had one. The tool-events flag, which the form has no control for, stays. */
export function signalOut(kind: SignalKind, pattern: string, prev?: AgentSignal): AgentSignal | undefined {
  const toolEvents = prev?.toolEvents || undefined
  switch (kind) {
    case 'pattern':
      return { kind: 'pattern', pattern, toolEvents }
    case 'hook':
      return { kind: 'hook', toolEvents }
    case 'none':
      return { kind: 'none', toolEvents }
    default:
      return prev ? { kind: 'bell', toolEvents } : undefined
  }
}

/** The body of POST /api/catalog. A masked row sends the mask, which keeps the stored value; a row without a value is a passthrough name. What the form has no control for comes from `prev`, the agent edited. */
export function agentPayload(f: AgentForm, prev?: AgentInfo): AgentInput {
  const env: Record<string, string> = {}
  const passthrough: string[] = []
  for (const row of f.env) {
    const key = row.key.trim()
    if (!key) continue
    if (row.masked) env[key] = MASK
    else if (row.value !== '') env[key] = row.value
    else passthrough.push(key)
  }
  return {
    id: f.id,
    name: f.name.trim(),
    description: f.description.trim() || undefined,
    site: f.site.trim() || undefined,
    command: commandOf(f),
    allowArgs: f.allowArgs,
    env: Object.keys(env).length ? env : undefined,
    envPassthrough: passthrough.length ? passthrough : undefined,
    cwd: prev?.cwd,
    icon: prev?.icon,
    adapter: prev?.adapter,
    signal: signalOut(f.signal, f.pattern, prev?.signal),
  }
}
