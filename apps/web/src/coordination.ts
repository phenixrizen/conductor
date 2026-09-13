import type { BrowserAccess } from './api';
import type { CollectionExecution } from './collections';
import { isHex } from './graphs';

export interface PackagePin { changeId: string; repositoryId: string; revision: number; digest: string }
export interface RunRepository { repositoryId: string; commit: string; collectionId: string; receiptDigest: string; fullSourceDigest?: string }
export interface RunTask { id: string; perspective: string; profile: string; profileDigest?: string; image?: string; prompt: string; dependsOn: string[]; scopes: {repositoryId: string; writablePaths: string[]}[]; checks: {id: string; repositoryId: string; argv: string[]; timeoutSeconds: number}[]; timeoutSeconds: number }
export interface RunPlan { schemaVersion: number; graphId: string; graphDigest: string; packages: PackagePin[]; repositories: RunRepository[]; tasks: RunTask[]; maxParallel: number }
export interface TaskReceipt { taskId: string; taskKey: string; digest: string; outcome: string; artifactDigest?: string; createdAt: string }
export interface CoordinationRun { id: string; workspaceId: string; repositoryId: string; proposerId: string; plan: RunPlan; digest: string; createdAt: string; authorization?: {actor: string; digest: string; createdAt: string}; cancelRequestedAt?: string; execution?: CollectionExecution; receipts: TaskReceipt[] }
export interface RunPage { runs: {id: string; workspaceId: string; repositoryId: string; digest: string; createdAt: string; authorized: boolean; tasks: number; receipts: number; execution?: CollectionExecution; cancelRequestedAt?: string}[]; nextBefore?: string }
export interface ExecutionCapabilities { repositoryId: string; canExecute: boolean; canPublish: boolean }
export interface ExecutionProfile { id: string; profileDigest: string; image: string; adapter: string; model?: string; command?: string[]; maxBudgetUsd?: string }
export interface ExecutionProfilePage { profiles: ExecutionProfile[]; truncated: boolean }
export function runPath(id: string) { return `/coordination-runs/${encodeURIComponent(id)}`; }
const record = (v: unknown): v is Record<string, unknown> => v !== null && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max: number): v is string => typeof v === 'string' && v.length > 0 && new TextEncoder().encode(v).length <= max && !v.includes('\0') && new TextDecoder().decode(new TextEncoder().encode(v)) === v;
const integer = (v: unknown, low: number, high: number): v is number => Number.isSafeInteger(v) && Number(v) >= low && Number(v) <= high;
const list = (v: unknown, min: number, max: number): v is unknown[] => Array.isArray(v) && v.length >= min && v.length <= max;
const date = (v: unknown) => typeof v === 'string' && Number.isFinite(Date.parse(v));
const execution = (v: unknown) => v === undefined || record(v) && ['namespace','workflowId','runId','state'].every(k => text(v[k],256)) && date(v.observedAt) && typeof v.current === 'boolean';
const keys = (v: Record<string, unknown>, allowed: string[]) => Object.keys(v).every(k => allowed.includes(k));
const image = (v: unknown) => typeof v === 'string' && v.length <= 512 && /^(?:sha256:|[^\s]+@sha256:)[a-f0-9]{64}$/.test(v);

