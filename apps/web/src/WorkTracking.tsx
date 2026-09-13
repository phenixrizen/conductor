import { useWorkflowActivity } from './workflowActivity';
import { useEffect, useRef, useState } from 'react';
import { APIError, accessFailure, dateLabel, errorMessage, request } from './api';
import type { BrowserAccess } from './api';
import { sameJSON, uncertainMutation } from './collections';
import { isHex } from './graphs';
import { providerURL } from './delivery';
import { visibleControls } from './sourceText';
import { parseStrictJSON } from './strictJSON';
import { clearTrackerLinkHint, readTrackerLinkHint } from './trackerLinkHint';
import { currentTrackerObservation, trackerInput, trackerPath, validateTrackerLink, validateTrackerPage, validateTrackerSettings, validateTrackerSync } from './tracking';
import type { TrackerLink, TrackerLinkInput, TrackerObservation, TrackerPage, TrackerSettings, TrackerSync, TrackerSyncInput } from './tracking';

type Job={sequence:number;signal:AbortSignal};
type Creation={input:TrackerLinkInput;key:string;outcome:'pending'|'recorded'|'uncertain'};
type Decision={linkID:string;input:TrackerSyncInput;key:string;uncertain:boolean};
export function WorkTracking({access,visible=true,onAccessFailure}:{access:BrowserAccess;visible?:boolean;onAccessFailure?:(failure:APIError)=>void}){
 const [settings,setSettings]=useState<TrackerSettings>();
 const [page,setPage]=useState<TrackerPage>();
 const [link,setLink]=useState<TrackerLink>();
 const [sync,setSync]=useState<TrackerSync>();
 const [id,setID]=useState(readTrackerLinkHint);
 useEffect(()=>clearTrackerLinkHint(id),[]);
 const [draft,setDraft]=useState('');
 const [preview,setPreview]=useState<TrackerLinkInput>();
 const [creation,setCreation]=useState<Creation>();
 const [decision,setDecision]=useState<Decision>();
 const [needsInspection,setNeedsInspection]=useState(false);
 const [pending,setPending]=useState(''),[notice,setNotice]=useState(''),[error,setError]=useState('');
 const [now,setNow]=useState(Date.now());
 const working=useRef(false),sequence=useRef(0),controller=useRef<AbortController|undefined>(undefined);
 const decisionPending = useRef(false);
 const busy=!!pending,locked=creation?.outcome==='pending'||creation?.outcome==='uncertain';
 const canSync=access.canAuthor&&settings?.enabled&&settings.canSync;
 const canResolve=canSync&&access.principalKind==='human'&&settings?.canResolve;
 const completed=!!link?.observation&&link.latestSyncId===link.observation.syncId;
 const current=!!link&&currentTrackerObservation(link.observation,now)&&!!link.observation?.issue&&(link.observation.projectionDigest===''||isHex(link.observation.projectionDigest,64));
  const workflowActive = useWorkflowActivity(visible, () => {
    sequence.current++; controller.current?.abort(); working.current = false; setPending('');
    setCreation(value => value?.outcome === 'pending' ? { ...value, outcome: 'uncertain' } : value);
    const interrupted = decisionPending.current; decisionPending.current = false;
    setDecision(value => value && (value.uncertain || interrupted) ? { ...value, uncertain: true } : undefined);
  });

 useEffect(()=>{const timer=setInterval(()=>setNow(Date.now()),5000);return()=>{clearInterval(timer);sequence.current++;controller.current?.abort();};},[]);
 function begin(label:string):Job|undefined{if(!workflowActive.current||working.current)return;working.current=true;controller.current?.abort();controller.current=new AbortController();setPending(label);setError('');setNotice('');return{sequence:++sequence.current,signal:controller.current.signal};}
 function active(job:Job){return workflowActive.current&&sequence.current===job.sequence&&!job.signal.aborted;}
 function finish(job:Job){if(active(job)){working.current=false;decisionPending.current=false;setPending('');}}
 function fail(job:Job,failure:unknown){if(!active(job))return;if(failure instanceof APIError&&[401,403,404].includes(failure.status)){
  // Invalidate first so an uncertain result cannot restore private issue text,
  // old source pins, or a captured conflict resolution after access denial.
  sequence.current++;controller.current?.abort();working.current=false;setPending('');setSettings(undefined);setPage(undefined);setLink(undefined);setSync(undefined);setID('');setDraft('');setPreview(undefined);setCreation(undefined);setDecision(undefined);setNeedsInspection(true);
  if(accessFailure(failure,access)){onAccessFailure?.(failure);return;}
 }setError(visibleControls(errorMessage(failure)));}
 async function refresh(more=false){const job=begin('Loading workspace tracker and linked work…');if(!job)return;setSettings(undefined);if(!decision?.uncertain)setDecision(undefined);
  try{const q=new URLSearchParams({limit:'20'});if(more&&page?.next)q.set('before',page.next);const [config,links]=await Promise.all([request<TrackerSettings>('/tracker',access,job.signal),request<TrackerPage>(`/tracker-links?${q}`,access,job.signal)]);if(!active(job))return;validateTrackerSettings(config,access);validateTrackerPage(links,access);setSettings(config);setPage(links);}
  catch(failure){fail(job,failure);}finally{finish(job);}
 }
 async function inspect(selectedID:string){if(!isHex(selectedID,32)){setError('Use the complete 32-character tracker link ID.');return;}if(decision?.uncertain)return;const job=begin('Inspecting linked package revisions and tracker observations…');if(!job)return;setLink(undefined);setSync(undefined);setDecision(undefined);setNeedsInspection(true);
  try{const value=await request<TrackerLink>(trackerPath(selectedID),access,job.signal);if(!active(job))return;validateTrackerLink(value,access,selectedID);setLink(value);setID(value.id);setNeedsInspection(false);setNotice('Tracker link inspected. Ticket fields cannot establish approval, passing checks or completed delivery.');}catch(failure){fail(job,failure);}finally{finish(job);}
 }
 function parse(text:string){if(new TextEncoder().encode(text).length>65536)throw new Error('Link JSON exceeds 64 KiB.');return trackerInput(parseStrictJSON(text),access);}
 function previewDraft(){try{setPreview(parse(draft));setCreation(undefined);setError('');}catch(failure){setPreview(undefined);setError(errorMessage(failure));}}
 async function importFile(file?:File){if(!file||busy||locked)return;const job=begin('Reading selected tracker link JSON…');if(!job)return;setPreview(undefined);
  try{if(file.size>65536)throw new Error('Link JSON file exceeds 64 KiB.');const bytes=await file.arrayBuffer();if(!active(job))return;const text=new TextDecoder('utf-8',{fatal:true}).decode(bytes),value=parse(text);setDraft(text);setPreview(value);setCreation(undefined);setNotice('File imported for preview. No ticket link or synchronization has been recorded.');}catch(failure){fail(job,failure);}finally{finish(job);}
 }
 async function create(){if(!canSync||!preview||creation?.outcome==='recorded'||decision?.uncertain)return;const captured:Creation=creation?.outcome==='uncertain'?creation:{input:preview,key:crypto.randomUUID(),outcome:'pending'};const job=begin('Linking exact work to the selected tracker issue…');if(!job)return;setCreation({...captured,outcome:'pending'});setDecision(undefined);setSync(undefined);
  try{const value=await request<TrackerLink>('/tracker-links',access,job.signal,captured.input,{idempotencyKey:captured.key});if(!active(job))return;validateTrackerLink(value,access);if(value.creatorId!==access.principalID||!sameJSON(trackerInput(value.input,access),captured.input))throw new Error('The returned link differs from the preview. Its outcome is unknown.');setCreation({...captured,outcome:'recorded'});setLink(value);setID(value.id);setPage(undefined);setNeedsInspection(false);setNotice('Shared tracker link recorded; its initial refresh is queued. Inspect later for retained tracker facts.');}
  catch(failure){fail(job,failure);if(active(job))setCreation(uncertainMutation(failure)?{...captured,outcome:'uncertain'}:undefined);}finally{finish(job);}
 }
 function prepare(mode:TrackerSyncInput['mode']){if(!link||!canSync||needsInspection||!completed||decision?.uncertain||mode!=='refresh'&&!current||mode==='restore'&&!canResolve)return;setDecision({linkID:link.id,key:crypto.randomUUID(),uncertain:false,input:{linkDigest:link.digest,mode,...(mode!=='refresh'&&link.observation!.projectionDigest?{expectedProjectionDigest:link.observation!.projectionDigest}:{})}});}
 async function confirm(){const captured=decision,selected=link;if(!captured||!selected||!canSync||needsInspection||captured.linkID!==selected.id||captured.input.linkDigest!==selected.digest||captured.input.mode==='restore'&&!canResolve)return;const job=begin('Recording the inspected tracker synchronization…');if(!job)return;decisionPending.current=true;
  try{
   // Preserve the displayed link and provider projection digests. No tracker,
   // package, permission or repository refresh occurs inside this decision.
   const value=await request<TrackerSync>(`${trackerPath(captured.linkID)}/syncs`,access,job.signal,captured.input,{idempotencyKey:captured.key});if(!active(job))return;validateTrackerSync(value,selected);if(value.actorId!==access.principalID||!sameJSON(value.input,captured.input))throw new Error('The synchronization request outcome does not match the inspected decision.');setSync(value);setDecision(undefined);setNeedsInspection(true);setNotice('Synchronization request recorded. Inspect its retained result and refresh the link before another decision.');
  }catch(failure){fail(job,failure);if(active(job)){if(uncertainMutation(failure))setDecision({...captured,uncertain:true});else{setDecision(undefined);setNeedsInspection(true);}}}finally{finish(job);}
 }
 async function inspectSync(){const selected=link,syncID=sync?.id??link?.latestSyncId;if(!selected||!syncID)return;const job=begin('Inspecting the retained synchronization result…');if(!job)return;
  try{const value=await request<TrackerSync>(`/tracker-syncs/${encodeURIComponent(syncID)}`,access,job.signal);if(!active(job))return;validateTrackerSync(value,selected,syncID);setSync(value);setNotice(value.observation?'Synchronization result inspected. Refresh the link before another decision.':'Synchronization outcome is unknown; no retained observation is available.');}catch(failure){fail(job,failure);}finally{finish(job);}
 }
 return <section className="work-tracking panel" aria-labelledby="tracking-title" aria-busy={busy}>
  <div className="section-heading"><div><p className="eyebrow">SHARED PLANNING AND DELIVERY LINKS</p><h2 id="tracking-title">Workspace tracker</h2></div><button className="secondary" disabled={busy} onClick={()=>void refresh()}>Refresh tracker and links</button></div>
  <p>Each workspace uses one tracker: Linear or Jira. Link existing tickets to exact package revisions and publication receipts; synchronization keeps ticket-owned planning fields separate from Conductor approvals and delivery evidence.</p>
  <div role="status" aria-live="polite">{pending||notice}</div>{error&&<p role="alert" className="error">{error}</p>}
  {settings?<section aria-label="Configured workspace tracker"><p><strong>{settings.provider==='linear'?'Linear':'Jira'}</strong> · {settings.host} · {settings.enabled?'Enabled':'Disabled'} · configuration {settings.version}</p><p>{canSync?'You may request synchronization.':'Synchronization controls are unavailable for this identity.'}</p><details><summary>Ticket status mappings</summary><ul>{settings.statuses.map(s=><li key={s.id}>{s.id} → {visibleControls(s.display)} · {s.ignore?'Intentionally unsynchronized':'Display mapping only'}</li>)}</ul></details></section>:<p className="muted">Refresh to discover the configured tracker and your access.</p>}
  {page&&<><ul className="record-list shared-list" aria-label="Shared tracker links">{page.links.map(l=><li key={l.id}><button className="shared-card" disabled={busy||decision?.uncertain} onClick={()=>void inspect(l.id)} aria-label={`Inspect tracker link ${l.id}`}><strong>{l.input.issueId}</strong><span>{l.input.packages.length} package revisions · {l.observation?.state??'Initial refresh outcome unknown'}</span><code>{l.digest}</code></button></li>)}</ul>{page.links.length===0&&<p>No tracker links are available in this scope.</p>}{page.truncated&&<><p className="warning">More tracker links exist. This page is bounded to 20.</p><button className="secondary" disabled={busy} onClick={()=>void refresh(true)}>Load more tracker links</button></>}</>}
  <form className="control-row" onSubmit={e=>{e.preventDefault();void inspect(id);}}><label htmlFor="tracker-link-id">Tracker link ID<input id="tracker-link-id" value={id} maxLength={32} disabled={busy||decision?.uncertain} onChange={e=>setID(e.target.value)}/></label><button className="secondary" disabled={busy||decision?.uncertain||!isHex(id,32)}>Inspect shared tracker link</button></form>
  {canSync&&<details open={creation?.outcome==='uncertain'||undefined}><summary>Link existing ticket to exact work</summary><p>Import or paste a JSON request containing issueId, packages (repositoryId, packageId, revision, digest), and optional publications (id, receipt digest). Use the tracker's stable issue ID. This does not create a ticket or change its status.</p><label htmlFor="tracker-link-file">Tracker link JSON file<input id="tracker-link-file" type="file" accept=".json,application/json" disabled={busy||locked||decision?.uncertain} onChange={e=>{void importFile(e.target.files?.[0]);e.target.value='';}}/></label><label htmlFor="tracker-link-json">Tracker link JSON<textarea id="tracker-link-json" rows={7} value={draft} maxLength={65536} disabled={busy||locked||decision?.uncertain} onChange={e=>{setDraft(e.target.value);setPreview(undefined);setCreation(undefined);}}/></label><button className="secondary" disabled={busy||locked||!draft||decision?.uncertain} onClick={previewDraft}>Preview tracker link</button>
   {preview&&<section aria-label="Tracker link preview"><h3>Exact linked work preview</h3><LinkPins input={preview}/><button disabled={busy||creation?.outcome==='recorded'||decision?.uncertain} onClick={()=>void create()}>{creation?.outcome==='uncertain'?'Retry exact tracker link':'Record tracker link'}</button></section>}
   {creation?.outcome==='uncertain'&&<p className="warning">Link outcome unknown. The exact input and key are retained for explicit retry.</p>}{creation?.outcome==='recorded'&&<button className="secondary" disabled={busy||decision?.uncertain} onClick={()=>{setCreation(undefined);setPreview(undefined);setDraft('');}}>Start another tracker link</button>}
  </details>}
  {link&&<article aria-label="Inspected tracker link"><div className="section-heading"><h3>Linked issue {link.input.issueId}</h3><button className="secondary" disabled={busy||decision?.uncertain} onClick={()=>void inspect(link.id)}>Refresh inspected tracker link</button></div><dl><dt>Link</dt><dd><code>{link.id}</code></dd><dt>Link digest</dt><dd><code>{link.digest}</code></dd><dt>Created by</dt><dd>{link.creatorId} · {dateLabel(link.createdAt)}</dd><dt>Configuration version</dt><dd>{link.configVersion}</dd></dl><LinkPins input={link.input}/>
   <section aria-label="Conductor-owned ticket link"><h4>Conductor-owned ticket link</h4><p>{visibleControls(link.projection.title)}</p><pre>{visibleControls(link.projection.summary)}</pre><p><code>{link.projection.url}</code></p><p className="muted">Conductor owns this attachment or remote link. The tracker owns title, description, priority, assignee and ticket status.</p></section>
   {link.observation?<TrackerFacts observation={link.observation} settings={settings} now={now}/>:<p className="warning">Tracker facts unavailable. Initial synchronization has no retained outcome.</p>}
   {link.publications.length>0&&<details><summary>Linked repository publication evidence ({link.publications.length})</summary><p>Each receipt describes its own repository. One merged PR/MR cannot complete a cross-repository ticket.</p>{link.publications.map(p=><section key={p.id}><h4>{p.target.provider} · {p.target.repositoryId}</h4><p>Receipt <code>{p.digest}</code> · {p.current?'Provider observation marked current at inspection':'Provider observation stale or unavailable'}</p><pre>{visibleControls(JSON.stringify(p.observation??p.receipt.observation,null,2))}</pre></section>)}</details>}
   {(!completed||needsInspection)&&<p className="warning">Inspect the retained synchronization outcome and refresh this link before another synchronization decision.</p>}
   {(sync||link.latestSyncId)&&<button className="secondary" disabled={busy||decision?.uncertain} onClick={()=>void inspectSync()}>Inspect latest tracker synchronization</button>}
   {sync&&<section aria-label="Inspected tracker synchronization"><h4>Synchronization {sync.input.mode}</h4><p><code>{sync.id}</code> · dispatch {sync.dispatch} · {dateLabel(sync.createdAt)}</p>{sync.observation?<TrackerFacts observation={sync.observation} settings={settings} now={now}/>:<p className="warning">Outcome unknown. Dispatch does not prove synchronized tracker state.</p>}</section>}
   {canSync&&<div className="control-row"><button className="secondary" disabled={busy||needsInspection||!completed||!!decision} onClick={()=>prepare('refresh')}>Refresh tracker-owned fields</button><button disabled={busy||needsInspection||!completed||!current||!!decision||link.observation?.state==='conflict'} onClick={()=>prepare('publish')}>Publish Conductor link</button>{canResolve&&<button className="secondary" disabled={busy||needsInspection||!completed||!current||!!decision||link.observation?.state!=='conflict'} onClick={()=>prepare('restore')}>Resolve inspected link conflict</button>}</div>}
  </article>}
  {decision&&<section role={decision.uncertain ? "region" : "dialog"} aria-modal={decision.uncertain ? undefined : "false"} aria-label={decision.uncertain ? "Uncertain tracker synchronization" : "Confirm tracker synchronization"} className="warning"><h3>{decision.input.mode==='restore'?'Restore the inspected Conductor-owned link':decision.input.mode==='publish'?'Publish the inspected Conductor link':'Refresh tracker-owned planning fields'}</h3><p>Link <code>{decision.linkID}</code> · digest <code>{decision.input.linkDigest}</code></p>{decision.input.mode!=='refresh'&&<p>Inspected provider projection: <code>{decision.input.expectedProjectionDigest??'Absent in the inspected provider snapshot'}</code></p>}<p>{decision.input.mode==='restore'?'This human decision replaces the conflicting attachment or remote link with the displayed Conductor-owned projection. It does not change tracker-owned planning fields.':'The worker will perform this scoped synchronization under current permissions. Ticket fields never grant approval or prove delivery.'}</p>{decision.uncertain&&<p>Request outcome unknown. Explicit retry preserves the same inspected input and key.</p>}<button disabled={busy} onClick={()=>void confirm()}>{decision.uncertain?'Retry exact tracker synchronization':'Confirm tracker synchronization'}</button>{!decision.uncertain&&<button className="secondary" disabled={busy} onClick={()=>setDecision(undefined)}>Dismiss tracker decision</button>}</section>}
 </section>;
}
function LinkPins({input}:{input:TrackerLinkInput}){return <><p>Issue ID: {input.issueId}</p><ul className="record-list">{input.packages.map(p=><li key={p.packageId}><strong>{p.repositoryId}</strong> · {p.packageId} · revision {p.revision}<br/><code>{p.digest}</code></li>)}</ul>{input.publications?.length?<ul>{input.publications.map(p=><li key={p.id}>Publication {p.id} · receipt <code>{p.digest}</code></li>)}</ul>:<p>No publication receipts linked.</p>}</>;}
function TrackerFacts({observation:o,settings,now}:{observation:TrackerObservation;settings?:TrackerSettings;now:number}){const issue=o.issue,url=issue&&settings?providerURL(issue.url,settings.host):undefined;return <section aria-label="Tracker observation"><h4>{currentTrackerObservation(o,now)?'Retained tracker observation':'Stale or uncertain tracker observation'} · {o.state}</h4><p>{dateLabel(o.observedAt)} · {o.code??'No additional diagnostic'}</p><p>Provider projection digest <code>{o.projectionDigest||'Unavailable'}</code></p>{issue?<><p>{url?<a href={url} target="_blank" rel="noopener noreferrer">Inspect ticket {issue.key}</a>:issue.key} · {visibleControls(issue.title)}</p><dl><dt>Tracker status</dt><dd>{visibleControls(issue.statusName)} · {issue.mapping} · {visibleControls(issue.mappedStatus)}</dd><dt>Priority / assignee</dt><dd>{visibleControls(issue.priority)} / {visibleControls(issue.assigneeId||'Unassigned')}</dd><dt>Tracker updated</dt><dd>{dateLabel(issue.updatedAt)}</dd></dl><details><summary>Tracker-owned description</summary><pre>{visibleControls(typeof issue.description==='string'?issue.description:JSON.stringify(issue.description,null,2))}</pre></details></>:<p className="warning">Issue fields unavailable; ticket status is unknown.</p>}{o.projection&&<details><summary>Observed provider attachment or remote link</summary><pre>{visibleControls(JSON.stringify(o.projection,null,2))}</pre></details>}<p className="muted">Ticket status is planning context. It does not establish effective approval, passing checks, repository merge, deployment or production outcome.</p></section>;}
