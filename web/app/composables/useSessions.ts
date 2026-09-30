import type { Attention, AttentionKind, AttentionOption, Role } from '~/utils/protocol'

export type SessionKind = 'server' | 'hosted'
export type SessionStatus = 'starting' | 'running' | 'exited' | 'stopped' | 'host_disconnected'

export interface SessionInfo {
  id: string
  name: string
  kind: SessionKind
  agentId: string
  command: string[]
  cwd: string
  status: SessionStatus
  exitCode?: number
  cols: number
  rows: number
  viewers: number
  hostName?: string
  /** OS user running `conductor host` (hosted sessions). */
  hostUser?: string
  /** Git branch of the working directory when known. */
  branch?: string
  attention?: Attention
  /** Who last answered a needs-input prompt (server-reported). */
  lastAnswer?: { by?: string; byName: string; at: string; message?: string }
  createdAt: string
  endedAt?: string
}

/** How an agent tells Conductor it needs a human. No signal means the bell default. */
export interface AgentSignal {
  kind: 'hook' | 'bell' | 'pattern' | 'none'
  /** RE2 on the last screen line; only with kind 'pattern'. */
  pattern?: string
  /** Ask hook adapters to report tool use as events (chatty; off by default). */
  toolEvents?: boolean
}

export interface AgentInfo {
  id: string
  name: string
  description?: string
  command: string[]
  allowArgs: boolean
  cwd?: string
  icon?: string
  /** Hook adapter id; empty or missing means none. */
  adapter?: string
  signal?: AgentSignal
  /** Names of server environment variables this agent may inherit. */
  envPassthrough?: string[]
  /** Variable names the server sets for this agent. Every value is masked as "***": it means "set on the server", never the real value. Send it back unchanged to keep the stored value. */
  env?: Record<string, string>
}

/** Body of POST /api/catalog: the whole agent, replacing any agent with the same id. */
export interface AgentInput {
  id: string
  name: string
  description?: string
  command: string[]
  allowArgs: boolean
  /** A value of "***", as read from the catalog, keeps the value stored on the server for that key; the server rejects it for a key the agent does not have. */
  env?: Record<string, string>
  envPassthrough?: string[]
  cwd?: string
  icon?: string
  adapter?: string
  signal?: AgentSignal
}

/** Body of POST /api/sessions/{id}/events: something the agent did, or an attention word. */
export interface EventInput {
  type: 'progress' | 'artifact' | 'handoff' | 'tool_use' | 'tool_denied' | 'error' | 'needs_input' | 'working' | 'done' | 'clear'
  message?: string
  /** `artifact`: where the result lives (≤ 2048 bytes). */
  url?: string
  /** `handoff`: who the work goes to (≤ 40 characters). */
  to?: string
  /** `tool_use`, `tool_denied`, `error`: the tool involved (≤ 100 bytes). */
  tool?: string
  /** With the attention words: the shape of the prompt and its quick replies. */
  kind?: AttentionKind
  options?: AttentionOption[]
}

/** One hook adapter as GET /api/integrations lists it, checked against the home of the user running the server. */
export interface Integration {
  id: string
  name: string
  /** What the adapter can report, such as needs_input, done or tool_use. */
  events: string[]
  /** A launch from this server wires the hooks in (flags or environment). */
  launchInjection: boolean
  /** The agent reads skills: installing its hooks also puts the Conductor skill in its skills directory. */
  installsSkill: boolean
  /** The hooks are in place as an install would leave them: false after an upgrade moved the binary, until installed again. */
  installed: boolean
  /** The install's main file; empty when there is no file to install into. */
  where: string
  /** What to paste to wire the agent by hand. */
  snippet: string
  /** The agent's hook interface is still changing (a developer preview). */
  experimental: boolean
}

/** A webhook from conductor.json, as GET /api/integrations lists it: never its secret, user info or query string. */
export interface WebhookInfo {
  /** scheme://host[:port]/path */
  url: string
  /** The event types the server POSTs to it. */
  events: string[]
}

/** GET /api/integrations: every hook adapter, the machine the server runs on, and the webhooks it sends events to. */
export interface Integrations {
  integrations: Integration[]
  /** The server's host name, where Install on this machine writes; empty when the server cannot tell. */
  host: string
  webhooks: WebhookInfo[]
}

export interface ShareLink {
  id: string
  sessionId: string
  label?: string
  role: Role
  createdAt: string
  expiresAt?: string
  revoked: boolean
  /** Viewers currently attached through this link (server-reported). */
  active?: number
}

export interface JoinInfo {
  session: Pick<SessionInfo, 'id' | 'name' | 'agentId' | 'kind' | 'status' | 'cols' | 'rows' | 'hostName' | 'hostUser'>
  role: Role
  label?: string
}

