import { APIError, request } from './api';
import type { BrowserAccess, Content, Revision } from './api';
import { object } from './collections';
import { packageFields } from './PackageContent';

export type DesignSection = typeof packageFields[number][0];
export interface AssistanceInput { changeId: string; expectedRevision: number; expectedDigest: string; instruction: string; sections: DesignSection[] }
export interface ApplySuggestionInput { requestDigest: string; suggestionDigest: string; expectedRevision: number; expectedDigest: string; sections: DesignSection[] }
export interface AssistanceSummary { id: string; requesterId: string; createdAt: string; digest: string; input: AssistanceInput; hasSuggestion: boolean; appliedRevision?: number }
export interface AssistancePage { requests: AssistanceSummary[]; nextBefore?: string }
export interface DesignAssistance {
  id: string; workspaceId: string; repositoryId: string; requesterId: string; createdAt: string; digest: string;
  input: AssistanceInput; base: Revision;
  suggestion?: { digest: string; agentId: string; createdAt: string; sections: Partial<Record<DesignSection, string>>; note?: string };
  application?: { digest: string; appliedBy: string; createdAt: string; revision: number; revisionDigest: string; sections: DesignSection[] };
}

const hex = (value: unknown, size: number): value is string => typeof value === 'string' && new RegExp(`^[a-f0-9]{${size}}$`).test(value);
const accessID = (value: unknown): value is string => typeof value === 'string' && value.trim() === value && value.length > 0 && new TextEncoder().encode(value).length <= 128 && !/[\u0000-\u001f\u007f-\u009f]/.test(value);
const timestamp = (value: unknown) => typeof value === 'string' && Number.isFinite(Date.parse(value));
export const textBytes = (value: string) => new TextEncoder().encode(value).length;
export const sectionName = (value: unknown): value is DesignSection => packageFields.some(([key]) => key === value);
function sections(value: unknown): value is DesignSection[] {
  return Array.isArray(value) && value.length > 0 && value.length <= 6 && value.every(sectionName) && new Set(value).size === value.length;
}
function validInput(value: AssistanceInput) {
  return !!value && accessID(value.changeId) && Number.isSafeInteger(value.expectedRevision) && value.expectedRevision > 0 && hex(value.expectedDigest, 64)
    && typeof value.instruction === 'string' && value.instruction.trim().length > 0 && textBytes(value.instruction) <= 4096 && sections(value.sections);
}
export function validateAssistance(value: DesignAssistance, access: BrowserAccess, id?: string) {
  if (!value || value.workspaceId !== access.workspaceID || value.repositoryId !== access.repositoryID) {
    throw new APIError('The assistance response does not belong to the selected workspace and repository.', 403);
  }
  if (!hex(value.id, 32) || (id && value.id !== id) || !hex(value.digest, 64) || !accessID(value.requesterId) || !timestamp(value.createdAt) || !validInput(value.input)
    || !value.base || value.base.changeId !== value.input.changeId || value.base.number !== value.input.expectedRevision || value.base.digest !== value.input.expectedDigest
    || !object(value.base.content) || !accessID(value.base.author) || !timestamp(value.base.createdAt)
    || value.input.sections.some(key => Object.hasOwn(value.base.content, key) && typeof value.base.content[key] !== 'string')) {
    throw new Error('The assistance response does not contain the exact saved Design basis. Inspect it again.');
  }
  const suggestion = value.suggestion;
  if (suggestion !== undefined && (!suggestion || !hex(suggestion.digest, 64) || !accessID(suggestion.agentId) || !timestamp(suggestion.createdAt) || !object(suggestion.sections)
    || !sections(Object.keys(suggestion.sections)) || Object.entries(suggestion.sections).some(([key, text]) => !value.input.sections.includes(key as DesignSection) || typeof text !== 'string' || textBytes(text) > 32768)
    || (suggestion.note !== undefined && (typeof suggestion.note !== 'string' || textBytes(suggestion.note) > 4096))
    || textBytes(JSON.stringify({ requestDigest: value.digest, sections: suggestion.sections, ...(suggestion.note !== undefined ? { note: suggestion.note } : {}) })) > 128 * 1024)) {
    throw new Error('The suggestion is incomplete or exceeds the reviewable section bounds.');
  }
  const application = value.application;
  if (application !== undefined && (!application || !suggestion || !hex(application.digest, 64) || application.appliedBy !== value.requesterId || !timestamp(application.createdAt)
    || application.revision !== value.base.number + 1 || !hex(application.revisionDigest, 64) || !sections(application.sections)
    || application.sections.some(key => !Object.hasOwn(suggestion.sections, key)))) {
    throw new Error('The application fact does not match this request and suggestion.');
  }
}
export function validateAssistancePage(value: AssistancePage, changeId?: string) {
  if (!value || !Array.isArray(value.requests) || value.requests.length > 20 || (value.nextBefore !== undefined && (typeof value.nextBefore !== 'string' || !value.nextBefore || value.nextBefore.length > 4096))
    || value.requests.some(item => !item || !hex(item.id, 32) || !accessID(item.requesterId) || !hex(item.digest, 64) || !timestamp(item.createdAt) || !validInput(item.input)
      || (changeId && item.input.changeId !== changeId) || typeof item.hasSuggestion !== 'boolean' || (item.appliedRevision !== undefined && (!item.hasSuggestion || !Number.isSafeInteger(item.appliedRevision) || item.appliedRevision !== item.input.expectedRevision + 1)))) {
    throw new Error('The assistance list is incomplete or does not match the selected Change.');
  }
}
export function suggestedContent(record: DesignAssistance, selected: DesignSection[]): Content {
  const content = { ...record.base.content };
  for (const key of selected) {
    if (!record.suggestion || !Object.hasOwn(record.suggestion.sections, key)) throw new Error('Select only retained suggested sections.');
    content[key] = record.suggestion.sections[key];
  }
  return content;
}
export const assistancePath = (id: string) => `/design-assistance/${encodeURIComponent(id)}`;
export function listAssistance(changeId: string | undefined, before: string | undefined, access: BrowserAccess, signal: AbortSignal) {
  const query = new URLSearchParams({ limit: '20' });
  if (changeId) query.set('changeId', changeId);
  if (before) query.set('before', before);
  return request<AssistancePage>(`/design-assistance?${query}`, access, signal);
}
export const getAssistance = (id: string, access: BrowserAccess, signal: AbortSignal) => request<DesignAssistance>(assistancePath(id), access, signal);
export const createAssistance = (input: AssistanceInput, key: string, access: BrowserAccess, signal: AbortSignal) => request<DesignAssistance>('/design-assistance', access, signal, input, { idempotencyKey: key });
export const applyAssistance = (id: string, input: ApplySuggestionInput, key: string, access: BrowserAccess, signal: AbortSignal) => request<DesignAssistance>(`${assistancePath(id)}/application`, access, signal, input, { idempotencyKey: key });
