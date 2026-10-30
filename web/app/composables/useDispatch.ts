import { DispatchOrder, through } from '~/utils/dispatchOrder'

// Dispatch's client (docs/plans/dispatch.md, "web/app"; the routes in docs/protocol.md, Dispatch): the types of every reply, as
// useSessions.ts holds the crews' and the runs', the dispatch event's beside them, and the routes over useApi. Every page's state goes
// through its own ordering gate (useDispatch().gate, utils/dispatchOrder.ts); the operations go through useDispatchOps.ts and the live
// events through useDispatchLive.ts.

/** A ticket's status (Owner decisions, 4: six, fixed). */
export type TicketStatus = 'backlog' | 'ready' | 'in_progress' | 'in_review' | 'done' | 'cancelled'
/** A ticket's horizon, a roadmap's column: Now, Next, Later, or '' for not planned. */
export type Horizon = '' | 'now' | 'next' | 'later'
/** A project's tracker: its own (native), or a pointer at another one by its URL (external). */
export type Tracker = 'native' | 'external'
/** What a project's close policy may require before a ticket is done. */
export type CloseRequirement = 'acceptance' | 'delivery'
/** The twelve colours a label may have, by name; the UI maps each to one of the brand's scales (utils/dispatch.ts). */
export type LabelColor = 'gray' | 'brown' | 'red' | 'orange' | 'amber' | 'yellow' | 'lime' | 'green' | 'teal' | 'cyan' | 'blue' | 'indigo'
/** What a list is sorted by: its updated or created time, its priority, its status, its title, its key, its milestone. */
export type DispatchSort = 'updated' | 'created' | 'priority' | 'status' | 'title' | 'key' | 'milestone'
/** What a dispatch event says changed. */
export type DispatchEntity = 'project' | 'ticket' | 'comment' | 'label' | 'milestone' | 'view' | 'person'

/** A project as its read carries it (GET /api/dispatch/projects/{key}). Times are RFC 3339; a zero one is absent. */
export interface DispatchProject {
  id: string
  key: string
  name: string
  description: string
  cwd: string
  tracker: Tracker
  trackerUrl: string
  closeRequires: CloseRequirement[]
  nextNumber: number
  numberFrom: number
  numberTo: number
  archivedAt?: string
  createdAt: string
  updatedAt: string
  rev: number
}

/** A project as the projects page lists it: its counts of open and done tickets, without its description, folder, URL or policy. */
export interface ProjectSummary {
  id: string
  key: string
  name: string
  tracker: Tracker
  archivedAt?: string
  open: number
  done: number
  rev: number
  updatedAt: string
}

export interface ProjectsPage {
  projects: ProjectSummary[]
  next?: string
}

export interface DispatchLabel {
  id: string
  projectId: string
  name: string
  color: LabelColor
  createdAt: string
  deletedAt?: string
  rev: number
}

export interface DispatchMilestone {
  id: string
  projectId: string
  name: string
  description: string
  /** YYYY-MM-DD, or '' for none. */
  target: string
  state: 'open' | 'closed'
  createdAt: string
  updatedAt: string
  deletedAt?: string
  rev: number
}

/** A saved view's query: a list's filter and sort as the store keeps them (labels and the milestone by id), and its grouping. */
export interface ViewQuery {
  status?: string[]
  priority?: number[]
  labels?: string[]
  owner?: string
  assignees?: string[]
  milestone?: string
  horizon?: string[]
  parent?: string
  blocked?: boolean
  q?: string
  deleted?: boolean
  sort?: string
  desc?: boolean
  group?: string
}

export interface DispatchView {
  id: string
  /** '' for a view of every project. */
  projectId: string
  name: string
  kind: 'list' | 'board' | 'roadmap'
  query: ViewQuery
  createdAt: string
  updatedAt: string
  deletedAt?: string
  rev: number
}

/** A project with what its pages offer: its live labels by name, its live milestones by target, the saved views. */
export interface ProjectRead {
  project: DispatchProject
  labels: DispatchLabel[]
  milestones: DispatchMilestone[]
  views: DispatchView[]
}

/** A name of the directory the owner and assignee pickers offer (people are local names until identity exists). */
export interface DispatchPerson {
  name: string
  createdAt: string
}

