import type { ActivityEntry, Attention, AttentionKind, AttentionOption, Role } from '~/utils/protocol'
import { toCrewInput, toCrewMember } from '~/utils/crews'

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
  /** The crew run the session is a member of; missing for a session launched on its own. */
  crew?: { runId: string; crewId: string; member: string }
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
  /** Where the catalog took the agent from: built in, the config file (or catalog file), or saved from the Agents page. */
  source?: 'built-in' | 'config' | 'saved'
  /** For a saved agent that replaces a built-in or configured one: where that one came from. Deleting the saved agent brings it back. */
  replaces?: 'built-in' | 'config'
  /** Whether command[0] resolves on the server (the check of POST /api/catalog/check, cached 30 s). Missing from an older server. */
  available?: boolean
  /** The agent's website, an https URL, when known: a built-in's, or what was saved with the agent. */
  site?: string
}

/** Body of POST /api/catalog: the whole agent, replacing any agent with the same id. */
export interface AgentInput {
  id: string
  name: string
  description?: string
  command: string[]
  allowArgs: boolean
  /**
   * A value of "***", as read from the catalog, means "unchanged": the value the saved agent holds for that key or, for an agent that
   * replaces a built-in or configured one, the replaced agent's value, which then follows the config. The server rejects it for a key
   * neither has.
   */
  env?: Record<string, string>
  envPassthrough?: string[]
  cwd?: string
  icon?: string
  /** An https:// URL with a host, at most 200 bytes; left out, an agent that replaces a built-in keeps the built-in's. */
  site?: string
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
  /** The adapter has a file Conductor can install its hooks into. */
  installable: boolean
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
  /** The session a session link opens; empty for a run link. */
  sessionId: string
  /** The crew run a run link opens: its role on the session of every member of the run, a member added later included. */
  runId?: string
  label?: string
  role: Role
  createdAt: string
  expiresAt?: string
  revoked: boolean
  /** Viewers currently attached through this link (server-reported). */
  active?: number
}

/** A member of a run as the join page sees it. */
export interface JoinRunMember {
  name: string
  /** Its session, only while that runs; `agentId` and `status` are then the session's. */
  sessionId?: string
  agentId: string
  /** Without a session: its state in the run (`pending`, `starting` until its session exists, `ended`). */
  status: 'pending' | 'starting' | 'running' | 'ended'
}

export interface JoinInfo {
  /** The session a session link opens; missing for a run link, whose reply has `run` in its place. */
  session?: Pick<SessionInfo, 'id' | 'name' | 'agentId' | 'kind' | 'status' | 'cols' | 'rows' | 'hostName' | 'hostUser'>
  /** The run a run link opens, with every member in the run's order. */
  run?: { id: string; name: string; members: JoinRunMember[] }
  role: Role
  label?: string
}

/** Why a broadcast skipped a member: waiting on a prompt, not running (no session yet, its prompt not typed yet, or ended), or not a member. */
export type BroadcastSkipReason = 'needs_input' | 'not_running' | 'unknown'

/** Reply of POST /api/runs/{run}/broadcast, both lists in the order asked. */
export interface BroadcastResult {
  sent: string[]
  skipped: { member: string; reason: BroadcastSkipReason }[]
}

/** When a crew member starts in a run. */
export interface CrewStart {
  when: 'immediately' | 'after' | 'manual'
  /** Only with `after`: the member whose first `done` starts this one. Following these never goes round in a cycle. */
  member?: string
}

/** One agent of a crew. */
export interface CrewMember {
  /** `^[a-z0-9][a-z0-9._-]{0,39}$` without `..` or a `.` or `.lock` at the end, unique in the crew: it becomes a branch and a worktree name. */
  name: string
  /** A catalog agent id (`^[a-z0-9-]{1,32}$`) the catalog has when the crew is saved. */
  agentId: string
  /** Typed once the member is ready; `$GOAL` and `${GOAL}` stand for the crew's goal. At most 4000 characters. */
  prompt: string
  /** At most 32, of at most 4096 bytes each and 8 KiB in all. */
  args?: string[]
  start: CrewStart
}

/** A saved crew in full, as GET /api/crews/{id} answers it and the crew routes reply. */
export interface CrewInfo {
  /** Set by the server from the name when the crew is created; never changes. */
  id: string
  /** At most 60 characters, no control characters; the server trims surrounding space. */
  name: string
  /** At most 2000 characters. */
  goal: string
  cwd: string
  where: 'server' | 'host'
  /** `worktree`: every member gets a git worktree of `cwd`. */
  isolation: 'none' | 'worktree'
  openAfterLaunch: boolean
  /** Lifetime of the view link a launch creates; none when missing. */
  viewLinkTtlSeconds?: number
  /** At most 12. The whole crew is at most 1 MiB as its file. */
  members: CrewMember[]
  createdAt: string
  updatedAt: string
}

