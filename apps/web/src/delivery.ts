import type { BrowserAccess } from './api';
import type { CollectionExecution } from './collections';
import { isHex } from './graphs';

export interface DeliveryInput { runId: string; taskId: string; artifactDigest: string; baseBranch: string; title: string; description: string }
export interface ProviderCheck { id: string; name: string; commit: string; state: string }
export interface ProviderDeployment { id: string; repositoryId: string; providerProfile: string; commit: string; commitRelation: string; environment: string; environmentId?: string; productionEnvironment?: boolean; state: string; providerState: string; statusId?: string; statusTruncated: boolean; providerUpdatedAt: string; observedAt: string }
export interface DeliveryObservation { sequence: number; providerId: string; number: number; url: string; commit: string; tree: string; state: string; draft: boolean; mergeCommit?: string; checks: ProviderCheck[]; checksTruncated: boolean; checksState: string; deployment: string; deployments: ProviderDeployment[]; deploymentsTruncated: boolean; productionOutcome: string; observedAt: string; trigger?: {kind: string; key?: string; eventType?: string; payloadDigest?: string} }
export interface Delivery { id: string; workspaceId: string; repositoryId: string; proposerId: string; input: DeliveryInput; digest: string; target: {workspaceId: string; repositoryId: string; provider: string; host: string; providerId: string; locator: string; profile: string; integrationVersion: number}; baseCommit: string; baseTree: string; resultTree: string; patchDigest: string; branch: string; createdAt: string; authorization?: {actor: string; digest: string; createdAt: string}; receipt?: {digest: string; observation: DeliveryObservation; createdAt: string}; observation?: DeliveryObservation; execution?: CollectionExecution }
export interface DeliveryPage { deliveries: {id: string; workspaceId: string; repositoryId: string; digest: string; title: string; authorized: boolean; state: string; createdAt: string}[]; nextBefore?: string }
export interface ExecutionEvidence { id: string; repositoryId: string; argv: string[]; state: string; exitCode?: number; output?: string; outputDigest: string; truncated: boolean; startedAt: string; finishedAt: string; sourceDigest: string }
export interface ExecutionPatch { repositoryId: string; baseCommit: string; baseTree: string; resultTree: string; patch: string; digest: string; paths: string[] }
export interface ExecutionArtifact { cleanupConfirmed: boolean; inputDigest: string; profileDigest: string; image: string; adapter: string; adapterVersion: string; producer: ExecutionEvidence; patches: ExecutionPatch[]; checks: ExecutionEvidence[]; startedAt: string; finishedAt: string }
export interface DeliveryArtifact { deliveryId: string; deliveryDigest: string; artifactDigest: string; artifact: ExecutionArtifact }
export interface InspectedExecutionArtifact { artifact: ExecutionArtifact; decodedPatches: {metadata: ExecutionPatch; bytes: Uint8Array; text: string}[] }
export interface InspectedArtifact extends DeliveryArtifact, InspectedExecutionArtifact {}
export const deliveryPath = (id: string) => `/repository-deliveries/${encodeURIComponent(id)}`;
const date = (v: unknown) => typeof v === 'string' && Number.isFinite(Date.parse(v));
export function validateDelivery(value: Delivery, access: BrowserAccess, id?: string) {
  if(!value||!isHex(value.id,32)||id!==undefined&&value.id!==id||value.workspaceId!==access.workspaceID||value.repositoryId!==access.repositoryID||!isHex(value.digest,64)||!value.input||!isHex(value.input.runId,32)||!isHex(value.input.taskId,32)||!isHex(value.input.artifactDigest,64)||typeof value.input.title!=='string'||typeof value.input.description!=='string'||typeof value.input.baseBranch!=='string'||!value.target||value.target.workspaceId!==access.workspaceID||value.target.repositoryId!==access.repositoryID||!['github','gitlab'].includes(value.target.provider)||value.target.host!==value.target.provider+'.com'||!isHex(value.baseCommit,40)||!isHex(value.baseTree,40)||!isHex(value.resultTree,40)||!isHex(value.patchDigest,64)||typeof value.branch!=='string'||!date(value.createdAt))throw new Error('The delivery does not match the selected scope or exact artifact.');
  if(value.authorization!==undefined&&(!value.authorization||value.authorization.digest!==value.digest||typeof value.authorization.actor!=='string'||!date(value.authorization.createdAt)))throw new Error('The publication authorization is incomplete.');
  for(const o of [value.receipt?.observation,value.observation])if(o){o.checks=o.checks??[];o.deployments=o.deployments??[];if(!isHex(o.commit,40)||o.tree!==value.resultTree||typeof o.state!=='string'||typeof o.checksState!=='string'||!Array.isArray(o.checks)||o.checks.length>200||o.checks.some(c=>!c||c.commit!==o.commit||typeof c.name!=='string'||typeof c.state!=='string')||typeof o.checksTruncated!=='boolean'||!Array.isArray(o.deployments)||o.deployments.length>100||o.deployments.some(d=>!d||d.repositoryId!==value.repositoryId||d.providerProfile!==value.target.profile||!isHex(d.commit,40)||typeof d.environment!=='string'||typeof d.state!=='string'||!date(d.observedAt))||typeof o.deploymentsTruncated!=='boolean'||!date(o.observedAt))throw new Error('Provider observations are incomplete or do not match the delivery.');}
}
export function validateDeliveryPage(page: DeliveryPage, access: BrowserAccess) {
  if(!page||!Array.isArray(page.deliveries)||page.deliveries.length>20||page.deliveries.some(d=>!d||!isHex(d.id,32)||!isHex(d.digest,64)||d.workspaceId!==access.workspaceID||d.repositoryId!==access.repositoryID||typeof d.title!=='string'||typeof d.authorized!=='boolean'||typeof d.state!=='string'||!date(d.createdAt))||new Set(page.deliveries.map(d=>d.id)).size!==page.deliveries.length||page.nextBefore!==undefined&&(typeof page.nextBefore!=='string'||page.nextBefore.length>1024))throw new Error('The delivery list is incomplete. Refresh before inspecting.');
}
export function providerURL(url: string, host: string): string | undefined { try{const value=new URL(url);if(value.protocol==='https:'&&value.hostname===host&&!value.username&&!value.password&&!value.port)return value.href;}catch{/* Display an unavailable link for invalid provider data. */} }
const digestBytes = async (bytes: Uint8Array) => Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes as BufferSource)),b=>b.toString(16).padStart(2,'0')).join('');
export { visibleControls } from './sourceText';

