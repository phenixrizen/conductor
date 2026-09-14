export type Content = Record<string, unknown>;

export interface Principal { id: string; kind: 'human' | 'agent' }
export interface Workspace { id: string; name: string }
export interface BrowserSession {
  session: { principal: Principal; workspaces: Workspace[]; truncated: boolean };
  csrfToken: string;
}
export interface AuthConfig { mode: 'local' | 'oidc'; browserLogin: boolean }
export interface ManagedRepository {
  id: string;
  workspaceId: string;
  provider: 'github' | 'gitlab';
  host: string;
  providerId: string;
  name: string;
  canRead: boolean;
  canAuthor: boolean;
  canApprove: boolean;
}
export interface RepositoryPage { repositories: ManagedRepository[]; truncated: boolean }

// Every asynchronous operation captures one immutable access selection. Session
// cookies are managed by the browser; access/ID tokens never enter this object.
export type BrowserAccess = Readonly<{
  mode: 'browser';
  principalID: string;
  principalKind: 'human' | 'agent';
  csrfToken: string;
  workspaceID: string;
  repositoryID: string;
  canAuthor: boolean;
  canApprove: boolean;
}>;
export type RequestAccess = Readonly<{ mode: 'local'; actor: string }> | BrowserAccess;

export interface Revision {
  changeId: string;
  number: number;
  digest: string;
  author: string;
  createdAt: string;
  submittedAt?: string;
  content: Content;
}

export interface Approval {
  revision: number;
  digest: string;
  reviewer: string;
  createdAt: string;
}

export interface WorkPackage {
  id: string;
  workspaceId?: string;
  repositoryId?: string;
  revision: Revision;
  approved: boolean;
  approval?: Approval;
}

export interface RevisionView {
  revision: Revision;
  approvals: Approval[];
  approvalsTruncated: boolean;
}

export interface RevisionSummary {
  number: number;
  digest: string;
  author: string;
  createdAt: string;
  submittedAt?: string;
  approvalCount: number;
}

export interface HistoryPage {
  revisions: RevisionSummary[];
  nextBeforeRevision?: number;
}

export interface AuditEvent {
  sequence: number;
  eventType: string;
  actor: string;
  revision: number;
  data: unknown;
  createdAt: string;
}

export interface EventPage {
  events: AuditEvent[];
  nextAfterSequence?: number;
}

export interface SharedChange {
  title?: string;
  intent?: string;
  titleTruncated?: boolean;
  intentTruncated?: boolean;
  id: string;
  workspaceId?: string;
  repositoryId?: string;
  revision: number;
  digest: string;
  author: string;
  createdAt: string;
  approved: boolean;
  repository?: string;
}

export interface SharedPage {
  changes: SharedChange[];
  nextBefore?: string;
}

export class APIError extends Error {
  constructor(message: string, public status: number) {
    super(message);
  }
}

export async function request<T>(
  path: string,
  access: RequestAccess | undefined,
  signal: AbortSignal,
  body?: unknown,
  options?: Readonly<{ idempotencyKey: string }>,
): Promise<T> {
  const controller = new AbortController();
  let timedOut = false;
  const cancel = () => controller.abort();
  if (signal.aborted) cancel();
  else signal.addEventListener('abort', cancel, { once: true });
  // The deadline covers both headers and a stalled/chunked response body. It
  // does not replace caller cancellation when identity or scope changes.
  const timer = setTimeout(() => { timedOut = true; controller.abort(); }, 20_000);
  try {
    return await requestWithSignal<T>(path, access, controller.signal, body, options);
  } catch (failure) {
    if (timedOut && !signal.aborted && !(failure instanceof APIError && (failure.status === 401 || failure.status === 403))) {
      throw new Error('The server did not respond within 20 seconds. Retry when it is available.');
    }
    throw failure;
  } finally {
    clearTimeout(timer);
    signal.removeEventListener('abort', cancel);
  }
}

