import { APIError } from './api';
import type { BrowserAccess, Content } from './api';

export interface CollectionInput { commit: string; paths: string[]; fullSource?: boolean }
export interface SourceBundleSummary { digest: string; tree: string; commit: string; size: number; fileCount: number; indexedFiles: number; truncated: boolean; createdAt: string }
export interface ContextSource {
  workspaceId: string; repositoryId: string; provider: string; host: string;
  providerId: string; locator: string; profile: string; integrationVersion: number;
}
export interface ContextSnapshot extends Record<string, unknown> {
  schemaVersion: 2; collectionId: string; collector: string; source: ContextSource;
  repository: string; commit: string; requestedRef: string; collectedAt: string;
  artifacts: Record<string, unknown>[];
}
export interface CollectionReceipt { id: string; digest: string; snapshot: ContextSnapshot; createdAt: string }
export interface CollectionExecution {
  namespace: string; workflowId: string; runId: string; state: string; observedAt: string; current: boolean;
}
export interface CollectionSummary {
  id: string; workspaceId: string; repositoryId: string; requesterId: string; createdAt: string;
  commit: string; cancelRequestedAt?: string; receiptDigest?: string; execution?: CollectionExecution;
}
export interface Collection extends Omit<CollectionSummary, 'commit' | 'receiptDigest'> {
  input: CollectionInput; inputDigest: string; source: ContextSource; receipt?: CollectionReceipt;
  fullSource?: SourceBundleSummary;
}
export interface CollectionPage { collections: CollectionSummary[]; nextBefore?: string }
export interface AttachmentTarget { id: string; revision: number; digest: string; content: Content }

export function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

// Compare every JSON field, regardless of property order. Ignoring an unknown
// nested field could falsely give modified source the stored receipt's linkage.
export function sameJSON(left: unknown, right: unknown): boolean {
  const pending: [unknown, unknown][] = [[left, right]];
  while (pending.length) {
    const [a, b] = pending.pop()!;
    if (a === b) continue;
    if (Array.isArray(a) && Array.isArray(b)) {
      if (a.length !== b.length) return false;
      a.forEach((value, index) => pending.push([value, b[index]]));
      continue;
    }
    if (!object(a) || !object(b)) return false;
    const keys = Object.keys(a);
    if (keys.length !== Object.keys(b).length) return false;
    for (const key of keys) {
      if (!Object.hasOwn(b, key)) return false;
      pending.push([a[key], b[key]]);
    }
  }
  return true;
}

function timestamp(value: unknown): value is string {
  return typeof value === 'string' && Number.isFinite(Date.parse(value));
}
function hex(value: unknown, length: number): value is string {
  return typeof value === 'string' && value.length === length && /^[a-f0-9]+$/.test(value);
}
function source(value: unknown): value is ContextSource {
  return object(value) && ['workspaceId', 'repositoryId', 'provider', 'host', 'providerId', 'locator', 'profile'].every(key => typeof value[key] === 'string')
    && ['github', 'gitlab'].includes(value.provider as string) && Number.isSafeInteger(value.integrationVersion) && Number(value.integrationVersion) > 0;
}
export function remoteSnapshot(value: unknown): value is ContextSnapshot {
  return object(value) && value.schemaVersion === 2 && hex(value.collectionId, 32)
    && value.collector === 'conductor-remote/v1' && source(value.source)
    && typeof value.repository === 'string' && hex(value.commit, 40) && value.requestedRef === value.commit
    && timestamp(value.collectedAt) && Array.isArray(value.artifacts) && value.artifacts.length > 0 && value.artifacts.length <= 32
    && value.artifacts.every(artifact => object(artifact) && typeof artifact.path === 'string'
      && ['collected', 'missing', 'unavailable', 'truncated'].includes(String(artifact.state))
      && (artifact.state === 'collected' ? typeof artifact.text === 'string' && hex(artifact.digest, 64) && hex(artifact.blobOID, 40)
        : artifact.text === undefined && artifact.digest === undefined && typeof artifact.message === 'string'));
}
function validExecution(value: unknown): boolean {
  return value === undefined || (object(value) && ['namespace', 'workflowId', 'runId', 'state'].every(key => typeof value[key] === 'string')
    && timestamp(value.observedAt) && typeof value.current === 'boolean');
}
function validRecord(value: Collection | CollectionSummary, access: BrowserAccess): boolean {
  return !!value && hex(value.id, 32) && value.workspaceId === access.workspaceID && value.repositoryId === access.repositoryID
    && typeof value.requesterId === 'string' && value.requesterId.length > 0 && timestamp(value.createdAt)
    && (value.cancelRequestedAt === undefined || timestamp(value.cancelRequestedAt)) && validExecution(value.execution);
}
export function validateCollection(value: Collection, access: BrowserAccess, expectedID?: string) {
  if (!validRecord(value, access) || (expectedID !== undefined && value.id !== expectedID)
    || !value.input || !hex(value.input.commit, 40) || !Array.isArray(value.input.paths) || value.input.paths.length < 1 || value.input.paths.length > 32
    || value.input.paths.some(path => typeof path !== 'string') || !hex(value.inputDigest, 64) || !source(value.source)
    || value.source.workspaceId !== access.workspaceID || value.source.repositoryId !== access.repositoryID) {
    throw new Error('The collection response does not match this repository or is incomplete. Inspect it again before taking an action.');
  }
  const receipt = value.receipt;
  if (value.input.fullSource !== undefined && typeof value.input.fullSource !== 'boolean') throw new Error('The full-source collection option is invalid.');
  if (value.fullSource !== undefined && (!value.input.fullSource || !receipt || !hex(value.fullSource.digest, 64) || !hex(value.fullSource.tree, 40)
    || value.fullSource.commit !== value.input.commit || !Number.isSafeInteger(value.fullSource.size) || value.fullSource.size < 1 || value.fullSource.size > 32 * 1024 * 1024
    || !Number.isSafeInteger(value.fullSource.fileCount) || value.fullSource.fileCount < 0 || !Number.isSafeInteger(value.fullSource.indexedFiles) || value.fullSource.indexedFiles < 0 || value.fullSource.indexedFiles > value.fullSource.fileCount
    || typeof value.fullSource.truncated !== 'boolean' || !timestamp(value.fullSource.createdAt))) throw new Error('The whole-repository source does not match the inspected receipt.');
  if (receipt !== undefined && (!receipt || receipt.id !== value.id || !hex(receipt.digest, 64) || !timestamp(receipt.createdAt)
    || !remoteSnapshot(receipt.snapshot) || receipt.snapshot.collectionId !== value.id || !sameJSON(receipt.snapshot.source, value.source)
    || receipt.snapshot.commit !== value.input.commit || !sameJSON(receipt.snapshot.artifacts.map(artifact => artifact.path), value.input.paths))) {
    throw new Error('The receipt does not match the inspected collection. Its source cannot be attached.');
  }
}
export function validateCollectionPage(value: CollectionPage, access: BrowserAccess) {
  if (!value || !Array.isArray(value.collections) || value.collections.length > 20
    || (value.nextBefore !== undefined && (typeof value.nextBefore !== 'string' || value.nextBefore.length > 1024))
    || value.collections.some(item => !validRecord(item, access) || !hex(item.commit, 40)
      || (item.receiptDigest !== undefined && !hex(item.receiptDigest, 64)))
    || new Set(value.collections.map(item => item.id)).size !== value.collections.length) {
    throw new Error('The collection list is incomplete or does not match the selected repository. Refresh before inspecting a request.');
  }
}
export function collectionPath(id: string): string { return `/context-collections/${encodeURIComponent(id)}`; }