// The server verifies the typed immutable artifact digest under all run grants.
// Also verify every displayed patch/check byte digest locally before enabling
// publication. The larger artifact response is never silently sliced for review.
export async function inspectArtifact(value: DeliveryArtifact, selected: Delivery): Promise<InspectedArtifact> {
  if(!value||value.deliveryId!==selected.id||value.deliveryDigest!==selected.digest||value.artifactDigest!==selected.input.artifactDigest||!value.artifact)throw new Error('The artifact does not match the inspected publication proposal.');
  const inspected=await inspectExecutionArtifact(value.artifact);
  const {artifact:a,decodedPatches:decoded}=inspected;
  const p=decoded.find(p=>p.metadata.repositoryId===selected.repositoryId)?.metadata;
  if(!p||p.digest!==selected.patchDigest||p.baseCommit!==selected.baseCommit||p.baseTree!==selected.baseTree||p.resultTree!==selected.resultTree||new Set(a.patches.map(p=>p.repositoryId)).size!==a.patches.length)throw new Error('The artifact lacks the exact target repository patch.');
  return {...value,...inspected};
}

// Failed attempts and read-only design reports use the same complete byte
// validation as delivery, without publication eligibility or a required patch.
export async function inspectExecutionArtifact(value: ExecutionArtifact): Promise<InspectedExecutionArtifact> {
  const a=value;
  a.patches=a.patches??[];a.checks=a.checks??[];
  if(!isHex(a.inputDigest,64)||!isHex(a.profileDigest,64)||typeof a.image!=='string'||typeof a.adapter!=='string'||typeof a.adapterVersion!=='string'||typeof a.cleanupConfirmed!=='boolean'||!a.producer||typeof a.producer.state!=='string'||!Array.isArray(a.patches)||a.patches.length>16||!Array.isArray(a.checks)||a.checks.length>32||!date(a.startedAt)||!date(a.finishedAt))throw new Error('Implementation evidence is incomplete.');
  const decoded:InspectedArtifact['decodedPatches']=[];
  for(const p of a.patches){
    if(!p||typeof p.repositoryId!=='string'||!isHex(p.baseCommit,40)||!isHex(p.baseTree,40)||!isHex(p.resultTree,40)||!isHex(p.digest,64)||!Array.isArray(p.paths)||p.paths.length>10000||p.paths.some(path=>typeof path!=='string')||typeof p.patch!=='string'||p.patch.length>12*1024*1024)throw new Error('A patch is incomplete or exceeds its bounds.');
    const bytes=Uint8Array.from(atob(p.patch),c=>c.charCodeAt(0));if(bytes.length>8*1024*1024||await digestBytes(bytes)!==p.digest)throw new Error('A displayed patch does not match its retained byte digest.');
    decoded.push({metadata:p,bytes,text:new TextDecoder('utf-8',{fatal:true}).decode(bytes)});
  }
  for(const c of [a.producer,...a.checks]){if(!c||typeof c.id!=='string'||typeof c.repositoryId!=='string'||!Array.isArray(c.argv??[])||(c.argv??[]).some(arg=>typeof arg!=='string')||typeof c.state!=='string'||typeof c.truncated!=='boolean'||!isHex(c.outputDigest,64)||!isHex(c.sourceDigest,64)||c.output!==undefined&&typeof c.output!=='string'||await digestBytes(new TextEncoder().encode(c.output??''))!==c.outputDigest)throw new Error('A check does not match its retained output digest.');}
  return {artifact:a,decodedPatches:decoded};
}

export function publicationEvidenceReady(a: InspectedArtifact, repositoryID: string) { return a.artifact.cleanupConfirmed && [a.artifact.producer,...a.artifact.checks].every(c=>c.state==='passed'&&c.exitCode===0&&!c.truncated) && a.artifact.checks.some(c=>c.repositoryId===repositoryID); }
