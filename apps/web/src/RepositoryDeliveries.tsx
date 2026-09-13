import { ExecutionArtifact } from './ExecutionArtifact';
import { useEffect, useRef, useState } from 'react';
import { APIError, accessFailure, dateLabel, errorMessage, request } from './api';
import type { BrowserAccess } from './api';
import { sameJSON, uncertainMutation } from './collections';
import type { ExecutionCapabilities } from './coordination';
import { isHex } from './graphs';
import { deliveryPath, inspectArtifact, providerURL, publicationEvidenceReady, validateDelivery, validateDeliveryPage, visibleControls } from './delivery';
import type { Delivery, DeliveryArtifact, DeliveryInput, DeliveryObservation, DeliveryPage, InspectedArtifact } from './delivery';

type Job = { sequence: number; signal: AbortSignal };
type Proposal = { input: DeliveryInput; key: string; outcome: 'pending' | 'uncertain' | 'recorded' };
type Reconciliation = { id: string; digest: string; key: string; uncertain: boolean };
const emptyInput: DeliveryInput = {runId:'',taskId:'',artifactDigest:'',baseBranch:'main',title:'',description:''};

export function RepositoryDeliveries({access,onAccessFailure}:{access:BrowserAccess;onAccessFailure?:(failure:APIError)=>void}) {
  const [page,setPage]=useState<DeliveryPage>();
  const [delivery,setDelivery]=useState<Delivery>();
  const [artifact,setArtifact]=useState<InspectedArtifact>();
  const [capabilities,setCapabilities]=useState<ExecutionCapabilities>();
  const [id,setID]=useState('');
  const [draft,setDraft]=useState<DeliveryInput>({...emptyInput});
  const [preview,setPreview]=useState<DeliveryInput>();
  const [proposal,setProposal]=useState<Proposal>();
  const [confirmation,setConfirmation]=useState<{id:string;digest:string}>();
  const [reconciliation,setReconciliation]=useState<Reconciliation>();
  const [reviewed,setReviewed]=useState(false);
  const [needsInspection,setNeedsInspection]=useState(false);
  const [pending,setPending]=useState('');
  const [notice,setNotice]=useState('');
  const [error,setError]=useState('');
  const [now,setNow]=useState(Date.now());
  const sequence=useRef(0),working=useRef(false),controller=useRef<AbortController|undefined>(undefined);
  const busy=!!pending;
  const locked=!!proposal&&proposal.outcome!=='recorded';
  const canPublish=access.principalKind==='human'&&access.canAuthor&&capabilities?.canPublish;
  useEffect(()=>{const timer=setInterval(()=>setNow(Date.now()),5000);return()=>{clearInterval(timer);sequence.current++;controller.current?.abort();};},[]);
  function begin(label:string):Job|undefined{if(working.current)return;working.current=true;controller.current?.abort();controller.current=new AbortController();setPending(label);setError('');setNotice('');return{sequence:++sequence.current,signal:controller.current.signal};}
  function current(job:Job){return sequence.current===job.sequence&&!job.signal.aborted;}
  function finish(job:Job){if(current(job)){working.current=false;setPending('');}}
  function clearInspection(){setDelivery(undefined);setArtifact(undefined);setReviewed(false);setConfirmation(undefined);setNeedsInspection(true);}
  function fail(job:Job,failure:unknown){
    if(!current(job))return;
    if(failure instanceof APIError&&[401,403,404].includes(failure.status)){
      // Invalidate the operation before its caller can restore a captured private
      // patch or retry. Scope/session remounts also discard every retained value.
      sequence.current++;controller.current?.abort();working.current=false;setPending('');clearInspection();setPage(undefined);setCapabilities(undefined);setID('');setDraft({...emptyInput});setPreview(undefined);setProposal(undefined);setReconciliation(undefined);
      if(accessFailure(failure,access)){onAccessFailure?.(failure);return;}
    }
    setError(visibleControls(errorMessage(failure)));
  }
  async function refresh(more=false){
    const job=begin('Loading shared deliveries and publication access…');if(!job)return;setCapabilities(undefined);setConfirmation(undefined);
    try{const query=new URLSearchParams({limit:'20'});if(more&&page?.nextBefore)query.set('before',page.nextBefore);
      const [next,caps]=await Promise.all([request<DeliveryPage>(`/repository-deliveries?${query}`,access,job.signal),request<ExecutionCapabilities>('/execution-capabilities',access,job.signal)]);
      if(!current(job))return;validateDeliveryPage(next,access);if(!caps||caps.repositoryId!==access.repositoryID||typeof caps.canPublish!=='boolean')throw new Error('Publication access could not be confirmed.');setPage(next);setCapabilities(caps);
    }catch(failure){fail(job,failure);}finally{finish(job);}
  }
  async function inspect(selectedID:string){
    if(!isHex(selectedID,32)){setError('Use the complete 32-character delivery ID.');return;}
    const job=begin('Inspecting the shared publication proposal…');if(!job)return;clearInspection();
    try{const value=await request<Delivery>(deliveryPath(selectedID),access,job.signal);if(!current(job))return;validateDelivery(value,access,selectedID);setDelivery(value);setID(value.id);setNeedsInspection(false);setNotice('Proposal inspected. Load its complete implementation evidence before publication authorization.');}
    catch(failure){fail(job,failure);}finally{finish(job);}
  }
  async function loadArtifact(){
    const selected=delivery;if(!selected)return;const job=begin('Loading and checking the complete retained artifact…');if(!job)return;setArtifact(undefined);setReviewed(false);setConfirmation(undefined);
    try{const raw=await request<DeliveryArtifact>(`${deliveryPath(selected.id)}/artifact`,access,job.signal);if(!current(job))return;const value=await inspectArtifact(raw,selected);if(!current(job))return;setArtifact(value);setNotice('Complete retained patches and output loaded; inspect every displayed result.');}
    catch(failure){fail(job,failure);}finally{finish(job);}
  }
  function previewInput(){
    const bytes=(v:string)=>new TextEncoder().encode(v).length;
    if(!isHex(draft.runId,32)||!isHex(draft.taskId,32)||!isHex(draft.artifactDigest,64)||!draft.baseBranch||bytes(draft.baseBranch)>128||!draft.title.trim()||bytes(draft.title)>240||bytes(draft.description)>16384||Object.values(draft).some(v=>v.includes('\0'))){setError('Provide complete run/task/artifact IDs, a base branch, a title up to 240 bytes and a description up to 16 KiB.');return;}
    setPreview({...draft});setError('');setProposal(undefined);
  }
  async function propose(){
    if(!access.canAuthor||!preview||proposal?.outcome==='recorded')return;
    const captured:Proposal=proposal?.outcome==='uncertain'?proposal:{input:preview,key:crypto.randomUUID(),outcome:'pending'};
    const job=begin('Recording the exact publication proposal…');if(!job)return;setProposal({...captured,outcome:'pending'});clearInspection();
    try{const value=await request<Delivery>('/repository-deliveries',access,job.signal,captured.input,{idempotencyKey:captured.key});if(!current(job))return;validateDelivery(value,access);if(value.proposerId!==access.principalID||!sameJSON(value.input,captured.input))throw new Error('The returned proposal differs from the inspected input. Its outcome is unknown.');setProposal({...captured,outcome:'recorded'});setDelivery(value);setID(value.id);setPage(undefined);setNeedsInspection(false);setNotice('Publication proposal recorded. No provider publication is established by this response.');}
    catch(failure){fail(job,failure);if(current(job))setProposal(uncertainMutation(failure)?{...captured,outcome:'uncertain'}:undefined);}finally{finish(job);}
  }
  async function authorize(){
    const captured=confirmation;if(!captured||!delivery||!artifact||!reviewed||!canPublish||needsInspection||reconciliation||captured.id!==delivery.id||captured.digest!==delivery.digest||!publicationEvidenceReady(artifact,access.repositoryID))return;
    const job=begin('Authorizing the inspected publication…');if(!job)return;
    try{
      // This sends only the displayed proposal digest. It never reloads a patch,
      // changes a target, or refreshes permission inside the confirmed decision.
      const value=await request<Delivery>(`${deliveryPath(captured.id)}/authorizations`,access,job.signal,{digest:captured.digest});if(!current(job))return;validateDelivery(value,access,captured.id);if(value.digest!==captured.digest||value.authorization?.actor!==access.principalID)throw new Error('The publication decision outcome is unknown.');setDelivery(value);setConfirmation(undefined);setNotice('Publication authorization recorded. The trusted publisher must still produce a provider receipt.');
    }catch(failure){fail(job,failure);if(current(job)){setConfirmation(undefined);setArtifact(undefined);setReviewed(false);setNeedsInspection(true);}}finally{finish(job);}
  }
  async function reconcile(){
    if(!delivery||!canPublish||needsInspection||!delivery.authorization||!delivery.observation)return;
    const captured:Reconciliation=reconciliation??{id:delivery.id,digest:delivery.digest,key:crypto.randomUUID(),uncertain:false};
    if(captured.id!==delivery.id||captured.digest!==delivery.digest)return;
    const job=begin('Requesting a provider observation refresh…');if(!job)return;setReconciliation(captured);setConfirmation(undefined);
    try{const value=await request<Delivery>(`${deliveryPath(captured.id)}/reconciliations`,access,job.signal,{digest:captured.digest},{idempotencyKey:captured.key});if(!current(job))return;validateDelivery(value,access,captured.id);if(value.digest!==captured.digest)throw new Error('The reconciliation result does not match the inspected delivery.');setDelivery(value);setReconciliation(undefined);setNotice('Provider refresh requested. Inspect again later for retained observations; this response does not prove newer provider facts.');}
    catch(failure){fail(job,failure);if(current(job))setReconciliation(uncertainMutation(failure)?{...captured,uncertain:true}:undefined);}finally{finish(job);}
  }
  const evidenceReady=!!artifact&&publicationEvidenceReady(artifact,access.repositoryID);
  return <section className="repository-deliveries panel" aria-labelledby="deliveries-title" aria-busy={busy}>
    <div className="section-heading"><div><p className="eyebrow">SHARED DELIVERY REVIEW</p><h2 id="deliveries-title">Repository deliveries</h2></div><button className="secondary" disabled={busy} onClick={()=>void refresh()}>Refresh deliveries and access</button></div>
    <p>Inspect exact patches and independent checks, then authorize a new draft GitHub PR or GitLab MR. Shared provider observations track checks, merges and deployments separately.</p>
    <div role="status" aria-live="polite">{pending||notice}</div>{error&&<p role="alert" className="error">{error}</p>}
    {!page&&<p className="muted">Refresh to discover shared proposals and your publication access.</p>}{page?.deliveries.length===0&&<p>No deliveries are available in this scope.</p>}
    {capabilities&&<p>{canPublish?'You may authorize inspected publication, subject to current source, approval and repository permissions.':'Publication authorization is unavailable for this identity or repository.'}</p>}
    {page&&<><ul className="shared-list record-list" aria-label="Shared deliveries">{page.deliveries.map(d=><li key={d.id}><button className="shared-card" disabled={busy||!!reconciliation} onClick={()=>void inspect(d.id)} aria-label={`Inspect delivery ${d.id}`}><strong>{visibleControls(d.title)}</strong><span>{d.authorized?'Human authorization recorded':'Awaiting human authorization'} · {d.state}</span><code>{d.digest}</code><span>{dateLabel(d.createdAt)}</span></button></li>)}</ul>{page.nextBefore&&<><p className="warning">More deliveries exist. This page is bounded to 20.</p><button className="secondary" disabled={busy} onClick={()=>void refresh(true)}>Load more deliveries</button></>}</>}
    <form className="control-row" onSubmit={e=>{e.preventDefault();void inspect(id);}}><label htmlFor="delivery-id">Delivery ID<input id="delivery-id" value={id} maxLength={32} disabled={busy||!!reconciliation} onChange={e=>setID(e.target.value)}/></label><button className="secondary" disabled={busy||!!reconciliation||!isHex(id,32)}>Inspect shared delivery</button></form>
    {access.canAuthor&&<details open={proposal?.outcome==='uncertain'||undefined}><summary>Propose publication from a retained task artifact</summary><p>Use the run ID, retained task ID and artifact digest shown in coordinated execution. The server validates actual patch/check evidence before recording the proposal.</p>
      {(Object.keys(emptyInput) as (keyof DeliveryInput)[]).map(key=><label key={key} htmlFor={`delivery-${key}`}>{({runId:'Implementation run ID',taskId:'Retained task ID',artifactDigest:'Implementation artifact digest',baseBranch:'Target base branch',title:'Draft PR or MR title',description:'Draft PR or MR description'})[key]}{key==='description'?<textarea id={`delivery-${key}`} rows={4} value={draft[key]} maxLength={16384} disabled={busy||locked||!!reconciliation} onChange={e=>{setDraft({...draft,[key]:e.target.value});setPreview(undefined);setProposal(undefined);}}/>:<input id={`delivery-${key}`} value={draft[key]} maxLength={key==='title'?240:key==='baseBranch'?128:key==='artifactDigest'?64:32} disabled={busy||locked||!!reconciliation} onChange={e=>{setDraft({...draft,[key]:e.target.value});setPreview(undefined);setProposal(undefined);}}/>}</label>)}
      <button className="secondary" disabled={busy||locked||!!reconciliation} onClick={previewInput}>Preview publication proposal</button>
      {preview&&<section aria-label="Publication proposal preview"><h3>Publication proposal preview</h3><pre>{visibleControls(JSON.stringify(preview,null,2))}</pre><button disabled={busy||proposal?.outcome==='recorded'||!!reconciliation} onClick={()=>void propose()}>{proposal?.outcome==='uncertain'?'Retry exact publication proposal':'Record publication proposal'}</button></section>}
      {proposal?.outcome==='uncertain'&&<p className="warning">Proposal outcome unknown. The exact input and idempotency key are retained for explicit retry.</p>}
      {proposal?.outcome==='recorded'&&<button className="secondary" disabled={busy||!!reconciliation} onClick={()=>{setDraft({...emptyInput});setPreview(undefined);setProposal(undefined);}}>Start another publication proposal</button>}
    </details>}
    {delivery&&<article aria-label="Inspected delivery" className="delivery-inspection"><div className="section-heading"><h3>{visibleControls(delivery.input.title)}</h3><button className="secondary" disabled={busy||!!reconciliation} onClick={()=>void inspect(delivery.id)}>Refresh inspected delivery</button></div><p>{visibleControls(delivery.input.description)}</p><dl><dt>Delivery</dt><dd><code>{delivery.id}</code></dd><dt>Proposal digest</dt><dd><code>{delivery.digest}</code></dd><dt>Provider target</dt><dd>{delivery.target.provider} · {delivery.target.host} · repository {delivery.target.providerId} · {delivery.target.locator}</dd><dt>New branch</dt><dd><code>{delivery.branch}</code> → {delivery.input.baseBranch}</dd><dt>Base commit / tree</dt><dd><code>{delivery.baseCommit}</code><br/><code>{delivery.baseTree}</code></dd><dt>Result tree</dt><dd><code>{delivery.resultTree}</code></dd><dt>Patch digest</dt><dd><code>{delivery.patchDigest}</code></dd><dt>Artifact digest</dt><dd><code>{delivery.input.artifactDigest}</code></dd><dt>Authorization</dt><dd>{delivery.authorization?`${delivery.authorization.actor} · ${dateLabel(delivery.authorization.createdAt)}`:'Not authorized'}</dd></dl>
      <button className="secondary" disabled={busy||needsInspection} onClick={()=>void loadArtifact()}>Inspect complete implementation artifact</button>
      {!artifact&&<p className="warning">Complete patch and check evidence has not been inspected in this view.</p>}
      {artifact&&<section aria-label="Inspected implementation artifact"><h4>Retained implementation evidence</h4><ExecutionArtifact value={artifact}/>
        {!evidenceReady&&<p className="warning">Evidence is incomplete, unverified or truncated. Publication authorization is blocked.</p>}
        {canPublish&&!delivery.authorization&&<label className="checkbox-label"><input type="checkbox" checked={reviewed} disabled={busy||!evidenceReady} onChange={e=>{setReviewed(e.target.checked);setConfirmation(undefined);}}/>I inspected these complete patches, producer output and independent check results.</label>}
      </section>}
      {needsInspection&&<p role="alert" className="warning">Renewed delivery and artifact inspection required before publication authorization.</p>}
      {canPublish&&!delivery.authorization&&<button disabled={busy||needsInspection||!reviewed||!evidenceReady||!!reconciliation} onClick={()=>setConfirmation({id:delivery.id,digest:delivery.digest})}>Authorize inspected publication</button>}
      <h4>Shared provider evidence</h4>{delivery.receipt?<p>First immutable publication receipt <code>{delivery.receipt.digest}</code> · {dateLabel(delivery.receipt.createdAt)}</p>:<p className="warning">No publication receipt. A draft PR/MR, checks, merge and deployment are not established.</p>}
      {delivery.execution&&<p>Temporal observation: {delivery.execution.current&&now-Date.parse(delivery.execution.observedAt)>=-5000&&now-Date.parse(delivery.execution.observedAt)<=30000?'Observed':'Stale or uncertain'} · {delivery.execution.state} · {dateLabel(delivery.execution.observedAt)}</p>}
      {delivery.observation?<ProviderObservation observation={delivery.observation} delivery={delivery} now={now}/>:<p>Provider state unknown. No provider observation is retained.</p>}
      {canPublish&&delivery.authorization&&delivery.observation&&<button className="secondary" disabled={busy||needsInspection} onClick={()=>void reconcile()}>{reconciliation?.uncertain?'Retry exact provider refresh':'Request provider observation refresh'}</button>}
      {reconciliation?.uncertain&&<p className="warning">Provider refresh outcome unknown. Retry retains the exact delivery, digest and key.</p>}
    </article>}
    {confirmation&&<section role="dialog" aria-modal="false" aria-label="Confirm publication decision" className="warning"><h3>Confirm exact publication authorization</h3><p>Delivery <code>{confirmation.id}</code></p><p>Proposal digest <code>{confirmation.digest}</code></p><p>Authorize the displayed patch, check evidence and provider target. A separate trusted publisher will create a new branch and draft PR/MR.</p><button disabled={busy} onClick={()=>void authorize()}>Confirm publication authorization</button><button className="secondary" disabled={busy} onClick={()=>setConfirmation(undefined)}>Dismiss publication decision</button></section>}
  </section>;
}

