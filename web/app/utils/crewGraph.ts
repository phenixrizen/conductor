import type { ActivityEntry } from '~/utils/protocol'
import type { CrewMember, CrewStart, RunInfo, RunMember } from '~/composables/useSessions'
import type { MemberStatus } from '~/utils/crews'

/**
 * The crew graph: members as nodes, "starts after" rules as solid edges and
 * the handoffs of a run as dashed ones. A member waits for at most one other,
 * so the start rules form a forest: the layout puts each member in the column
 * of its depth (roots left) and the row of its place in the crew within that
 * column, on the 4 px grid. Twelve members at most, so no layout engine.
 */

/** Node size and the gaps between columns and rows, in px. */
export const NODE_W = 240
export const NODE_H = 96
export const GAP_X = 96
export const GAP_Y = 24

export type GraphDirection = 'LR' | 'TB'

/** What a node needs of a member: a saved crew's or a run's. */
export type GraphMember = Pick<CrewMember, 'name' | 'agentId' | 'start'> & Partial<Pick<CrewMember, 'prompt'>> & Partial<Pick<RunMember, 'status' | 'branch' | 'diff' | 'error' | 'sessionId' | 'needsInput' | 'startedAt' | 'endedAt'>>

export interface GraphNode {
  /** The member's name. */
  id: string
  member: GraphMember
  /** Column: 0 for a member that starts on its own (immediately, by hand, or after a member that is not here). */
  depth: number
  /** Row within its column, in crew order. */
  row: number
  x: number
  y: number
  /** The member's state in a run (memberStatus); absent for a saved crew. */
  state?: MemberStatus
  /** The member it starts after when that member is not in the crew, or the cycle it is in: drawn as a root, said on the node. */
  warning?: string
}

export interface GraphEdge {
  id: string
  from: string
  to: string
  kind: 'after' | 'handoff'
  /** Handoffs from `from` to `to` delivered in this run. */
  count?: number
  /** The last handoff's words and time, when the feed had them. */
  last?: string
  lastAt?: string
}

export interface CrewGraphData {
  nodes: GraphNode[]
  edges: GraphEdge[]
}

/** A handoff seen in a run: delivered (the run log) or reported (the feed, with its words). */
export interface HandoffSeen {
  from: string
  to: string
  at: string
  message?: string
}

/** The parent of a member under its start rule: the member it starts after, when that member is in the list. */
function parentOf(m: GraphMember, names: ReadonlySet<string>): string | undefined {
  return m.start.when === 'after' && m.start.member && names.has(m.start.member) ? m.start.member : undefined
}

/**
 * The depth of every member: 0 for a root, else one more than its parent. A
 * member whose rule names a member that is not here, or that sits in a cycle
 * (the server refuses one, but a file may hold it), is a root with a warning.
 */
export function depths(members: readonly GraphMember[]): { depth: Map<string, number>; warnings: Map<string, string> } {
  const names = new Set(members.map((m) => m.name))
  const byName = new Map(members.map((m) => [m.name, m]))
  const depth = new Map<string, number>()
  const warnings = new Map<string, string>()
  for (const m of members) {
    if (m.start.when === 'after' && m.start.member && !names.has(m.start.member)) warnings.set(m.name, `starts after ${m.start.member}, which is not in this crew`)
  }
  for (const m of members) {
    if (depth.has(m.name)) continue
    // Walk up to a root, or until a name repeats (a cycle).
    const chain: string[] = []
    let cur: GraphMember | undefined = m
    while (cur && !depth.has(cur.name)) {
      if (chain.includes(cur.name)) {
        const cycle = chain.slice(chain.indexOf(cur.name))
        for (const n of cycle) {
          depth.set(n, 0)
          warnings.set(n, `the start rules go round in a cycle: ${[...cycle, cycle[0]].join(' after ')}`)
        }
        break
      }
      chain.push(cur.name)
      const p = parentOf(cur, names)
      cur = p ? byName.get(p) : undefined
    }
    // Unwind: the last of the chain is a root or sits on a known depth.
    for (let i = chain.length - 1; i >= 0; i--) {
      const name = chain[i]!
      if (depth.has(name)) continue
      const p = parentOf(byName.get(name)!, names)
      depth.set(name, p && depth.has(p) ? depth.get(p)! + 1 : 0)
    }
  }
  return { depth, warnings }
}

/** The nodes laid out by depth and crew order, and the "after" edges. */
export function layout(members: readonly GraphMember[], direction: GraphDirection = 'LR'): CrewGraphData {
  const { depth, warnings } = depths(members)
  const rows = new Map<number, number>()
  const names = new Set(members.map((m) => m.name))
  const nodes: GraphNode[] = members.map((m) => {
    const d = depth.get(m.name) ?? 0
    const row = rows.get(d) ?? 0
    rows.set(d, row + 1)
    const along = d * (NODE_W + GAP_X)
    const across = row * (NODE_H + GAP_Y)
    return {
      id: m.name,
      member: m,
      depth: d,
      row,
      x: direction === 'LR' ? along : across,
      y: direction === 'LR' ? across : d * (NODE_H + GAP_Y),
      warning: warnings.get(m.name),
    }
  })
  const edges: GraphEdge[] = []
  for (const m of members) {
    const p = parentOf(m, names)
    if (p && !warnings.has(m.name)) edges.push({ id: `after:${p}-${m.name}`, from: p, to: m.name, kind: 'after' })
  }
  return { nodes, edges }
}