/** A ticket as a list shows it: no description, criteria or acceptance; its labels by id, its assignees as `person:<name>` or `agent:<id>`. */
export interface TicketSummary {
  id: string
  projectId: string
  number: number
  key: string
  title: string
  status: TicketStatus
  priority: number
  horizon: Horizon
  owner: string
  assignees: string[]
  labels: string[]
  milestoneId: string
  parentId: string
  blocked: boolean
  createdAt: string
  updatedAt: string
  closedAt?: string
  deletedAt?: string
  rev: number
}

export interface TicketsPage {
  tickets: TicketSummary[]
  next?: string
  /** How many tickets the whole list holds. */
  total: number
}

/** The ticket at the other end of a reference: its parent, a relation's other end, a child, the ticket a run is linked to. */
export interface RelatedTicket {
  id: string
  key: string
  title: string
  status: TicketStatus
  deletedAt?: string
}

export interface TicketRelation {
  id: string
  fromId: string
  toId: string
  kind: 'blocks' | 'relates'
  createdAt: string
  createdBy: string
  deletedAt?: string
  other: RelatedTicket
}

export interface TicketLink {
  id: string
  kind: 'delivery' | 'run' | 'external'
  ref: string
  url: string
  note: string
  createdAt: string
  createdBy: string
  deletedAt?: string
}

export interface TicketAttachment {
  id: string
  ticketId: string
  name: string
  size: number
  sha256: string
  contentType: string
  createdAt: string
  createdBy: string
  deletedAt?: string
}

export interface TicketComment {
  id: string
  ticketId: string
  /** Its 1-based place among the ticket's comments, derived at read. */
  n: number
  author: string
  by: string
  body: string
  createdAt: string
  editedAt?: string
  deletedAt?: string
  rev: number
}

/** A ticket's close policy as read: what its project requires, what it meets, what it lacks, and why an acceptance on record is unmet. */
export interface CloseState {
  requires: CloseRequirement[]
  met: { acceptance: boolean; delivery: boolean }
  missing: CloseRequirement[]
  why?: string
}

/** A ticket in full, as its page reads it (GET /api/dispatch/tickets/{key}), from one read transaction. */
export interface DispatchTicket {
  id: string
  projectId: string
  number: number
  key: string
  title: string
  description: string
  acceptance: string
  status: TicketStatus
  priority: number
  horizon: Horizon
  owner: string
  parentId: string
  milestoneId: string
  acceptedAt?: string
  acceptedBy?: string
  acceptedFor?: string
  acceptedCriteria?: string
  acceptedNote?: string
  source: string
  externalId?: string
  externalUrl?: string
  createdAt: string
  createdBy: string
  updatedAt: string
  closedAt?: string
  deletedAt?: string
  rev: number
  assignees: string[]
  labels: string[]
  parent?: RelatedTicket
  children: { tickets: RelatedTicket[]; total: number }
  relations: TicketRelation[]
  relationsTotal: number
  blocked: boolean
  links: TicketLink[]
  attachments: TicketAttachment[]
  comments: TicketComment[]
  close: CloseState
}

/** One row of a ticket's history, as the store recorded it: one field set, added or removed by one operation. */
export interface DispatchChange {
  seq: number
  entity: DispatchEntity
  entityId: string
  ticketId?: string
  projectId?: string
  field?: string
  op: 'set' | 'add' | 'remove' | 'create' | 'delete' | 'reconcile' | 'migrate'
  value: string
  prior?: string
  at: string
  actor: string
  by?: string
  opId: string
  /** The row's place in its operation, from 0. */
  n: number
}

export interface HistoryPage {
  changes: DispatchChange[]
  /** The seq to read after for the next page. */
  next?: number
}

/** An operation's receipt (docs/protocol.md, Dispatch operations): kept under its id, answered again for the same id. */
export interface DispatchReceipt {
  opId: string
  intent: string
  status: 'done' | 'failed'
  rev?: number
  id?: string
  key?: string
  repeat?: boolean
  error?: string
  refusal?: string
  missing?: CloseRequirement[]
}

/**
 * The `dispatch` event (GET /api/events): something of Dispatch's changed, for a page to read again. `seq` is the commit's last change;
 * `entity` what changed and `id` its id (empty for a person, and for the one event a commit that changed more than 64 entities of a kind
 * in a project sends for them); `ticket` the id of the ticket it belongs to and `project` its project's key, empty where there is none.
 * A commit's events arrive together. Nothing reads an event's `id` or `ticket` without checking it is set.
 */
