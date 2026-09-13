import type { BrowserAccess } from './api';
import type { CollectionExecution } from './collections';
import type { DeliveryObservation } from './delivery';
import { isHex } from './graphs';

export interface RuntimeRequirement { changeId: string; revision: number; digest: string; criterionId: string }
export interface RuntimeInput {
  deliveryId: string; deliveryDigest: string; observationSequence: number;
  deploymentId: string; commit: string; environment: string; start: string; end: string;
  requirements: RuntimeRequirement[];
}
export interface RuntimeCriterion {
  id: string; metric: string; aggregation: 'maximum' | 'minimum' | 'mean'; operator: 'lte' | 'gte';
  threshold: number; expectedSeries: number; windowSeconds: number; stepSeconds: number; maxAgeSeconds: number;
}
export interface RuntimeSignal {
  kind: 'metrics' | 'logs' | 'traces'; metric?: string;
  state: 'collected' | 'missing' | 'unavailable' | 'truncated' | 'uncorrelated';
  correlation: 'exact' | 'not_established'; coverage: string;
  queryDigest: string; responseDigest?: string; endpoint: string; collectedAt: string;
  series: { labels: Record<string, string>; points: { at: string; value: number }[] }[];
  records: { at: string; data: unknown }[];
}
export interface RuntimeEvidence {
  id: string; workspaceId: string; repositoryId: string; requesterId: string; input: RuntimeInput; digest: string;
  target: {
    workspaceId: string; repositoryId: string; environment: string; service: string; backendId: string;
    profile: string; sourceCommit: string; integrationVersion: number; metrics: string[];
    metricFields: Record<string, string>; recordFields: Record<string, string>; stepSeconds: number; maxAgeSeconds: number;
  };
  deployment: DeliveryObservation['deployments'][number];
  criteria: { requirement: RuntimeRequirement; criterion: RuntimeCriterion; criterionDigest: string }[];
  createdAt: string; freshness: 'not_collected' | 'current' | 'stale'; execution?: CollectionExecution;
  receipt?: {
    digest: string; collectedAt: string; productionOutcome: 'not_verified'; signals: RuntimeSignal[];
    evaluations: { requirement: RuntimeRequirement; criterionDigest: string; state: 'met' | 'not_met' | 'not_verified'; reason: string; value?: number }[];
  };
}
export interface RuntimePage {
  evidence: { id: string; deliveryId: string; repositoryId: string; environment: string; commit: string; digest: string; createdAt: string; receiptDigest?: string }[];
  nextBefore?: string;
}
export const runtimePath = (id: string) => `/runtime-evidence/${encodeURIComponent(id)}`;
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max = 128): v is string => typeof v === 'string' && v.length > 0 && new TextEncoder().encode(v).length <= max && !/[\u0000-\u001f\u007f]/.test(v);
const keys = (v: Record<string, unknown>, allowed: string[]) => Object.keys(v).every(k => allowed.includes(k));
const integer = (v: unknown, min: number, max = Number.MAX_SAFE_INTEGER) => Number.isSafeInteger(v) && Number(v) >= min && Number(v) <= max;
const date = (v: unknown): v is string => typeof v === 'string' && Number.isFinite(Date.parse(v));
const requirement = (v: unknown): v is RuntimeRequirement => object(v) && keys(v, ['changeId', 'revision', 'digest', 'criterionId']) && text(v.changeId) && integer(v.revision, 1) && isHex(v.digest, 64) && text(v.criterionId);
const sameRequirement = (a: RuntimeRequirement, b: RuntimeRequirement) => a.changeId === b.changeId && a.revision === b.revision && a.digest === b.digest && a.criterionId === b.criterionId;

