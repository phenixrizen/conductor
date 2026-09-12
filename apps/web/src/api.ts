export type Content = Record<string, unknown>;

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
  id: string;
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
  actor: string,
  signal: AbortSignal,
  body?: unknown,
): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Conductor-Actor': actor },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
  // Bound response consumption before decoding, including chunked responses.
  const reader = response.body?.getReader();
  if (!reader) throw new APIError('The server returned an empty response.', response.status);
  const chunks: Uint8Array[] = [];
  let size = 0;
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.length;
    if (size > 4 * 1024 * 1024) {
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
    const message = typeof value.error?.message === 'string'
      ? value.error.message : `Request failed (${response.status}).`;
    const correlation = typeof value.error?.correlationId === 'string'
      ? ` Reference: ${value.error.correlationId}` : '';
    throw new APIError(message + correlation, response.status);
  }
  return value as T;
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