export interface DispatchEvent {
  seq: number
  entity: DispatchEntity
  id: string
  ticket: string
  project: string
}

/** What every Dispatch write carries beside its fields: the op id the browser mints, the revision it expects (none: absent), the display name. */
export interface OpBody {
  opId: string
  expectedRev?: number
  byName?: string
}

export interface NewProject {
  key: string
  name: string
  description?: string
  cwd?: string
  tracker?: Tracker
  trackerUrl?: string
  closeRequires?: CloseRequirement[]
}

export interface NewTicket {
  project: string
  title: string
  description?: string
  acceptance?: string
  status?: TicketStatus
  priority?: number
  horizon?: Horizon
  owner?: string
  assignees?: string[]
  labels?: string[]
  milestone?: string
  parent?: string
}

/** A ticket's edit, every change one operation: the fields set (a parent by key, a milestone by id, '' for none) and the members added and removed. */
export interface TicketEdit {
  set?: Partial<{ title: string; description: string; acceptance: string; status: TicketStatus; priority: number; horizon: Horizon; owner: string; parent: string; milestone: string }>
  add?: { assignees?: string[]; labels?: string[] }
  remove?: { assignees?: string[]; labels?: string[] }
}

export interface ProjectReply {
  receipt: DispatchReceipt
  project: DispatchProject | null
}

export interface TicketReply {
  receipt: DispatchReceipt
  ticket: DispatchTicket | null
}

const enc = encodeURIComponent

/** Dispatch's routes, over useApi: the workbench token in the header, errors as ApiError (a refused operation's `body` carries its receipt). */
export function useDispatchApi() {
  const { request } = useApi()
  return {
    /** A page of the projects by key, after `cursor` (a page's `next`). */
    projects: (cursor = '') => request<ProjectsPage>('/api/dispatch/projects' + (cursor ? `?cursor=${enc(cursor)}` : '')),
    project: (key: string) => request<ProjectRead>(`/api/dispatch/projects/${enc(key)}`),
    createProject: (body: OpBody & NewProject) => request<ProjectReply>('/api/dispatch/projects', { method: 'POST', body }),
    people: () => request<{ people: DispatchPerson[] }>('/api/dispatch/people').then((r) => r.people ?? []),
    /** A page of a list of tickets; `query` is the API's query string (utils/dispatchQuery.ts, apiQuery), without its `?`. */
    tickets: (query: string) => request<TicketsPage>('/api/dispatch/tickets' + (query ? `?${query}` : '')),
    ticket: (key: string) => request<{ ticket: DispatchTicket }>(`/api/dispatch/tickets/${enc(key)}`).then((r) => r.ticket),
    createTicket: (body: OpBody & NewTicket) => request<TicketReply>('/api/dispatch/tickets', { method: 'POST', body }),
    editTicket: (key: string, body: OpBody & TicketEdit) => request<TicketReply>(`/api/dispatch/tickets/${enc(key)}/edit`, { method: 'POST', body }),
    /** A page of a ticket's history after the change `after` (its seq; 0 from the start), oldest first. */
    history: (key: string, after = 0) => request<HistoryPage>(`/api/dispatch/tickets/${enc(key)}/history` + (after ? `?after=${after}` : '')),
    /** An operation's receipt, read without acting. */
    receipt: (opId: string) => request<{ receipt: DispatchReceipt }>(`/api/dispatch/ops/${enc(opId)}`).then((r) => r.receipt),
  }
}

export type DispatchApi = ReturnType<typeof useDispatchApi>

/**
 * A Dispatch page's client and its ordering gate (utils/dispatchOrder.ts): `read` sends a request about one thing the page shows and
 * applies its reply only when the gate takes it, so every read and every operation's reply on the page lands in the order the gate keeps.
 * One gate per page: each call makes its own.
 */
export function useDispatch() {
  const api = useDispatchApi()
  const gate = new DispatchOrder()
  function read<T>(thing: string, send: () => Promise<T>, apply: (reply: T) => void, rev?: (reply: T) => number | undefined): Promise<boolean> {
    return through(gate, thing, send, apply, rev)
  }
  return { api, gate, read }
}