export function uncertainMutation(failure: unknown): boolean {
  return !(failure instanceof APIError) || failure.status >= 500 || failure.status < 400 || failure.status === 408;
}

export function executionLabel(execution: CollectionExecution | undefined, now: number): string {
  if (!execution) return 'Progress unknown — no execution observation';
  if (execution.state === 'unresolved') return 'Recovery unresolved — operator investigation required';
  if (execution.state === 'unavailable') return 'Progress unavailable';
  if (!['running', 'completed', 'cancelled', 'failed', 'timed_out', 'terminated'].includes(execution.state)) return 'Progress unknown — unsupported execution observation';
  const age = now - Date.parse(execution.observedAt);
  if (!execution.current || !Number.isFinite(age) || age < -5000 || age >= 30_000) return `Stale observation: ${execution.state}`;
  return `Observed execution: ${execution.state}`;
}

export function collectionInput(commit: string, pathLines: string): CollectionInput {
  // Do not silently trim individual paths: leading/trailing spaces may be literal
  // Git names. Only the optional final textarea newline is presentation whitespace.
  const paths = pathLines.replace(/\r\n/g, '\n').replace(/\n$/, '').split('\n');
  const encoder = new TextEncoder();
  if (!hex(commit, 40)) throw new Error('Enter a full lowercase 40-hex commit ID. Branches and abbreviated IDs are not supported.');
  if (paths.length < 1 || paths.length > 32 || new Set(paths).size !== paths.length
    || paths.some(path => !path.trim() || encoder.encode(path).length > 1024 || new TextDecoder().decode(encoder.encode(path)) !== path || /[\u0000-\u001f\u007f\\]/.test(path)
      || path.startsWith('-') || path.startsWith('/') || path.split('/').some(part => part === '' || part === '.' || part === '..'))) {
    throw new Error('Enter 1–32 unique relative file paths, one per line, without dot segments, backslashes, or control characters.');
  }
  // Go normalizes paths by UTF-8 byte order. JavaScript's default UTF-16 order
  // differs for supplementary Unicode characters and would break same-key retry.
  const encoded = paths.map(path => ({ path, bytes: encoder.encode(path) }));
  encoded.sort((a, b) => {
    for (let index = 0; index < Math.min(a.bytes.length, b.bytes.length); index++) {
      if (a.bytes[index] !== b.bytes[index]) return a.bytes[index] - b.bytes[index];
    }
    return a.bytes.length - b.bytes.length;
  });
  return { commit, paths: encoded.map(value => value.path) };
}