// Preview never invents a threshold, query, deployment or time window. Historical
// reads validate the same shape without treating their age as invalid content.
export function runtimeInput(value: unknown, now?: number): RuntimeInput {
  if (!object(value) || !keys(value, ['deliveryId', 'deliveryDigest', 'observationSequence', 'deploymentId', 'commit', 'environment', 'start', 'end', 'requirements']) ||
      !isHex(value.deliveryId, 32) || !isHex(value.deliveryDigest, 64) || !integer(value.observationSequence, 1) || !text(value.deploymentId) ||
      !isHex(value.commit, 40) || !text(value.environment) || !/^[A-Za-z0-9][A-Za-z0-9 ._/-]{0,127}$/.test(value.environment) ||
      !date(value.start) || !date(value.end) || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(value.start) || !/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(value.end) ||
      !Array.isArray(value.requirements) || value.requirements.length > 16 || !value.requirements.every(requirement)) {
    throw new Error('Provide exact delivery/deployment pins, a whole-second UTC window and up to 16 approved criterion references. Unknown or incomplete fields are rejected.');
  }
  const input = value as unknown as RuntimeInput, start = Date.parse(input.start), end = Date.parse(input.end);
  if (end <= start || end - start > 3600000 || now !== undefined && (end > now || start < now - 7 * 86400000) || new Set(input.requirements.map(r => `${r.changeId}:${r.criterionId}`)).size !== input.requirements.length) {
    throw new Error('Use a past window of at most one hour, starting within seven days, and unique criterion references.');
  }
  return input;
}

export function validateRuntimePage(v: RuntimePage, access: BrowserAccess) {
  if (!v || !Array.isArray(v.evidence) || v.evidence.length > 20 || v.nextBefore !== undefined && !text(v.nextBefore, 1024) ||
      v.evidence.some(r => !r || !isHex(r.id, 32) || !isHex(r.deliveryId, 32) || r.repositoryId !== access.repositoryID || !text(r.environment) || !isHex(r.commit, 40) || !isHex(r.digest, 64) || !date(r.createdAt) || r.receiptDigest !== undefined && !isHex(r.receiptDigest, 64)) ||
      new Set(v.evidence.map(r => r.id)).size !== v.evidence.length) throw new Error('Runtime evidence discovery is incomplete or outside the selected repository.');
}