// This validates the preview's shape and bounds. Domain DAG/scope policy and all
// permission decisions remain on the server; a local preview grants no authority.
export function validatePlan(value: unknown): asserts value is RunPlan {
  if (!record(value) || !keys(value,['schemaVersion','graphId','graphDigest','packages','repositories','tasks','maxParallel']) || value.schemaVersion !== 1 || !isHex(value.graphId,32) || !isHex(value.graphDigest,64) || !integer(value.maxParallel,1,4) || !list(value.repositories,1,16) || !list(value.packages,1,16) || !list(value.tasks,1,32) || new TextEncoder().encode(JSON.stringify(value)).length > 1048576) throw new Error('Use a complete version 1 plan with bounded source/package pins and tasks.');
  if (value.repositories.some(r => !record(r) || !keys(r,['repositoryId','commit','collectionId','receiptDigest','fullSourceDigest']) || !text(r.repositoryId,128) || !isHex(r.commit,40) || !isHex(r.collectionId,32) || !isHex(r.receiptDigest,64) || r.fullSourceDigest !== undefined && !isHex(r.fullSourceDigest,64))) throw new Error('Each source must name an exact repository, commit and receipt digest.');
  const repos = new Set(value.repositories.map(r => (r as RunRepository).repositoryId));
  if (repos.size !== value.repositories.length || value.packages.length !== repos.size || value.packages.some(p => !record(p) || !keys(p,['changeId','repositoryId','revision','digest']) || !text(p.changeId,128) || !repos.has(String(p.repositoryId)) || !integer(p.revision,1,Number.MAX_SAFE_INTEGER) || !isHex(p.digest,64)) || new Set(value.packages.map(p=>(p as PackagePin).repositoryId)).size!==repos.size) throw new Error('Include one exact package revision per source repository.');
  if (value.tasks.some(t => !record(t) || !keys(t,['id','perspective','profile','profileDigest','image','prompt','dependsOn','scopes','checks','timeoutSeconds']) || !text(t.id,64) || !text(t.profile,64) || !['architect','developer','qc','product','operations'].includes(String(t.perspective)) || !text(t.prompt,32768) || !integer(t.timeoutSeconds,1,1800) || t.profileDigest!==undefined&&!isHex(t.profileDigest,64) || t.image!==undefined&&!image(t.image)
    || !list(t.dependsOn ?? [],0,32) || (t.dependsOn as unknown[]|undefined)?.some(d=>!text(d,64)) || !list(t.scopes,1,16)
    || t.scopes.some(s=>!record(s)||!keys(s,['repositoryId','writablePaths'])||!repos.has(String(s.repositoryId))||!list(s.writablePaths ?? [],0,128)||(s.writablePaths as unknown[]|undefined)?.some(p=>!text(p,1024)))
    || !list(t.checks ?? [],0,16) || (t.checks as unknown[]|undefined)?.some(c=>!record(c)||!keys(c,['id','repositoryId','argv','timeoutSeconds'])||!text(c.id,64)||!repos.has(String(c.repositoryId))||!list(c.argv,1,64)||c.argv.some(a=>typeof a!=='string'||new TextEncoder().encode(a).length>4096||a.includes('\0'))||!integer(c.timeoutSeconds,1,1800)))) throw new Error('A task has incomplete scope, profile, dependencies or verification commands.');
  const tasks=value.tasks as unknown as RunTask[];
  if(new Set(tasks.map(t=>t.id)).size!==tasks.length)throw new Error('Task identifiers must be unique.');
}
export function normalizePlan(plan: RunPlan): RunPlan { return {...plan,tasks:plan.tasks.map(t=>({...t,dependsOn:t.dependsOn??[],checks:t.checks??[],scopes:t.scopes.map(s=>({...s,writablePaths:s.writablePaths??[]}))}))}; }
export function validateRun(run: CoordinationRun, access: BrowserAccess, id?: string) {
  if (!run || !isHex(run.id,32) || id!==undefined&&run.id!==id || run.workspaceId!==access.workspaceID || run.repositoryId!==access.repositoryID || !isHex(run.digest,64) || !text(run.proposerId,128) || !date(run.createdAt) || !execution(run.execution)) throw new Error('This run does not match the selected workspace and repository.');
  validatePlan(run.plan);
  if(!run.plan.repositories.some(r=>r.repositoryId===access.repositoryID) || !list(run.receipts,0,32) || run.receipts.some(r=>!r||!isHex(r.taskId,32)||!text(r.taskKey,64)||!run.plan.tasks.some(t=>t.id===r.taskKey)||!isHex(r.digest,64)||!text(r.outcome,64)||r.artifactDigest!==undefined&&!isHex(r.artifactDigest,64)||!date(r.createdAt)) || new Set(run.receipts.map(r=>r.taskKey)).size!==run.receipts.length || run.authorization!==undefined&&(!run.authorization||run.authorization.digest!==run.digest||!text(run.authorization.actor,128)||!date(run.authorization.createdAt)) || run.cancelRequestedAt!==undefined&&!date(run.cancelRequestedAt)) throw new Error('The run receipt or authorization response is incomplete.');
}
export function validateRunPage(page: RunPage, access: BrowserAccess) {
  if(!page||!list(page.runs,0,20)||page.runs.some(r=>!r||!isHex(r.id,32)||!isHex(r.digest,64)||r.workspaceId!==access.workspaceID||r.repositoryId!==access.repositoryID||!date(r.createdAt)||!execution(r.execution)||typeof r.authorized!=='boolean'||!integer(r.tasks,1,32)||!integer(r.receipts,0,32))||new Set(page.runs.map(r=>r.id)).size!==page.runs.length||page.nextBefore!==undefined&&!text(page.nextBefore,1024))throw new Error('The shared run list is incomplete. Refresh before inspecting a run.');
}
export function validateProfiles(page: ExecutionProfilePage) {
  if(!page||!list(page.profiles,0,100)||typeof page.truncated!=='boolean'||page.profiles.some(p=>!p||!text(p.id,64)||!isHex(p.profileDigest,64)||!image(p.image)||!text(p.adapter,64))||new Set(page.profiles.map(p=>p.id)).size!==page.profiles.length)throw new Error('The execution profile catalog is incomplete.');
}
