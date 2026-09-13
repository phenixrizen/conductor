import { useWorkflowActivity } from './workflowActivity';
import { ExecutionArtifact } from './ExecutionArtifact';
import { inspectExecutionArtifact } from './delivery';
import type { ExecutionArtifact as Artifact, InspectedExecutionArtifact } from './delivery';
import { visibleControls } from './sourceText';
import { useEffect, useRef, useState } from 'react';
import { APIError, accessFailure, dateLabel, errorMessage, request } from './api';
import type { BrowserAccess } from './api';
import { sameJSON, uncertainMutation } from './collections';
import { isHex } from './graphs';
import { normalizePlan, runPath, validatePlan, validateProfiles, validateRun, validateRunPage, validateVerificationReview } from './coordination';
import type { CoordinationRun, ExecutionCapabilities, ExecutionProfilePage, RunPage, RunPlan, TaskReceipt, VerificationReview } from './coordination';
import { parseStrictJSON } from './strictJSON';

type Job = { sequence: number; signal: AbortSignal };
type Creation = { plan: RunPlan; key: string; outcome: 'uncertain' | 'recorded' | 'pending' };
type Confirmation = { id: string; digest: string; plan: RunPlan; cancel: boolean };

export function CoordinatedRuns({ access, visible = true, onAccessFailure }: { access: BrowserAccess; visible?: boolean; onAccessFailure?: (failure: APIError) => void }) {
  const [page, setPage] = useState<RunPage>();
  const [run, setRun] = useState<CoordinationRun>();
  const [artifact, setArtifact] = useState<(InspectedExecutionArtifact & { taskKey: string; artifactDigest: string })>();
  const [profiles, setProfiles] = useState<ExecutionProfilePage>();
  const [capabilities, setCapabilities] = useState<ExecutionCapabilities>();
  const [id, setID] = useState('');
  const [draft, setDraft] = useState('');
  const [preview, setPreview] = useState<RunPlan>();
  const [creation, setCreation] = useState<Creation>();
  const [confirmation, setConfirmation] = useState<Confirmation>();
  const [needsInspection, setNeedsInspection] = useState(false);
  const [pending, setPending] = useState('');
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const [now, setNow] = useState(Date.now());
  const sequence = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const working = useRef(false);
  const busy = !!pending;
  const locked = !!creation && creation.outcome !== 'recorded';
  const canExecute = access.principalKind === 'human' && access.canAuthor && capabilities?.canExecute;
  const workflowActive = useWorkflowActivity(visible, () => {
    sequence.current++; controller.current?.abort(); working.current = false; setPending('');
    setConfirmation(undefined);
    if (pending) setNeedsInspection(true);
    setCreation(value => value?.outcome === 'pending' ? { ...value, outcome: 'uncertain' } : value);
  });

  useEffect(() => { const timer=setInterval(()=>setNow(Date.now()),5000);return()=>{clearInterval(timer);sequence.current++;controller.current?.abort();}; },[]);
  function begin(label: string): Job | undefined { if(!workflowActive.current||working.current)return;working.current=true;controller.current?.abort();controller.current=new AbortController();setPending(label);setError('');setNotice('');return{sequence:++sequence.current,signal:controller.current.signal}; }
  function current(job: Job) { return workflowActive.current&&sequence.current===job.sequence&&!job.signal.aborted; }
  function finish(job: Job) { if(current(job)){working.current=false;setPending('');} }
  function fail(job: Job, failure: unknown) {
    if(!current(job))return;
    if(failure instanceof APIError&&[401,403,404].includes(failure.status)){
      // Retained prompts, source pins and confirmations are private shared data.
      // A denial invalidates this operation before callers can restore its capture.
      sequence.current++;controller.current?.abort();working.current=false;setPending('');setRun(undefined);setArtifact(undefined);setPage(undefined);setProfiles(undefined);setCapabilities(undefined);setID('');setDraft('');setPreview(undefined);setCreation(undefined);setConfirmation(undefined);setNeedsInspection(true);
      if(accessFailure(failure,access)){onAccessFailure?.(failure);return;}
    }
    setError(visibleControls(errorMessage(failure)));
  }
  async function refresh(more=false) {
    const job=begin('Loading shared runs and execution access…');if(!job)return;
    setConfirmation(undefined);setCapabilities(undefined);
    try {
      const query=new URLSearchParams({limit:'20'});if(more&&page?.nextBefore)query.set('before',page.nextBefore);
      const [next,caps,catalog]=await Promise.all([
        request<RunPage>(`/coordination-runs?${query}`,access,job.signal),request<ExecutionCapabilities>('/execution-capabilities',access,job.signal),request<ExecutionProfilePage>('/execution-profiles',access,job.signal),
      ]);
      if(!current(job))return;validateRunPage(next,access);validateProfiles(catalog);
      if(!caps||caps.repositoryId!==access.repositoryID||typeof caps.canExecute!=='boolean'||typeof caps.canPublish!=='boolean')throw new Error('Execution access could not be confirmed.');
      setPage(next);setCapabilities(caps);setProfiles(catalog);
    }catch(failure){fail(job,failure);}finally{finish(job);}
  }
  async function inspect(selectedID: string) {
    if(!isHex(selectedID,32)){setError('Use the complete 32-character run ID.');return;}
    const job=begin('Inspecting exact shared plan and receipts…');if(!job)return;setConfirmation(undefined);setRun(undefined);setArtifact(undefined);setNeedsInspection(true);
    try {const value=await request<CoordinationRun>(runPath(selectedID),access,job.signal);if(!current(job))return;validateRun(value,access,selectedID);setRun(value);setID(value.id);setNeedsInspection(false);setNotice('Plan inspected. Execution authorization binds to this exact digest.');}
    catch(failure){fail(job,failure);}finally{finish(job);}
  }
  async function inspectTask(receipt: TaskReceipt) {
    const selected = run;
    if (!selected || !receipt.artifactDigest) return;
    const job = begin('Loading the exact retained task artifact…'); if (!job) return;
    setArtifact(undefined); setConfirmation(undefined);
    const query = new URLSearchParams({ runDigest: selected.digest, taskId: receipt.taskId, artifactDigest: receipt.artifactDigest });
    try {
      // Read exactly the receipt currently displayed; this does not propose a
      // delivery or require a successful producer, cleanup or independent check.
      const value = await request<{ runId: string; runDigest: string; taskId: string; artifactDigest: string; artifact: Artifact; verification?: VerificationReview }>(`${runPath(selected.id)}/artifact?${query}`, access, job.signal);
      if (!current(job)) return;
      if (!value || value.runId !== selected.id || value.runDigest !== selected.digest || value.taskId !== receipt.taskId || value.artifactDigest !== receipt.artifactDigest) throw new Error('Task artifact does not match the inspected run and receipt.');
      const inspected = await inspectExecutionArtifact(value.artifact);
      validateVerificationReview(value.verification, selected.plan, receipt.taskKey, inspected.artifact);
      if (!current(job)) return;
      const task = selected.plan.tasks.find(t => t.id === receipt.taskKey);
      if (!task || inspected.artifact.profileDigest !== task.profileDigest || inspected.artifact.image !== task.image || inspected.decodedPatches.some(p => !selected.plan.repositories.some(r => r.repositoryId === p.metadata.repositoryId && r.commit === p.metadata.baseCommit)) || new Set(inspected.decodedPatches.map(p => p.metadata.repositoryId)).size !== inspected.decodedPatches.length) throw new Error('Task artifact does not match the inspected profile, image or source pins.');
      setArtifact({ ...inspected, verification: value.verification, taskKey: receipt.taskKey, artifactDigest: receipt.artifactDigest });
      setNotice('Complete retained task report and patch bytes inspected. Task outcome does not establish publication or production success.');
    } catch (failure) { fail(job, failure); } finally { finish(job); }
  }
  function parseDraft(text: string) {const value=parseStrictJSON(text);validatePlan(value);if(!value.repositories.some(r=>r.repositoryId===access.repositoryID))throw new Error('Include the selected repository in this plan.');return normalizePlan(value);}
  async function importFile(file?: File) {
    if(!file||busy||locked)return;
    const job=begin('Reading the selected plan file…');if(!job)return;setPreview(undefined);
    try {if(file.size>1048576)throw new Error('The selected JSON file exceeds 1 MiB.');const bytes=await file.arrayBuffer();if(!current(job))return;const text=new TextDecoder('utf-8',{fatal:true}).decode(bytes);const value=parseDraft(text);setDraft(text);setPreview(value);setCreation(undefined);setNotice('File imported for preview. No plan has been recorded or authorized.');}
    catch(failure){fail(job,failure);}finally{finish(job);}
  }
  function previewDraft() {try{setPreview(parseDraft(draft));setError('');setCreation(undefined);}catch(failure){setPreview(undefined);setError(visibleControls(errorMessage(failure)));}}
  async function propose() {
    if(!access.canAuthor||!preview||creation?.outcome==='recorded')return;
    const captured:Creation=creation?.outcome==='uncertain'?creation:{plan:preview,key:crypto.randomUUID(),outcome:'pending'};
    const job=begin('Recording the shared plan proposal…');if(!job)return;setCreation({...captured,outcome:'pending'});setConfirmation(undefined);setArtifact(undefined);
    try {const value=await request<CoordinationRun>('/coordination-runs',access,job.signal,captured.plan,{idempotencyKey:captured.key});if(!current(job))return;validateRun(value,access);if(value.proposerId!==access.principalID||!sameJSON(value.plan,captured.plan))throw new Error('The returned proposal does not match the preview. Its outcome is unknown.');setCreation({...captured,outcome:'recorded'});setRun(value);setID(value.id);setPage(undefined);setNeedsInspection(false);setNotice('Shared proposal recorded. Human execution authorization is a separate decision.');}
    catch(failure){fail(job,failure);if(current(job))setCreation(uncertainMutation(failure)?{...captured,outcome:'uncertain'}:undefined);}finally{finish(job);}
  }
  function prepare(cancel: boolean) {if(!run||busy||needsInspection||!canExecute)return;setConfirmation({id:run.id,digest:run.digest,plan:run.plan,cancel});}
  async function confirm() {
    const captured=confirmation;if(!captured||!run||needsInspection||!canExecute||captured.id!==run.id||captured.digest!==run.digest)return;
    const job=begin(captured.cancel?'Recording cancellation intent…':'Authorizing the inspected execution plan…');if(!job)return;
    try {
      // Send only the displayed immutable digest. Never refresh source, packages,
      // profiles, access or the plan inside this consequential command.
      const value=await request<CoordinationRun>(`${runPath(captured.id)}/${captured.cancel?'cancellation':'authorization'}`,access,job.signal,{digest:captured.digest});
      if(!current(job))return;validateRun(value,access,captured.id);
      if(value.digest!==captured.digest||!sameJSON(value.plan,captured.plan)||(!captured.cancel&&(!value.authorization||value.authorization.actor!==access.principalID))||(captured.cancel&&!value.cancelRequestedAt))throw new Error('The command outcome does not match the inspected decision.');
      setRun(value);setConfirmation(undefined);setNotice(captured.cancel?'Cancellation intent recorded. Reservations remain until execution and cleanup are confirmed.':'Execution authorization recorded. Await retained task receipts; this response does not prove a running worker or passing checks.');
    }catch(failure){fail(job,failure);if(current(job)){setConfirmation(undefined);setNeedsInspection(true);}}finally{finish(job);}
  }
  const observed=run?.execution;
  const observedAt=observed?Date.parse(observed.observedAt):NaN;
  const currentObservation=observed?.current&&Number.isFinite(observedAt)&&now-observedAt>=-5000&&now-observedAt<=30000;
  return <section className="coordinated-runs panel" aria-labelledby="runs-title" aria-busy={busy}>
    <div className="section-heading"><div><p className="eyebrow">SHARED AGENT WORK</p><h2 id="runs-title">Coordinated execution</h2></div><button className="secondary" disabled={busy} onClick={()=>void refresh()}>Refresh runs and access</button></div>
    <p>Review dependent tasks, exact source and package revisions, and retained results across repositories. People authorize execution separately from design approval.</p>
    <div role="status" aria-live="polite">{pending||notice}</div>{error&&<p role="alert" className="error">{error}</p>}
    {!page&&<p className="muted">Refresh to discover shared plans and available execution profiles.</p>}{page?.runs.length===0&&<p>No shared runs are available in this scope.</p>}
    {capabilities&&<p>{canExecute?'You may authorize execution, subject to current permissions and approvals on every included repository.':'Execution authorization is unavailable for this identity or repository. You can inspect shared plans.'}</p>}
    {page&&<><ul className="shared-list record-list" aria-label="Shared coordinated runs">{page.runs.map(item=><li key={item.id}><button className="shared-card" disabled={busy} onClick={()=>void inspect(item.id)} aria-label={`Inspect run ${item.id}`}><strong>{item.id}</strong><span>{item.authorized?'Human authorization recorded':'Proposal awaiting authorization'} · {item.receipts}/{item.tasks} retained task receipts</span><code>{item.digest}</code><span>{dateLabel(item.createdAt)}</span></button></li>)}</ul>{page.nextBefore&&<><p className="warning">More runs exist. This page is bounded to 20.</p><button className="secondary" disabled={busy} onClick={()=>void refresh(true)}>Load more runs</button></>}</>}
    <form className="control-row" onSubmit={e=>{e.preventDefault();void inspect(id);}}><label htmlFor="run-id">Run ID<input id="run-id" value={id} maxLength={32} disabled={busy} onChange={e=>setID(e.target.value)} /></label><button className="secondary" disabled={busy||!isHex(id,32)}>Inspect shared run</button></form>
    {profiles&&<details><summary>Available execution profiles ({profiles.profiles.length})</summary>{profiles.profiles.length===0&&<p>No enabled profiles are available. A workspace operator must configure them.</p>}{profiles.truncated&&<p className="warning">The catalog is truncated. Missing profiles cannot be selected from this response.</p>}<ul className="record-list">{profiles.profiles.map(p=><li key={p.id}><strong>{p.id}</strong> · {p.adapter}<dl><dt>Profile digest</dt><dd><code>{p.profileDigest}</code></dd><dt>Image</dt><dd><code>{p.image}</code></dd>{p.model&&<><dt>Configured model</dt><dd>{p.model}</dd></>}{p.maxBudgetUsd&&<><dt>Budget</dt><dd>{p.maxBudgetUsd} USD</dd></>}</dl>{p.command&&<pre>{JSON.stringify(p.command)}</pre>}</li>)}</ul></details>}
    {access.canAuthor&&<details className="run-proposal" open={creation?.outcome==='uncertain'||undefined}><summary>Import or author a plan proposal</summary><p>Agents can propose shared plans through MCP. To record one here, import its bounded JSON file or paste a version 1 plan, then inspect the structured preview. Recording a proposal does not execute it.</p>
      <label htmlFor="run-plan-file">Plan JSON file<input id="run-plan-file" type="file" accept=".json,application/json" disabled={busy||locked} onChange={e=>{void importFile(e.target.files?.[0]);e.target.value='';}} /></label>
      <label htmlFor="run-plan-json">Plan JSON<textarea id="run-plan-json" value={draft} rows={8} maxLength={1048576} disabled={busy||locked} onChange={e=>{setDraft(e.target.value);setPreview(undefined);setCreation(undefined);}} /></label><button className="secondary" disabled={busy||locked||!draft} onClick={previewDraft}>Preview plan</button>
      {preview&&<section aria-label="Plan proposal preview"><h3>Plan proposal preview</h3><PlanDetails plan={preview}/><button disabled={busy||creation?.outcome==='recorded'} onClick={()=>void propose()}>{creation?.outcome==='uncertain'?'Retry exact plan proposal':'Record plan proposal'}</button></section>}
      {creation?.outcome==='uncertain'&&<p className="warning">Proposal outcome unknown. The exact plan and idempotency key are retained for this explicit retry.</p>}
      {creation?.outcome==='recorded'&&<button className="secondary" disabled={busy} onClick={()=>{setDraft('');setPreview(undefined);setCreation(undefined);}}>Start another proposal</button>}
    </details>}
    {run&&<article className="run-inspection" aria-label="Inspected coordinated run"><div className="section-heading"><h3>Inspected coordinated run</h3><button className="secondary" disabled={busy} onClick={()=>void inspect(run.id)}>Refresh inspected run</button></div><dl><dt>Run</dt><dd><code>{run.id}</code></dd><dt>Plan digest</dt><dd><code>{run.digest}</code></dd><dt>Proposed by</dt><dd>{run.proposerId}</dd><dt>Created</dt><dd>{dateLabel(run.createdAt)}</dd><dt>Authorization</dt><dd>{run.authorization?`${run.authorization.actor} · ${dateLabel(run.authorization.createdAt)}`:'Not authorized'}</dd></dl>
      <PlanDetails plan={run.plan}/>
      <section aria-label="Coordinated execution observation"><h4>Observed execution</h4><p>{observed?`${currentObservation?'Observed execution':'Stale or uncertain observation'}: ${observed.state}`:'Progress unknown. No execution observation is available.'}</p>{observed&&<p className="muted">Observed at {dateLabel(observed.observedAt)}. Refresh explicitly for newer facts.</p>}{run.cancelRequestedAt&&<p className="warning">Cancellation requested at {dateLabel(run.cancelRequestedAt)}. This does not prove that work stopped or reservations were released.</p>}</section>
      <h4>Retained task receipts ({run.receipts.length}/{run.plan.tasks.length})</h4><ul className="record-list task-receipts">{run.plan.tasks.map(task=>{const receipt=run.receipts.find(r=>r.taskKey===task.id);return<li key={task.id}><strong>{task.id}</strong> · {receipt?receipt.outcome:'No receipt; verification unknown'}{receipt&&<dl><dt>Retained task ID</dt><dd><code>{receipt.taskId}</code></dd><dt>Receipt digest</dt><dd><code>{receipt.digest}</code></dd><dt>Artifact digest</dt><dd><code>{receipt.artifactDigest??'No implementation artifact'}</code></dd><dt>Retained</dt><dd>{dateLabel(receipt.createdAt)}</dd></dl>}{receipt?.artifactDigest&&<button className="secondary" disabled={busy} onClick={()=>void inspectTask(receipt)}>Inspect task artifact {task.id}</button>}</li>;})}</ul><p className="muted">Inspect retained reports, complete patches and executed checks here. Delivery review separately governs publication authorization. Task outcome alone does not prove a merge, deployment or production outcome.</p>
      {artifact&&<section aria-label="Inspected task artifact"><h4>Retained task artifact · {artifact.taskKey}</h4><p>Artifact digest <code>{artifact.artifactDigest}</code></p><ExecutionArtifact value={artifact}/><p className="muted">This read grants no approval, execution or publication authority.</p></section>}
      {needsInspection&&<p role="alert" className="warning">Renewed run inspection required before another execution command.</p>}
      {canExecute&&<div className="control-row"><button disabled={busy||needsInspection||!!run.authorization||!!run.cancelRequestedAt} onClick={()=>prepare(false)}>Authorize inspected plan</button><button className="secondary" disabled={busy||needsInspection||!run.authorization||!!run.cancelRequestedAt} onClick={()=>prepare(true)}>Request run cancellation</button></div>}
    </article>}
    {confirmation&&<section role="dialog" aria-modal="false" aria-label="Confirm execution decision" className="warning run-confirmation"><h3>{confirmation.cancel?'Confirm cancellation intent':'Confirm exact execution authorization'}</h3><p>Run <code>{confirmation.id}</code></p><p>Plan digest <code>{confirmation.digest}</code></p><p>{confirmation.cancel?'Record cancellation intent for this run. Cleanup and reservation release must still be confirmed.':'Authorize the displayed tasks, source/package revisions, profiles, writable paths and checks. Current independent design approval and permission are required on every repository.'}</p><button disabled={busy} onClick={()=>void confirm()}>{confirmation.cancel?'Confirm run cancellation':'Confirm execution authorization'}</button><button className="secondary" disabled={busy} onClick={()=>setConfirmation(undefined)}>Dismiss execution decision</button></section>}
  </section>;
}

