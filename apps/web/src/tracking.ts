import type { BrowserAccess } from './api';
import type { Delivery } from './delivery';
import { isHex } from './graphs';

export interface TrackerSettings {workspaceId:string;provider:'linear'|'jira';host:string;scopeId:string;profile:string;version:number;enabled:boolean;statuses:{id:string;display:string;ignore:boolean}[];canSync:boolean;canResolve:boolean}
export interface TrackerLinkInput {issueId:string;packages:{repositoryId:string;packageId:string;revision:number;digest:string}[];publications?:{id:string;digest:string}[]}
export interface TrackerProjection {url:string;title:string;summary:string}
export interface TrackerIssue {id:string;key:string;url:string;title:string;description:unknown;priority:string;assigneeId:string;statusId:string;statusName:string;mappedStatus:string;mapping:string;updatedAt:string}
export interface TrackerObservation {syncId:string;state:string;code?:string;issue?:TrackerIssue;projection?:TrackerProjection;projectionDigest:string;observedAt:string;current:boolean}
export interface TrackerPublication {id:string;digest:string;target:Delivery['target'];receipt:NonNullable<Delivery['receipt']>;observation?:Delivery['observation'];current:boolean}
export interface TrackerLink {id:string;workspaceId:string;creatorId:string;configVersion:number;input:TrackerLinkInput;digest:string;projection:TrackerProjection;createdAt:string;observation?:TrackerObservation;latestSyncId?:string;publications:TrackerPublication[]}
export interface TrackerPage {links:TrackerLink[];next?:string;truncated:boolean}
export interface TrackerSyncInput {linkDigest:string;mode:'refresh'|'publish'|'restore';expectedProjectionDigest?:string}
export interface TrackerSync {id:string;linkId:string;actorId:string;input:TrackerSyncInput;createdAt:string;observation?:TrackerObservation;dispatch:string}
export const trackerPath=(id:string)=>`/tracker-links/${encodeURIComponent(id)}`;
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const keys=(v:Record<string,unknown>,allowed:string[])=>Object.keys(v).every(k=>allowed.includes(k));
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&v.length>0&&new TextEncoder().encode(v).length<=max&&!v.includes('\0');
const date=(v:unknown)=>typeof v==='string'&&Number.isFinite(Date.parse(v));
export function validateTrackerSettings(v:TrackerSettings,access:BrowserAccess){
  if(!v||v.workspaceId!==access.workspaceID||!['linear','jira'].includes(v.provider)||!(v.provider==='linear'?v.host==='linear.app':/^[a-z0-9][a-z0-9-]{0,62}\.atlassian\.net$/.test(v.host))||!text(v.profile,128)||!text(v.scopeId,64)||!Number.isSafeInteger(v.version)||v.version<1||typeof v.enabled!=='boolean'||typeof v.canSync!=='boolean'||typeof v.canResolve!=='boolean'||!Array.isArray(v.statuses)||v.statuses.length>100||v.statuses.some(s=>!s||!text(s.id,64)||!text(s.display,128)||typeof s.ignore!=='boolean'))throw new Error('Workspace tracker settings are incomplete. Refresh access before synchronization.');
}
// The server command owns validation and authorization. This strict preview
// rejects ambiguous fields, preserves exact pins and applies Go's UTF-8 order.
export function trackerInput(value:unknown,access:BrowserAccess):TrackerLinkInput{
 if(!object(value)||!keys(value,['issueId','packages','publications'])||!text(value.issueId,64)||!Array.isArray(value.packages)||value.packages.length<1||value.packages.length>16||value.publications!==undefined&&!Array.isArray(value.publications))throw new Error('Provide an issue ID, 1–16 exact package references and optional publication receipt references.');
 if(value.packages.some(p=>!object(p)||!keys(p,['repositoryId','packageId','revision','digest'])||!text(p.repositoryId,128)||!text(p.packageId,128)||!Number.isSafeInteger(p.revision)||Number(p.revision)<1||!isHex(p.digest,64)))throw new Error('Each package needs a repository, ID, revision and exact digest.');
 const input=value as unknown as TrackerLinkInput;
 if(!input.packages.some(p=>p.repositoryId===access.repositoryID)||new Set(input.packages.map(p=>p.packageId)).size!==input.packages.length)throw new Error('Include the selected repository and unique package references.');
 if((input.publications?.length??0)>16||input.publications?.some(p=>!object(p)||!keys(p,['id','digest'])||!isHex(p.id,32)||!isHex(p.digest,64))||new Set(input.publications?.map(p=>p.id)).size!==(input.publications?.length??0))throw new Error('Publication references need unique IDs and exact receipt digests.');
 const compare=(a:string,b:string)=>{const x=new TextEncoder().encode(a),y=new TextEncoder().encode(b);for(let i=0;i<Math.min(x.length,y.length);i++)if(x[i]!==y[i])return x[i]-y[i];return x.length-y.length;};
 return {issueId:input.issueId,packages:[...input.packages].sort((a,b)=>compare(a.packageId,b.packageId)),...(input.publications?.length?{publications:[...input.publications].sort((a,b)=>compare(a.id,b.id))}:{})};
}
const projection=(v:TrackerProjection)=>v&&typeof v.url==='string'&&typeof v.title==='string'&&typeof v.summary==='string';
function observation(v?:TrackerObservation){if(v===undefined)return true;return !!v&&isHex(v.syncId,32)&&['refreshed','conflict','synchronized','unknown','unavailable'].includes(v.state)&&typeof v.current==='boolean'&&date(v.observedAt)&&typeof v.projectionDigest==='string'&&(v.projectionDigest===''||isHex(v.projectionDigest,64))&&(v.projection===undefined||projection(v.projection))&&(v.issue===undefined||!!v.issue&&['id','key','url','title','priority','assigneeId','statusId','statusName','mappedStatus','mapping'].every(k=>typeof v.issue![k as keyof TrackerIssue]==='string')&&'description' in v.issue&&date(v.issue.updatedAt));}
export function validateTrackerLink(v:TrackerLink,access:BrowserAccess,id?:string){
 if(!v||!isHex(v.id,32)||id!==undefined&&v.id!==id||v.workspaceId!==access.workspaceID||!text(v.creatorId,128)||!isHex(v.digest,64)||!Number.isSafeInteger(v.configVersion)||!projection(v.projection)||!date(v.createdAt)||v.latestSyncId!==undefined&&!isHex(v.latestSyncId,32)||!observation(v.observation))throw new Error('The tracker link does not match the selected workspace and repository.');
 trackerInput(v.input,access);v.publications=v.publications??[];
 if(!Array.isArray(v.publications)||v.publications.length>16||v.publications.some(p=>!p||!isHex(p.id,32)||!isHex(p.digest,64)||!v.input.publications?.some(r=>r.id===p.id&&r.digest===p.digest)||p.target?.workspaceId!==access.workspaceID||!v.input.packages.some(r=>r.repositoryId===p.target.repositoryId)||p.receipt?.digest!==p.digest||typeof p.current!=='boolean'))throw new Error('Linked publication evidence is incomplete.');
}
export function validateTrackerPage(v:TrackerPage,access:BrowserAccess){if(!v||!Array.isArray(v.links)||v.links.length>20||typeof v.truncated!=='boolean'||v.next!==undefined&&!isHex(v.next,32)||v.truncated&&!v.next||new Set(v.links.map(l=>l.id)).size!==v.links.length)throw new Error('Tracker link discovery is incomplete.');v.links.forEach(l=>validateTrackerLink(l,access));}
export function validateTrackerSync(v:TrackerSync,link:TrackerLink,id?:string){if(!v||!isHex(v.id,32)||id!==undefined&&v.id!==id||v.linkId!==link.id||!text(v.actorId,128)||!v.input||v.input.linkDigest!==link.digest||!['refresh','publish','restore'].includes(v.input.mode)||!date(v.createdAt)||typeof v.dispatch!=='string'||!observation(v.observation)||v.observation!==undefined&&v.observation.syncId!==v.id)throw new Error('Synchronization does not match the inspected tracker link.');}
export function currentTrackerObservation(o:TrackerObservation|undefined,now:number){return !!o&&o.current&&now-Date.parse(o.observedAt)>=-5000&&now-Date.parse(o.observedAt)<=300000;}
