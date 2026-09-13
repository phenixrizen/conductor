import type { BrowserAccess } from './api';

export interface GraphSource { repositoryId: string; collectionId: string; digest: string; fullSourceDigest?: string }
export interface GraphSourceRecord extends GraphSource { commit: string; collectedAt: string; freshness: string }
export interface GraphNode { id: string; repositoryId: string; collectionId: string; kind: string; name: string; path?: string; artifactDigest?: string; line?: number }
export interface GraphEdge { from: string; to: string; kind: string; evidence: string; provenance?: string }
export interface GraphGap { repositoryId: string; path?: string; state: string; message: string }
export interface GraphSnapshot { schemaVersion: number; indexer: string; sources: GraphSourceRecord[]; nodes: GraphNode[]; edges: GraphEdge[]; gaps: GraphGap[]; truncated: boolean }
export interface RepositoryGraph { id: string; workspaceId: string; creatorId: string; digest: string; createdAt: string; snapshot: GraphSnapshot }
export interface GraphPage { graphs: { id: string; digest: string; createdAt: string }[]; nextBefore?: string }
export interface GraphQuery extends Omit<GraphSnapshot, 'schemaVersion'> { graphId: string; digest: string }
export function isHex(value: unknown, length: number): value is string { return typeof value === 'string' && value.length === length && /^[a-f0-9]+$/.test(value); }
export function graphPath(id: string) { return `/repository-graphs/${encodeURIComponent(id)}`; }
export function validateGraphPage(page: GraphPage) {
  if (!page || !Array.isArray(page.graphs) || page.graphs.length > 20 || page.graphs.some(g => !isHex(g.id, 32) || !isHex(g.digest, 64) || !Number.isFinite(Date.parse(g.createdAt)))
    || new Set(page.graphs.map(g => g.id)).size !== page.graphs.length || page.nextBefore !== undefined && (typeof page.nextBefore !== 'string' || page.nextBefore.length > 1024)) throw new Error('The graph list is incomplete. Refresh before inspecting a graph.');
}
export function validateGraphData(value: Omit<GraphSnapshot, 'schemaVersion'>, access: BrowserAccess, nodeLimit = 4096) {
  if (!value || typeof value.indexer !== 'string' || typeof value.truncated !== 'boolean' || !Array.isArray(value.sources) || value.sources.length < 1 || value.sources.length > 16
    || !value.sources.some(s => s.repositoryId === access.repositoryID) || new Set(value.sources.map(s => s.repositoryId)).size !== value.sources.length
    || value.sources.some(s => !s || typeof s.repositoryId !== 'string' || !isHex(s.collectionId, 32) || !isHex(s.digest, 64) || !isHex(s.commit, 40) || typeof s.freshness !== 'string' || !Number.isFinite(Date.parse(s.collectedAt)) || s.fullSourceDigest !== undefined && !isHex(s.fullSourceDigest, 64))
    || !Array.isArray(value.nodes) || value.nodes.length > nodeLimit || !Array.isArray(value.edges) || value.edges.length > 8192 || !Array.isArray(value.gaps) || value.gaps.length > 512) throw new Error('The graph response is incomplete or does not match the selected repository.');
  const sources = new Map(value.sources.map(s => [s.repositoryId, s.collectionId]));
  if (value.nodes.some(n => !n || !isHex(n.id, 64) || sources.get(n.repositoryId) !== n.collectionId || typeof n.kind !== 'string' || typeof n.name !== 'string' || n.path !== undefined && typeof n.path !== 'string' || n.artifactDigest !== undefined && !isHex(n.artifactDigest, 64) || n.line !== undefined && (!Number.isSafeInteger(n.line) || n.line < 0))) throw new Error('Graph nodes are not bound to the inspected sources.');
  const nodes = new Set(value.nodes.map(n => n.id));
  if (nodes.size !== value.nodes.length || value.edges.some(e => !e || !nodes.has(e.from) || !nodes.has(e.to) || typeof e.kind !== 'string' || typeof e.evidence !== 'string' || e.provenance !== undefined && typeof e.provenance !== 'string')
    || value.gaps.some(g => !g || !sources.has(g.repositoryId) || typeof g.state !== 'string' || typeof g.message !== 'string' || g.path !== undefined && typeof g.path !== 'string')) throw new Error('Graph relationships or gaps are incomplete.');
}
export function validateGraph(value: RepositoryGraph, access: BrowserAccess, expectedID?: string) {
  if (!value || !isHex(value.id, 32) || expectedID !== undefined && value.id !== expectedID || value.workspaceId !== access.workspaceID || !isHex(value.digest, 64)
    || typeof value.creatorId !== 'string' || !Number.isFinite(Date.parse(value.createdAt)) || value.snapshot?.schemaVersion !== 1) throw new Error('This graph does not match the inspected workspace.');
  validateGraphData(value.snapshot, access);
}