export function useSessions() {
  const { request } = useApi()

  return {
    list: () => request<{ sessions: SessionInfo[] }>('/api/sessions').then((r) => r.sessions ?? []),
    get: (id: string, token?: string) => request<{ session: SessionInfo; role: Role; links?: ShareLink[] }>(`/api/sessions/${encodeURIComponent(id)}`, { token }),
    create: (body: { agentId: string; name?: string; cwd?: string; args?: string[]; cols?: number; rows?: number }) =>
      request<SessionInfo>('/api/sessions', { method: 'POST', body }),
    stop: (id: string) => request<SessionInfo | void>(`/api/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    catalog: () => request<{ agents: AgentInfo[] }>('/api/catalog').then((r) => r.agents ?? []),
    /** The launchable agents plus the ids hidden from the catalog, which `unhideAgent` brings back. */
    catalogWithHidden: () =>
      request<{ agents: AgentInfo[]; hidden?: string[] }>('/api/catalog').then((r) => ({ agents: r.agents ?? [], hidden: r.hidden ?? [] })),
    /** Adds an agent or replaces the one with the same id (a built-in too); the server saves it in its data directory. */
    saveAgent: (a: AgentInput) => request<{ agent: AgentInfo }>('/api/catalog', { method: 'POST', body: a }).then((r) => r.agent),
    /** Removes the saved override with this id, restoring a built-in it replaced; an agent without one is hidden instead. */
    deleteAgent: (id: string) => request<void>(`/api/catalog/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    /** Takes a hidden id off the hidden list and returns its agent, back as it was; undefined when no agent has that id any more. */
    unhideAgent: (id: string) =>
      request<{ agent?: AgentInfo }>(`/api/catalog/${encodeURIComponent(id)}/unhide`, { method: 'POST' }).then((r) => r.agent),
    /** Whether the program (argv[0]) resolves on the server. Nothing is run. */
    checkCommand: (argv: string[]) =>
      request<{ found: boolean; path?: string }>('/api/catalog/check', { method: 'POST', body: { command: argv } }),
    /** OS user running the server; the default display name for admins. */
    whoami: () => request<{ user: string }>('/api/whoami'),
    /** Every hook adapter, in a stable order, with its install checked in the server user's home, the server's host name, and its webhooks. */
    integrations: () =>
      request<Partial<Integrations>>('/api/integrations').then(
        (r): Integrations => ({ integrations: r.integrations ?? [], host: r.host ?? '', webhooks: r.webhooks ?? [] }),
      ),
    /**
     * Installs an adapter's hooks into the server user's home and returns the files it changed, [] when they were in place.
     * 400 `no_file_route`: nothing to install, or a step left to do by hand with the adapter's `snippet` (files changed before it stay changed);
     * 500 `install_failed`.
     */
    installIntegration: (id: string) =>
      request<{ changed: string[] }>(`/api/integrations/${encodeURIComponent(id)}/install`, { method: 'POST' }).then((r) => r.changed ?? []),
    links: (id: string) => request<{ links: ShareLink[] }>(`/api/sessions/${encodeURIComponent(id)}/links`).then((r) => r.links ?? []),
    createLink: (id: string, body: { role: Role; label?: string; ttlSeconds?: number }) =>
      request<{ link: ShareLink; token: string; url: string }>(`/api/sessions/${encodeURIComponent(id)}/links`, { method: 'POST', body }),
    revokeLink: (id: string, linkId: string) =>
      request<void>(`/api/sessions/${encodeURIComponent(id)}/links/${encodeURIComponent(linkId)}`, { method: 'DELETE' }),
    join: (token: string) => request<JoinInfo>(`/api/join/${encodeURIComponent(token)}`, { token }),
    /**
     * Sets a session's attention state as the admin. 429 `rate_limited` means the session's limit of 20 reports a second, 40 at once, which its
     * events share, is used up: nothing changed.
     */
    setAttention: (id: string, state: 'needs_input' | 'working' | 'done' | 'clear', message?: string) =>
      request<{ attention: Attention }>(`/api/sessions/${encodeURIComponent(id)}/attention`, { method: 'POST', body: { state, message } }),
    /**
     * Reports an event for a session. 202 means recorded, or on its way to the host of a hosted session (the host's own limit may still drop it).
     * 429 `rate_limited` means the session's limit of 20 reports a second, 40 at once, is used up (events and attention words alike: a server
     * session's own, or for a hosted session what the server sends on to the host), or that this client has been refused too often.
     * 409 `host_disconnected` means a hosted session has no host connected.
     */
    sendEvent: (id: string, body: EventInput) =>
      request<{ accepted: boolean }>(`/api/sessions/${encodeURIComponent(id)}/events`, { method: 'POST', body }),
  }
}

export function statusColor(status: SessionStatus): 'success' | 'neutral' | 'warning' | 'error' | 'info' {
  switch (status) {
    case 'running':
      return 'success'
    case 'starting':
      return 'info'
    case 'host_disconnected':
      return 'warning'
    case 'exited':
      return 'neutral'
    case 'stopped':
      return 'neutral'
  }
  return 'neutral'
}

export function statusLabel(status: SessionStatus): string {
  return status.replace('_', ' ')
}