/** Handoffs aggregated per pair: how many, and the last one's words and time (the latest with words, else the latest). */
export function handoffEdges(seen: readonly HandoffSeen[], names: ReadonlySet<string>): GraphEdge[] {
  const byPair = new Map<string, GraphEdge>()
  for (const h of seen) {
    if (!names.has(h.from) || !names.has(h.to) || h.from === h.to) continue
    const id = `handoff:${h.from}-${h.to}`
    const e = byPair.get(id) ?? { id, from: h.from, to: h.to, kind: 'handoff' as const, count: 0 }
    e.count = (e.count ?? 0) + 1
    if (h.message && (!e.lastAt || h.at >= e.lastAt || !e.last)) {
      e.last = h.message
      e.lastAt = h.at
    } else if (!e.last && (!e.lastAt || h.at >= e.lastAt)) e.lastAt = h.at
    byPair.set(id, e)
  }
  return [...byPair.values()]
}

/**
 * The handoffs of a run: each delivered one from the run log (its `byName` and `to`) counts once; the feed's handoff events of the run's
 * members (sessionId → member) lend their words to the pair's last message. Older servers noted deliveries in words alone: those are read.
 */
export function handoffsOf(log: readonly ActivityEntry[], feed: ReadonlyArray<ActivityEntry & { sessionId: string }>, memberOf: ReadonlyMap<string, string>): HandoffSeen[] {
  const out: HandoffSeen[] = []
  const words = new Map<string, { at: string; message: string }>()
  for (const e of feed) {
    if (e.type !== 'handoff' || !e.to) continue
    const from = memberOf.get(e.sessionId)
    if (!from) continue
    const key = `${from}>${e.to}`
    const prev = words.get(key)
    if (e.message && (!prev || e.at >= prev.at)) words.set(key, { at: e.at, message: e.message })
  }
  for (const e of log) {
    if (e.type !== 'status') continue
    let from = e.byName ?? ''
    let to = e.to ?? ''
    if (!from || !to) {
      const m = /^handoff delivered from (\S+) to (\S+)$/.exec(e.message ?? '')
      if (!m) continue
      from = m[1] ?? ''
      to = m[2] ?? ''
    } else if (!(e.message ?? '').startsWith('handoff delivered')) continue
    if (!from || !to) continue
    out.push({ from, to, at: e.at, message: words.get(`${from}>${to}`)?.message })
  }
  return out
}

/** The graph of a crew or a run: the layout, the run's member states, and its handoffs when asked. */
export function crewGraph(
  members: readonly GraphMember[],
  opts: { direction?: GraphDirection; states?: ReadonlyMap<string, MemberStatus>; handoffs?: readonly HandoffSeen[] } = {},
): CrewGraphData {
  const g = layout(members, opts.direction ?? 'LR')
  if (opts.states) for (const n of g.nodes) n.state = opts.states.get(n.id)
  if (opts.handoffs?.length) g.edges.push(...handoffEdges(opts.handoffs, new Set(members.map((m) => m.name))))
  return g
}

/**
 * Why `to` cannot start after `from` in the editor: it is itself, `from` is not a member, `to` already waits for another member (one
 * parent: delete that edge first), or the rule would go round (from waits, through others, for to). Empty when it can.
 */
export function connectError(members: readonly GraphMember[], from: string, to: string): string {
  if (from === to) return `${to} cannot start after itself`
  const byName = new Map(members.map((m) => [m.name, m]))
  if (!byName.has(from) || !byName.has(to)) return 'not a member of this crew'
  const target = byName.get(to)!
  if (target.start.when === 'after' && target.start.member && target.start.member !== from) return `${to} already starts after ${target.start.member}: delete that edge first`
  const names = new Set(byName.keys())
  const chain = [from]
  for (let cur = parentOf(byName.get(from)!, names); cur; cur = parentOf(byName.get(cur)!, names)) {
    chain.push(cur)
    if (cur === to) return `that would go round in a cycle: ${[to, ...chain].join(' after ')}`
    if (chain.length > members.length) break
  }
  return ''
}

/** The members with `to` starting after `from` (connectError empty), or starting on its own again (from empty). */
export function withStart<M extends GraphMember>(members: readonly M[], to: string, start: CrewStart): M[] {
  return members.map((m) => (m.name === to ? { ...m, start } : m))
}

/** The rows of the phone list: the nodes in column order, parents before children, indented by depth. */
export function listRows(g: CrewGraphData): GraphNode[] {
  const children = new Map<string, GraphNode[]>()
  const roots: GraphNode[] = []
  const byId = new Map(g.nodes.map((n) => [n.id, n]))
  for (const n of g.nodes) {
    const parent = g.edges.find((e) => e.kind === 'after' && e.to === n.id)?.from
    if (parent && byId.has(parent)) children.set(parent, [...(children.get(parent) ?? []), n])
    else roots.push(n)
  }
  const out: GraphNode[] = []
  const visit = (n: GraphNode) => {
    out.push(n)
    for (const c of children.get(n.id) ?? []) visit(c)
  }
  for (const r of roots) visit(r)
  return out
}