/** A crew as GET /api/crews lists it: what the list shows, without the prompts. GET /api/crews/{id} answers it in full. */
export interface CrewSummary {
  id: string
  name: string
  cwd: string
  where: 'server' | 'host'
  isolation: 'none' | 'worktree'
  members: Array<{ name: string; agentId: string }>
  updatedAt: string
}

/** Body of POST /api/crews and PUT /api/crews/{id}: a crew without what the server sets. The server rejects unknown fields, these three included. */
export type CrewInput = Omit<CrewInfo, 'id' | 'createdAt' | 'updatedAt'>

/** A member of a crew run, as the run routes report it. */
export interface RunMember {
  name: string
  agentId: string
  start: CrewStart
  /** The member's session, once it has started. */
  sessionId?: string
  /** With worktree isolation, once it has started: its branch (`crew/<run>/<member>`) and worktree. */
  branch?: string
  worktree?: string
  /** `starting` once its session exists, until its prompt is typed; `running` after. */
  status: 'pending' | 'starting' | 'running' | 'ended'
  startedAt?: string
  endedAt?: string
  /** Why it ended before it ran: it could not start, or its process ended before its prompt was typed. */
  error?: string
  /**
   * GET /api/runs/{run} only: lines of tracked files its worktree adds and removes against the commit it began from,
   * committed or not (`git diff --shortstat <base>`; untracked files do not count, a branch merged into its own does).
   * Read at most every 10 s; the last value stays when a read fails.
   */
  diff?: { added: number; removed: number }
}

/** A launch of a crew. Runs live in the server's memory: a restart forgets them. */
export interface RunInfo {
  /** `<crew id>-<8 hex>`. */
  id: string
  crewId: string
  name: string
  goal: string
  cwd: string
  isolation: 'none' | 'worktree'
  startedAt: string
  stoppedAt?: string
  members: RunMember[]
  /** The run's own log, oldest first, at most 200 entries: launched, member started, prompt typed, run links created and revoked, stopped. */
  log: ActivityEntry[]
}