export function PlanDetails({plan}:{plan:RunPlan}) {
  return <div className="plan-details"><dl><dt>Graph</dt><dd><code>{plan.graphId}</code></dd><dt>Graph digest</dt><dd><code>{plan.graphDigest}</code></dd><dt>Parallel tasks</dt><dd>At most {plan.maxParallel}</dd></dl><h4>Exact source and design revisions</h4><ul className="record-list">{plan.repositories.map(repo=>{const pin=plan.packages.find(p=>p.repositoryId===repo.repositoryId);return<li key={repo.repositoryId}><strong>{repo.repositoryId}</strong><dl><dt>Commit</dt><dd><code>{repo.commit}</code></dd><dt>Collection</dt><dd><code>{repo.collectionId}</code></dd><dt>Receipt digest</dt><dd><code>{repo.receiptDigest}</code></dd><dt>Whole source</dt><dd><code>{repo.fullSourceDigest??'Unpinned; execution unavailable'}</code></dd><dt>Package</dt><dd>{pin?.changeId} · revision {pin?.revision}</dd><dt>Design digest</dt><dd><code>{pin?.digest}</code></dd></dl></li>;})}</ul><h4>Tasks and dependencies</h4><ol className="run-tasks">{plan.tasks.map(task=><li key={task.id}><h4>{task.id} · {task.perspective}</h4><p>After: {task.dependsOn?.length?task.dependsOn.join(', '):'No predecessor tasks'}. Deadline: {task.timeoutSeconds} seconds.</p><dl><dt>Profile</dt><dd>{task.profile}</dd><dt>Profile digest</dt><dd><code>{task.profileDigest??'Unpinned; execution unavailable'}</code></dd><dt>Image</dt><dd><code>{task.image??'Unpinned; execution unavailable'}</code></dd></dl><pre className="task-prompt">{visibleControls(task.prompt)}</pre><ul>{task.scopes.map(scope=><li key={scope.repositoryId}><strong>{scope.repositoryId}</strong> · {scope.writablePaths?.length?`Writable paths: ${scope.writablePaths.join(', ')}`:'Read only'}</li>)}</ul>{task.checks?.length?<ul>{task.checks.map(check=><li key={check.id}><strong>{check.id}</strong> · {check.repositoryId} · {check.timeoutSeconds} seconds<pre>{JSON.stringify(check.argv)}</pre>{check.requirements?.length?<ul aria-label={`Criterion links for ${check.id}`}>{check.requirements.map(r=><li key={`${r.changeId}/${r.criterionId}`}>{r.changeId} · revision {r.revision} · criterion {r.criterionId}<br/><code>{r.digest}</code></li>)}</ul>:<p>Unlinked check: no acceptance criterion support is declared.</p>}</li>)}</ul>:<p className="warning">No checks are declared. This task cannot establish passing implementation verification.</p>}</li>)}</ol></div>;
}