async function requestWithSignal<T>(path: string, access: RequestAccess | undefined, signal: AbortSignal, body?: unknown, options?: Readonly<{ idempotencyKey: string }>): Promise<T> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  // Only explicitly idempotent commands accept this header. Callers cannot replace
  // identity, scope, CSRF, or transport controls through arbitrary headers.
  if (options) {
    if (!(['/context-collections', '/repository-graphs', '/coordination-runs', '/repository-deliveries', '/tracker-links', '/runtime-evidence', '/design-assistance'].includes(path) || /^\/design-assistance\/[a-f0-9]{32}\/application$/.test(path) || /^\/repository-deliveries\/[a-f0-9]{32}\/reconciliations$/.test(path) || /^\/tracker-links\/[a-f0-9]{32}\/syncs$/.test(path)) || body === undefined || !/^[\x21-\x2b\x2d-\x7e]{1,128}$/.test(options.idempotencyKey)) {
      throw new Error('This command requires a valid idempotency key.');
    }
    headers['Idempotency-Key'] = options.idempotencyKey;
  }
  if (access?.mode === 'local') headers['X-Conductor-Actor'] = access.actor;
  if (access?.mode === 'browser') {
    if (access.workspaceID) headers['X-Conductor-Workspace'] = access.workspaceID;
    if (access.repositoryID) headers['X-Conductor-Repository'] = access.repositoryID;
    // Bind reads too: another tab may replace the shared session cookie while
    // this inspection still belongs to the previous principal.
    headers['X-Conductor-CSRF'] = access.csrfToken;
  }
  const response = await fetch(`/api/v1${path}`, {
    method: body === undefined ? 'GET' : 'POST',
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    credentials: 'same-origin',
    redirect: 'error',
    cache: 'no-store',
    signal,
  });
  // Denial headers are sufficient to invalidate this inspection. A broken or
  // stalled error body must never turn revoked access into an ordinary retry.
  if (response.status === 401 || response.status === 403) {
    void response.body?.cancel().catch(() => undefined);
    throw new APIError(response.status === 401 ? 'Your session has ended.' : 'Your access could not be confirmed.', response.status);
  }
  // Bound response consumption before decoding, including chunked responses.
  // Only the complete retained implementation artifact has the larger envelope.
  const maxBytes = body === undefined && (/^\/repository-deliveries\/[a-f0-9]{32}\/artifact$/.test(path) || /^\/coordination-runs\/[a-f0-9]{32}\/artifact\?/.test(path)) ? 17 * 1024 * 1024 : 4 * 1024 * 1024;
  const reader = response.body?.getReader();
  if (!reader) throw new APIError('The server returned an empty response.', response.status);
  const chunks: Uint8Array[] = [];
  let size = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.length;
    if (size > maxBytes) {
      await reader.cancel();
      throw new APIError('The response is too large to inspect safely.', response.status);
    }
    chunks.push(value);
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.length;
  }
  let value;
  try {
    value = JSON.parse(new TextDecoder().decode(bytes));
  } catch {
    throw new APIError('The server response was not valid JSON. Check API connectivity.', response.status);
  }
  if (!response.ok) {
    const message = typeof value?.error?.message === 'string'
      ? value.error.message : `Request failed (${response.status}).`;
    const correlation = typeof value?.error?.correlationId === 'string'
      ? ` Reference: ${value.error.correlationId}` : '';
    throw new APIError(message + correlation, response.status);
  }
  return value as T;
}

export function accessFailure(error: unknown, access: RequestAccess): error is APIError {
  return access.mode === 'browser' && error instanceof APIError && (error.status === 401 || error.status === 403);
}

export function changePath(id: string): string {
  return `/changes/${encodeURIComponent(id)}`;
}

export function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : 'The request could not be completed.';
}

export function dateLabel(value?: string): string {
  if (!value) return 'Not recorded';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? 'Unavailable timestamp' : date.toLocaleString();
}

// Package commands use the exact inspected input; none performs a preflight read.
export function createChange(content: Content, access: RequestAccess, signal: AbortSignal) {
  return request<WorkPackage>('/changes', access, signal, { content });
}
export function reviseChange(id: string, expectedRevision: number, content: Content, access: RequestAccess, signal: AbortSignal) {
  return request<WorkPackage>(`${changePath(id)}/revisions`, access, signal, { expectedRevision, content });
}
export function submitChange(id: string, revision: number, access: RequestAccess, signal: AbortSignal) {
  return request<WorkPackage>(`${changePath(id)}/review-requests`, access, signal, { revision });
}