export interface PathGit {
  repo: boolean
  commits: boolean
}
/** One directory GET /api/paths lists: its name, its full path, and whether it is a git repository with a commit. */
export interface PathEntry {
  name: string
  path: string
  git: PathGit
}
export interface PathsReply {
  dir: string
  entries: PathEntry[]
  truncated: boolean
}
/** What GET /api/git/check says of a working directory, by the launch's rules for worktrees. */
export interface GitCheck {
  inRepo: boolean
  toplevel?: string
  hasCommit: boolean
  message: string
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
    /** Whether the program resolves on the server. Only the program is sent; nothing is run. */
    checkCommand: (program: string) =>
      request<{ found: boolean; path?: string }>('/api/catalog/check', { method: 'POST', body: { command: [program] } }),
    /** One page of the saved crews' summaries, ordered by name, and how many there are in all. `limit` is 1 to 500. */
    listCrews: (offset = 0, limit = 100) =>
      request<{ crews: CrewSummary[]; total: number }>('/api/crews', { query: { offset: String(offset), limit: String(limit) } }).then((r) => ({ crews: r.crews ?? [], total: r.total ?? 0 })),
    /** One crew in full, for the editor. 404 for an unknown id; 409 `crew_unreadable` for a file the server cannot use, with why. */
    getCrew: (id: string) => request<{ crew: CrewInfo }>(`/api/crews/${encodeURIComponent(id)}`).then((r) => r.crew),
    /**
     * Without an id, creates a crew: the server derives its id from the name (then `-2`, `-3`… when taken). With an id, replaces that crew's
     * fields; its id and createdAt stay. Only CrewInput's fields are sent, so a CrewInfo read with getCrew can be passed as it is.
     * 400 `invalid_crew` says what is wrong, an agent the catalog does not have included; 404 for an unknown id; 409 `crew_unreadable` for a
     * crew whose file the server cannot use.
     */
    saveCrew: (crew: CrewInput, id?: string) =>
      (id === undefined
        ? request<{ crew: CrewInfo }>('/api/crews', { method: 'POST', body: toCrewInput(crew) })
        : request<{ crew: CrewInfo }>(`/api/crews/${encodeURIComponent(id)}`, { method: 'PUT', body: toCrewInput(crew) })
      ).then((r) => r.crew),
    deleteCrew: (id: string) => request<void>(`/api/crews/${encodeURIComponent(id)}`, { method: 'DELETE' }),
    /** Saves a copy under `<id>-copy` (then `-copy-2`…), named "<name> copy". 400 `invalid_crew` when one of its agents is no longer in the catalog. */
    duplicateCrew: (id: string) =>
      request<{ crew: CrewInfo }>(`/api/crews/${encodeURIComponent(id)}/duplicate`, { method: 'POST' }).then((r) => r.crew),
    /**
     * Launches a saved crew. Resolves once the session of every member that starts immediately exists (the member `starting`);
     * each prompt is typed once its session is ready, and a member whose prompt cannot be typed ends alone.
     * 400 `invalid_crew` (no members, a hosted crew, an agent the catalog lacks or one given arguments it does not take, a symlinked
     * `.conductor`), `invalid_cwd`; 409 `not_a_repo` for worktree isolation outside a git working tree or in one without a commit,
     * `run_stopped` when the run is stopped while it launches; a member's session errors as POST /api/sessions; 500 `launch_failed`.
     * A crew with `viewLinkTtlSeconds` also gets a view link of the run, in `viewLink`: its token is in this reply only, to show once.
     */
    launchCrew: (id: string) => request<{ run: RunInfo; viewLink?: { link: ShareLink; token: string; url: string } }>(`/api/crews/${encodeURIComponent(id)}/launch`, { method: 'POST' }),
    /** Every run in the server's memory, newest first. */
    listRuns: () => request<{ runs: RunInfo[] }>('/api/runs').then((r) => r.runs ?? []),
    /** One run, with each worktree member's diff. */
    getRun: (id: string) => request<{ run: RunInfo }>(`/api/runs/${encodeURIComponent(id)}`).then((r) => r.run),
    /**
     * Adds a member mid-run; one that starts immediately has its session when this resolves. 400 `invalid_crew` (a name the run has,
     * one whose start failed included), 409 `run_stopped`; a session that cannot be created leaves the member in the run, ended.
     */
    addRunMember: (runId: string, member: CrewMember) =>
      request<{ run: RunInfo }>(`/api/runs/${encodeURIComponent(runId)}/members`, { method: 'POST', body: toCrewMember(member) }).then((r) => r.run),
    /** Starts a pending member by hand, whatever its start condition; resolves once its session exists. 409 `member_started` or `run_stopped`. */
    startRunMember: (runId: string, name: string) =>
      request<{ run: RunInfo }>(`/api/runs/${encodeURIComponent(runId)}/members/${encodeURIComponent(name)}/start`, { method: 'POST' }).then((r) => r.run),
    /** Stops every member's session; the worktrees stay. */
    stopRun: (runId: string) => request<{ run: RunInfo }>(`/api/runs/${encodeURIComponent(runId)}/stop`, { method: 'POST' }).then((r) => r.run),
    /**
     * Types `text` and a carriage return into the named members, or every member when `members` is empty or missing, recorded as input by
     * `byName`. Line breaks and tabs become spaces and other control characters go; 400 `invalid_request` when nothing is left or it is over
     * 4096 bytes. A member waiting on a prompt is skipped, never typed into.
     */
    broadcastRun: (runId: string, body: { text: string; members?: string[]; byName?: string }) =>
      request<BroadcastResult>(`/api/runs/${encodeURIComponent(runId)}/broadcast`, { method: 'POST', body }),
    /** A run's share links, each with the viewers attached through it to the sessions of its members. */
    listRunLinks: (runId: string) => request<{ links: ShareLink[] }>(`/api/runs/${encodeURIComponent(runId)}/links`).then((r) => r.links ?? []),
    /** Creates a run link: its role on the session of every member of the run, those added later included. The token is shown this once. */
    createRunLink: (runId: string, body: { role: Role; label?: string; ttlSeconds?: number }) =>
      request<{ link: ShareLink; token: string; url: string }>(`/api/runs/${encodeURIComponent(runId)}/links`, { method: 'POST', body }),
    /** Revokes a run link, closing every viewer attached through it. */
    revokeRunLink: (runId: string, linkId: string) =>
      request<void>(`/api/runs/${encodeURIComponent(runId)}/links/${encodeURIComponent(linkId)}`, { method: 'DELETE' }),
    /** OS user running the server; the default display name for admins. */
    whoami: () => request<{ user: string }>('/api/whoami'),
    /**
     * The child directories of the longest existing directory in `prefix`, under the allowed roots (symlinks resolved), at most 50 of the
     * first 2000 entries read, each marked when it is a git repository (`repo`) with a commit (`commits`). Hidden directories show once
     * the typed element starts with a dot. 400 `invalid_cwd` when no part of the prefix is under a root.
     */
    listPaths: (prefix: string, limit = 50) => request<PathsReply>('/api/paths', { query: { prefix, limit: String(limit) } }),
    /** Whether a crew with worktrees could launch in `cwd` (the server's default when empty). A preview: the launch's 409 `not_a_repo` decides. */
    gitCheck: (cwd: string) => request<GitCheck>('/api/git/check', { query: { cwd } }),
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