function ProviderObservation({observation:o,delivery,now}:{observation:DeliveryObservation;delivery:Delivery;now:number}) {
  const url=providerURL(o.url,delivery.target.host),age=now-Date.parse(o.observedAt);
  return <section aria-label="Provider observation"><p>{age>=-5000&&age<=30000?'Retained provider observation':'Stale or uncertain provider observation'} · {dateLabel(o.observedAt)} · sequence {o.sequence}</p><p>{url?<a href={url} target="_blank" rel="noopener noreferrer">Inspect provider {delivery.target.provider==='github'?'PR':'MR'} #{o.number}</a>:'Provider link unavailable'} · {o.state} · {o.draft?'Draft':'Not draft'}</p><dl><dt>Published commit</dt><dd><code>{o.commit}</code></dd><dt>Merge commit</dt><dd><code>{o.mergeCommit??'Not observed'}</code></dd><dt>Provider checks</dt><dd>{o.checksState} · {o.checksTruncated?'Coverage truncated; cannot establish passing':'Retained bounded coverage'}</dd><dt>Deployment coverage</dt><dd>{o.deployment} · {o.deploymentsTruncated?'Truncated':'Bounded'}</dd><dt>Production outcome</dt><dd>{o.productionOutcome}</dd></dl>
    {o.checks.length?<ul>{o.checks.map((c,i)=><li key={i}>{visibleControls(c.name)} · {c.state} · <code>{c.commit}</code></li>)}</ul>:<p>No provider checks retained; verification unknown.</p>}
    {o.deployments.length?<ul className="record-list">{o.deployments.map((d,i)=><li key={i}><strong>{visibleControls(d.environment)}</strong> · {d.state}<dl><dt>Deployment / status</dt><dd>{d.id} / {d.statusId??'Not retained'}</dd><dt>Commit</dt><dd><code>{d.commit}</code> · {d.commitRelation}</dd><dt>Provider / observed</dt><dd>{d.providerState} · {dateLabel(d.providerUpdatedAt)} / {dateLabel(d.observedAt)}</dd><dt>Status coverage</dt><dd>{d.statusTruncated?'Truncated; incomplete evidence':'Bounded'}</dd></dl></li>)}</ul>:<p>No matching deployment retained; deployment and production outcome remain unverified.</p>}
    <p className="muted">Provider facts do not approve a design or prove a production outcome. Refresh is explicit.</p>
  </section>;
}