export function validateRuntime(v: RuntimeEvidence, access: BrowserAccess, id?: string) {
  if (!v || !isHex(v.id, 32) || id !== undefined && id !== v.id || v.workspaceId !== access.workspaceID || v.repositoryId !== access.repositoryID ||
      !isHex(v.digest, 64) || !text(v.requesterId) || !date(v.createdAt) || !['not_collected', 'current', 'stale'].includes(v.freshness)) throw new Error('Runtime evidence does not match the selected scope.');
  runtimeInput(v.input);
  const target = v.target, deployment = v.deployment;
  if (!target || target.workspaceId !== v.workspaceId || target.repositoryId !== v.repositoryId || target.environment !== v.input.environment ||
      !text(target.service) || !text(target.backendId) || !text(target.profile) || !isHex(target.sourceCommit, 40) || !integer(target.integrationVersion, 1) ||
      !Array.isArray(target.metrics) || target.metrics.length > 4 || target.metrics.some(m => !text(m)) || !object(target.metricFields) || !object(target.recordFields) ||
      !integer(target.stepSeconds, 15, 300) || !integer(target.maxAgeSeconds, 60, 86400) || !deployment || deployment.id !== v.input.deploymentId ||
      deployment.repositoryId !== v.repositoryId || deployment.commit !== v.input.commit || deployment.environment !== v.input.environment || typeof deployment.state !== 'string') throw new Error('Runtime target or deployment pins are incomplete.');
  v.criteria = v.criteria ?? [];
  if (!Array.isArray(v.criteria) || v.criteria.length !== v.input.requirements.length || v.criteria.some(l => !l || !requirement(l.requirement) ||
      !v.input.requirements.some(r => sameRequirement(r, l.requirement)) || !isHex(l.criterionDigest, 64) || !l.criterion || l.criterion.id !== l.requirement.criterionId ||
      !text(l.criterion.metric) || !['maximum', 'minimum', 'mean'].includes(l.criterion.aggregation) || !['lte', 'gte'].includes(l.criterion.operator) ||
      !Number.isFinite(l.criterion.threshold) || !integer(l.criterion.expectedSeries, 1, 8) || !integer(l.criterion.windowSeconds, 15, 3600) || !integer(l.criterion.stepSeconds, 15, 300) || !integer(l.criterion.maxAgeSeconds, 60, 86400)) ||
      new Set(v.criteria.map(l => `${l.requirement.changeId}:${l.requirement.criterionId}`)).size !== v.criteria.length) throw new Error('Approved runtime criteria are incomplete.');
  if (!v.receipt) return;
  const receipt = v.receipt;
  if (!isHex(receipt.digest, 64) || !date(receipt.collectedAt) || receipt.productionOutcome !== 'not_verified' || !Array.isArray(receipt.signals) || receipt.signals.length !== target.metrics.length + 2 ||
      new Set(receipt.signals.map(s => `${s.kind}:${s.metric ?? ''}`)).size !== receipt.signals.length) throw new Error('Retained runtime receipt is incomplete.');
  receipt.evaluations = receipt.evaluations ?? [];
  if (receipt.evaluations.length !== v.criteria.length || receipt.evaluations.some(e => !e || !requirement(e.requirement) || !v.criteria.some(l => sameRequirement(l.requirement, e.requirement) && l.criterionDigest === e.criterionDigest) ||
      !['met', 'not_met', 'not_verified'].includes(e.state) || typeof e.reason !== 'string' || e.value !== undefined && !Number.isFinite(e.value)) ||
      new Set(receipt.evaluations.map(e => `${e.requirement.changeId}:${e.requirement.criterionId}`)).size !== receipt.evaluations.length) throw new Error('Criterion evaluations do not match the inspected policy.');
  for (const s of receipt.signals) {
    if (!s || !['metrics', 'logs', 'traces'].includes(s.kind) || !['collected', 'missing', 'unavailable', 'truncated', 'uncorrelated'].includes(s.state) ||
        !['exact', 'not_established'].includes(s.correlation) || !['unknown', 'empty_query_result', 'truncated', 'complete_query_grid', 'partial_query_grid', 'bounded_query_result'].includes(s.coverage) ||
        !isHex(s.queryDigest, 64) || s.responseDigest !== undefined && !isHex(s.responseDigest, 64) || !date(s.collectedAt) || typeof s.endpoint !== 'string') throw new Error('Runtime signal provenance is incomplete.');
    s.series = s.series ?? []; s.records = s.records ?? [];
    if (s.kind === 'metrics' ? !s.metric || !target.metrics.includes(s.metric) || s.endpoint !== '/api/metrics/query-range' : s.metric !== undefined || s.endpoint !== `/api/${s.kind}/v2/search`) throw new Error('Runtime signal does not match the bound integration.');
    if (!Array.isArray(s.series) || s.series.length > 8 || !Array.isArray(s.records) || s.records.length > 50 ||
        s.series.some(series => !series || !object(series.labels) || Object.values(series.labels).some(label => typeof label !== 'string') || !Array.isArray(series.points) || series.points.length > 241 || series.points.some(p => !p || !date(p.at) || !Number.isFinite(p.value))) ||
        s.records.some(r => !r || !date(r.at) || r.data === undefined || new TextEncoder().encode(JSON.stringify(r.data)).length > 8192)) throw new Error('Runtime signal exceeds its retained bounds or has incomplete source data.');
  }
}

// Current means only that this window is within its configured display age.
// It never turns a historical criterion result into present application health.
export function runtimeFreshness(v: RuntimeEvidence, now: number) {
  if (!v.receipt) return 'not_collected';
  return v.freshness === 'current' && now >= Date.parse(v.input.end) && now - Date.parse(v.input.end) <= v.target.maxAgeSeconds * 1000 ? 'current' : 'stale';
}
