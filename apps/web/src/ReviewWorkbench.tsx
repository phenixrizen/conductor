import { DesignAssistance } from './DesignAssistance';
import { visibleControls } from './sourceText';
import { PackageContent, PackageFields } from './PackageContent';
import { sameJSON } from './collections';
import { createChange, reviseChange, submitChange } from './api';
import type { Content } from './api';
import { WorkflowNavigation, workflows } from './WorkflowNavigation';
import type { Workflow } from './WorkflowNavigation';
import { useWorkflowActivity } from './workflowActivity';
import { readTrackerLinkHint } from './trackerLinkHint';
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { APIError, accessFailure, changePath, dateLabel, errorMessage, request } from './api';
import type { BrowserAccess, EventPage, HistoryPage, RequestAccess, Revision, RevisionView, WorkPackage } from './api';
import { Comparison } from './Comparison';
import { RepositoryContext } from './RepositoryContext';
import { Events, History } from './Records';
import { SharedChanges } from './SharedChanges';
import type { RelatedRequest } from './SharedChanges';
import { ContextCollections } from './ContextCollections';
import { RepositoryGraphs } from './RepositoryGraphs';
import { CoordinatedRuns } from './CoordinatedRuns';
import { RepositoryDeliveries } from './RepositoryDeliveries';
import { WorkTracking } from './WorkTracking';
import { RuntimeEvidence } from './RuntimeEvidence';
import type { Collection } from './collections';

const perspectives = {
  Architect: 'Inspect boundaries, contracts, constraints, and the decisions behind this design.',
  'QC / QA': 'Inspect acceptance criteria, verification plans, and missing or unexecuted evidence.',
  Developer: 'Inspect scope, task dependencies, implementation constraints, and verification commands.',
  Operations: 'Inspect deployment scope, recovery, observability, and the freshness of runtime evidence.',
  Product: 'Inspect the intended outcome, business rules, and measurable acceptance criteria.',
};

interface AuthorDraft { kind: 'create' | 'revise' | 'submit'; content: Content; original: Content; id?: string; revision?: number; digest?: string; stage: 'editing' | 'preview' | 'sending' | 'uncertain'; inspected?: boolean; reuse?: 'replace' | 'create' }

interface Job { token: number; signal: AbortSignal; access: RequestAccess }
interface AttachmentRecovery { packageID: string; collectionID: string; packageInspected: boolean; collectionInspected: boolean }

export function ReviewWorkbench({ access: browserAccess, sessionControls, onAccessFailure }: {
  access?: BrowserAccess;
  sessionControls?: ReactNode;
  onAccessFailure?: (failure: APIError) => void;
}) {
  const [workflow, setWorkflow] = useState<Workflow>(() => readTrackerLinkHint() ? 'tracker' : 'review');
  const lastWorkflow = useRef(workflow);
  useLayoutEffect(() => {
    if (lastWorkflow.current === workflow) return;
    lastWorkflow.current = workflow;
    // A tab chosen deep in a long report opens the next workflow at its start.
    // This changes scroll position only; focus stays on the selected tab.
    document.getElementById(`workflow-${workflow}`)?.scrollIntoView({ block: 'start' });
  }, [workflow]);
  const [assistanceLocked, setAssistanceLocked] = useState(false);
  const [authorDraft, setAuthorDraft] = useState<AuthorDraft>();
  const authorWrite = useRef<AuthorDraft | undefined>(undefined);
  const [id, setID] = useState('');
  const [actor, setActor] = useState('reviewer');
  const [perspective, setPerspective] = useState<keyof typeof perspectives>('Architect');
  const [pkg, setPackage] = useState<WorkPackage>();
  const [view, setView] = useState<RevisionView>();
  const [historical, setHistorical] = useState(false);
  const [history, setHistory] = useState<HistoryPage>();
  const [events, setEvents] = useState<EventPage>();
  const [historyError, setHistoryError] = useState('');
  const [eventsError, setEventsError] = useState('');
  const [error, setError] = useState('');
  const [inspectionRequired, setInspectionRequired] = useState('');
  const [notice, setNotice] = useState('');
  const [pending, setPending] = useState('');
  const [comparisonTarget, setComparisonTarget] = useState('previous');
  const [comparison, setComparison] = useState<Revision>();
  const [related, setRelated] = useState<RelatedRequest>();
  const [collectionBusy, setCollectionBusy] = useState(false);
  const [inspectedCollection, setInspectedCollection] = useState<Collection>();
  const [requestedCollection, setRequestedCollection] = useState<{ id: string; sequence: number }>();
  const [attachmentRecovery, setAttachmentRecovery] = useState<AttachmentRecovery>();
  const sequence = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const working = useRef(false);
  const collectionWorking = useRef(false);
  const busy = pending !== '' || collectionBusy;
  const access: RequestAccess = browserAccess ?? { mode: 'local', actor: actor.trim() };
  const reviewer = access.mode === 'browser' ? access.principalID : access.actor;
  const authorPermission = access.mode === 'local' || access.canAuthor;
  const approvalPermission = access.mode === 'local' || (access.principalKind === 'human' && access.canApprove);

  const reviewActive = useWorkflowActivity(workflow === 'review', () => {
    if (authorWrite.current) {
      setAuthorDraft({ ...authorWrite.current, stage: 'uncertain', inspected: false });
      authorWrite.current = undefined;
    } else setAuthorDraft(previous => previous?.stage === 'preview' ? (previous.kind === 'submit' ? undefined : { ...previous, stage: 'editing' }) : previous);
    sequence.current++; controller.current?.abort(); working.current = false; setPending('');
    if (pending) setInspectionRequired('Review was interrupted. Inspect the latest revision before another decision. An already sent command may have completed.');
  });
  useEffect(() => () => { sequence.current++; controller.current?.abort(); }, []);

  function clearInspection() {
    sequence.current++;
    controller.current?.abort();
    working.current = false;
    setPending('');
    setPackage(undefined);
    setView(undefined);
    setHistory(undefined);
    setEvents(undefined);
    setHistorical(false);
    setComparison(undefined);
    setRelated(undefined);
    setHistoryError('');
    setEventsError('');
    setError('');
    setNotice('');
    setInspectionRequired('');
  }

  function begin(label: string): Job | undefined {
    // The ref also prevents two actions before React renders the disabled state.
    if (!reviewActive.current || working.current || collectionWorking.current) return;
    working.current = true;
    controller.current?.abort();
    controller.current = new AbortController();
    const token = ++sequence.current;
    setPending(label);
    setError('');
    return { token, signal: controller.current.signal, access };
  }

  // Cancellation alone cannot stop an already-resolved response from replacing a newer inspection.
  function current(job: Job) { return reviewActive.current && sequence.current === job.token && !job.signal.aborted; }

  function finish(job: Job) {
    if (current(job)) { working.current = false; setPending(''); }
  }

  function failed(job: Job, failure: unknown) {
    if (!current(job)) return;
    if (accessFailure(failure, job.access)) {
      clearInspection();
      setAuthorDraft(undefined); authorWrite.current = undefined;
      onAccessFailure?.(failure);
      return;
    }
    setError(errorMessage(failure));
  }

  function latestView(value: WorkPackage): RevisionView {
    return { revision: value.revision, approvals: value.approval ? [value.approval] : [], approvalsTruncated: false };
  }

  function checkPackage(value: WorkPackage, job: Job, expectedID: string) {
    if (value.id !== expectedID || value.revision.changeId !== expectedID
      || (job.access.mode === 'browser' && (value.workspaceId !== job.access.workspaceID || value.repositoryId !== job.access.repositoryID))) {
      throw new Error('The package response does not match the selected scope. Reload access and inspect again.');
    }
  }

  async function readRecords(job: Job, packageID: string, inspectedRevision: number) {
    const path = changePath(packageID);
    const results = await Promise.allSettled([
      request<HistoryPage>(`${path}/history?beforeRevision=0&limit=20`, job.access, job.signal),
      request<EventPage>(`${path}/events?afterSequence=0&limit=20`, job.access, job.signal),
    ]);
    if (!current(job)) return;
    for (const result of results) {
      if (result.status === 'rejected' && accessFailure(result.reason, job.access)) {
        failed(job, result.reason);
        return false;
      }
    }
    const [historyResult, eventsResult] = results;
    if (historyResult.status === 'fulfilled') {
      setHistory(historyResult.value);
      setHistoryError('');
      if (historyResult.value.revisions.some(revision => revision.number > inspectedRevision)) {
        setInspectionRequired('History contains a newer revision. Inspect the latest revision before approving.');
      }
    }
    else { setHistory(undefined); setHistoryError(errorMessage(historyResult.reason)); }
    if (eventsResult.status === 'fulfilled') { setEvents(eventsResult.value); setEventsError(''); }
    else { setEvents(undefined); setEventsError(errorMessage(eventsResult.reason)); }
    return historyResult.status === 'fulfilled' && eventsResult.status === 'fulfilled';
  }

  async function inspectLatest(packageID = id.trim()) {
    if (!packageID || !reviewer.trim()) return;
    const job = begin('Inspecting latest revision…');
    if (!job) return;
    setAuthorDraft(previous => previous?.stage === 'uncertain' ? previous : undefined);
    setID(packageID);
    setPackage(undefined);
    setView(undefined);
    setComparison(undefined);
    setHistory(undefined);
    setEvents(undefined);
    setHistoryError('');
    setEventsError('');
    setInspectionRequired('');
    setNotice('');
    try {
      const value = await request<WorkPackage>(changePath(packageID), job.access, job.signal);
      if (!current(job)) return;
      checkPackage(value, job, packageID);
      setAttachmentRecovery(previous => {
        if (!previous || previous.packageID !== value.id) return previous;
        return previous.collectionInspected ? undefined : { ...previous, packageInspected: true };
      });
      setAuthorDraft(previous => previous?.stage === 'uncertain' && (!previous.id || previous.id === value.id) ? { ...previous, inspected: true } : previous);
      setPackage(value);
      setView(latestView(value));
      setHistorical(false);
      setComparisonTarget('previous');
      await readRecords(job, value.id, value.revision.number);
    } catch (failure) { failed(job, failure); }
    finally { finish(job); }
  }

  async function inspectHistory(revision: number) {
    if (!pkg) return;
    const job = begin(`Loading revision ${revision}…`);
    if (!job) return;
    setAuthorDraft(previous => previous?.stage === 'uncertain' ? previous : undefined);
    try {
      const value = await request<RevisionView>(`${changePath(pkg.id)}/revisions/${revision}`, job.access, job.signal);
      if (!current(job)) return;
      setView(value);
      // Even the newest numbered history record is read-only until the latest endpoint is inspected.
      setHistorical(true);
      setComparison(undefined);
      setComparisonTarget(revision === 1 && pkg.revision.number !== 1 ? 'latest' : 'previous');
      setNotice('');
    } catch (failure) { failed(job, failure); }
    finally { finish(job); }
  }

  async function compare() {
    if (!pkg || !view) return;
    const target = comparisonTarget === 'previous' ? view.revision.number - 1 : pkg.revision.number;
    if (target < 1 || target === view.revision.number) return;
    const job = begin(`Comparing with revision ${target}…`);
    if (!job) return;
    setComparison(undefined);
    try {
      const value = await request<RevisionView>(`${changePath(pkg.id)}/revisions/${target}`, job.access, job.signal);
      if (current(job)) setComparison(value.revision);
    } catch (failure) { failed(job, failure); }
    finally { finish(job); }
  }

  async function loadMore(kind: 'history' | 'events') {
    if (!pkg) return;
    const job = begin(`Loading more ${kind}…`);
    if (!job) return;
    try {
      if (kind === 'history') {
        const value = await request<HistoryPage>(`${changePath(pkg.id)}/history?beforeRevision=${history?.nextBeforeRevision ?? 0}&limit=20`, job.access, job.signal);
        if (current(job)) {
          setHistory(previous => ({ ...value, revisions: [...(previous?.revisions ?? []), ...value.revisions] }));
          setHistoryError('');
        }
      } else {
        const value = await request<EventPage>(`${changePath(pkg.id)}/events?afterSequence=${events?.nextAfterSequence ?? 0}&limit=20`, job.access, job.signal);
        if (current(job)) {
          setEvents(previous => ({ ...value, events: [...(previous?.events ?? []), ...value.events] }));
          setEventsError('');
        }
      }
    } catch (failure) {
      if (accessFailure(failure, job.access)) { failed(job, failure); return; }
      if (current(job)) (kind === 'history' ? setHistoryError : setEventsError)(errorMessage(failure));
    } finally { finish(job); }
  }

  async function refreshRecords() {
    if (!pkg) return;
    const job = begin('Refreshing history and audit events…');
    if (!job) return;
    try {
      const complete = await readRecords(job, pkg.id, pkg.revision.number);
      if (current(job)) setNotice(complete
        ? 'Records refreshed. The inspected content has not changed.'
        : 'Some records are unavailable. The inspected content has not changed.');
    } finally { finish(job); }
  }

  const canApprove = !!pkg && !!view && !historical && !inspectionRequired && !attachmentRecovery && !authorDraft && !assistanceLocked && !busy
    && !!view.revision.submittedAt && !pkg.approved && reviewer !== view.revision.author && approvalPermission;

  async function approve() {
    if (!pkg || !view || !canApprove) return;
    const inspected = view.revision;
    const job = begin('Recording approval for the inspected revision…');
    if (!job) return;
    try {
      // No read happens in this action: send only the revision/digest on screen.
      const value = await request<WorkPackage>(`${changePath(pkg.id)}/approvals`, job.access, job.signal,
        { revision: inspected.number, digest: inspected.digest });
      if (!current(job)) return;
      checkPackage(value, job, pkg.id);
      if (value.revision.number !== inspected.number || value.revision.digest !== inspected.digest) {
        setInspectionRequired('The approval response does not match the inspected content. Inspect the latest revision again.');
        return;
      }
      setPackage(value);
      setView(latestView(value));
      setNotice('Approval recorded for the displayed revision. Refresh records to see the new audit event and approval count.');
    } catch (failure) {
      failed(job, failure);
      if (current(job)) {
        if (failure instanceof APIError && failure.status === 409) {
          setInspectionRequired('This inspection is stale. Inspect the latest revision and review its content before approving again.');
        } else if (!(failure instanceof APIError) || failure.status >= 500 || failure.status < 400) {
          setInspectionRequired('The approval outcome could not be confirmed. Inspect the latest revision before taking another action.');
        }
      }
    } finally { finish(job); }
  }

  function startAuthoring(kind: 'create' | 'revise' | 'submit') {
    if (!reviewActive.current || busy || authorDraft || assistanceLocked || !authorPermission || !reviewer.trim()) return;
    if (kind === 'create') {
      clearInspection(); setID('');
      setAuthorDraft({ kind, content: {}, original: {}, stage: 'editing' });
    } else if (pkg && view && !historical && !inspectionRequired && !attachmentRecovery) {
      setAuthorDraft({ kind, id: pkg.id, revision: view.revision.number, digest: view.revision.digest,
        content: view.revision.content, original: view.revision.content, stage: kind === 'submit' ? 'preview' : 'editing' });
    }
  }

  function previewAuthoring() {
    if (!authorDraft || busy || authorDraft.stage !== 'editing') return;
    if (authorDraft.kind === 'create' && (!String(authorDraft.content.title ?? '').trim() || !String(authorDraft.content.intent ?? '').trim())) {
      setError('Give the change a title and describe its intended outcome.'); return;
    }
    if (new TextEncoder().encode(JSON.stringify({ content: authorDraft.content, expectedRevision: authorDraft.revision })).length > 1024 * 1024) {
      setError('This change exceeds the 1 MiB request limit. Shorten the editable text.'); return;
    }
    if (authorDraft.kind === 'revise' && view && sameJSON(authorDraft.content, view.revision.content)) {
      setError('No content changed. Edit a field before saving a new revision.'); return;
    }
    setError(''); setAuthorDraft({ ...authorDraft, stage: 'preview' });
  }

  function reuseRetainedDraft() {
    if (!authorDraft || authorDraft.stage !== 'uncertain' || !authorDraft.inspected || busy || !authorPermission) return;
    if (authorDraft.kind === 'create') {
      const content = authorDraft.content;
      clearInspection(); setID('');
      setAuthorDraft({ kind: 'create', content, original: authorDraft.original, stage: 'editing', reuse: 'create' });
    } else if (authorDraft.kind === 'revise' && pkg && view && pkg.id === authorDraft.id && !historical && !inspectionRequired && !attachmentRecovery) {
      // This explicit choice creates a replacement draft against the newly inspected
      // version. It neither merges content nor replays the earlier command.
      setAuthorDraft({ kind: 'revise', content: authorDraft.content, original: authorDraft.original, id: pkg.id, revision: view.revision.number,
        digest: view.revision.digest, stage: 'editing', reuse: 'replace' });
      setError('');
    }
  }

  async function confirmAuthoring() {
    if (!authorDraft || authorDraft.stage !== 'preview' || !authorPermission || inspectionRequired || attachmentRecovery) return;
    const captured = authorDraft;
    const job = begin(captured.kind === 'submit' ? 'Submitting the inspected design for review…' : 'Saving the displayed change…');
    if (!job) return;
    authorWrite.current = captured;
    setAuthorDraft({ ...captured, stage: 'sending' });
    try {
      const value = captured.kind === 'create' ? await createChange(captured.content, job.access, job.signal)
        : captured.kind === 'revise' ? await reviseChange(captured.id!, captured.revision!, captured.content, job.access, job.signal)
          : await submitChange(captured.id!, captured.revision!, job.access, job.signal);
      if (!current(job)) return;
      checkPackage(value, job, captured.id ?? value.id);
      const expected = captured.kind === 'create' ? 1 : captured.revision! + (captured.kind === 'revise' ? 1 : 0);
      if (value.revision.number !== expected || !sameJSON(value.revision.content, captured.content)
        || !/^[a-f0-9]{64}$/.test(value.revision.digest)
        || (captured.kind === 'submit' ? value.revision.digest !== captured.digest || !value.revision.submittedAt
          : value.revision.author !== reviewer || value.approved || !!value.revision.submittedAt)) {
        throw new Error('The response does not confirm the exact command. Inspect shared work before continuing.');
      }
      setID(value.id); setPackage(value); setView(latestView(value)); setHistorical(false);
      setHistory(undefined); setEvents(undefined); setComparison(undefined); setAuthorDraft(undefined);
      setHistoryError('Refresh history to see this command.'); setEventsError('Refresh events to see this command.');
      setNotice(captured.kind === 'submit' ? 'Design review requested. An independent reviewer can now inspect and approve this revision.'
        : 'Draft saved. Review its content, then request design review when it is ready.');
    } catch (failure) {
      failed(job, failure);
      if (current(job) && !accessFailure(failure, job.access)) {
        if (!(failure instanceof APIError) || failure.status === 409 || failure.status >= 500 || failure.status < 400) {
          setAuthorDraft({ ...captured, stage: 'uncertain', inspected: false });
          setInspectionRequired('The command was stale or its outcome could not be confirmed. Inspect the latest saved change before another command.');
        } else setAuthorDraft({ ...captured, stage: captured.kind === 'submit' ? 'preview' : 'editing' });
      }
    } finally { if (current(job)) authorWrite.current = undefined; finish(job); }
  }

  const targetNumber = view && (comparisonTarget === 'previous' ? view.revision.number - 1 : pkg?.revision.number);
  const context = view?.revision.content.repositoryContext;
  const repository = context !== null && typeof context === 'object' && 'repository' in context
    && typeof context.repository === 'string' ? context.repository : undefined;

  function collectionWork(active: boolean): boolean {
    if (active && working.current) return false;
    collectionWorking.current = active;
    setCollectionBusy(active);
    return true;
  }

  function attached(value: WorkPackage) {
    setPackage(value); setView(latestView(value)); setHistorical(false);
    setHistory(undefined); setEvents(undefined); setComparison(undefined);
    setHistoryError('Records have not been refreshed for this new revision.');
    setEventsError('Events have not been refreshed after attachment.');
    setNotice('Context attached as a new draft. Inspect its content, then refresh history and events explicitly.');
  }

  function collectionInspected(value?: Collection, freshInspection = false) {
    setInspectedCollection(value);
    // Only a successful explicit collection GET satisfies recovery. Clearing a
    // selection, replaying creation, or receiving a mutation response does not.
    if (freshInspection && value) setAttachmentRecovery(previous => {
      if (!previous || previous.collectionID !== value.id) return previous;
      return previous.packageInspected ? undefined : { ...previous, collectionInspected: true };
    });
  }

  function requestCollectionInspection(collectionID: string) {
    setWorkflow('source');
    setRequestedCollection(previous => ({ id: collectionID, sequence: (previous?.sequence ?? 0) + 1 }));
  }

  const selectedWorkflow = workflows.find(value => value.id === workflow)!;
  return <main className="workbench-shell">
    <WorkbenchHeader compact />
    {sessionControls}
    <section className="perspective" aria-label="Review perspective guidance">
      <label htmlFor="review-perspective">Review perspective<select id="review-perspective" aria-label="Review perspective" value={perspective}
        onChange={event => setPerspective(event.target.value as keyof typeof perspectives)}>
        {Object.keys(perspectives).map(value => <option key={value}>{value}</option>)}
      </select></label>
      <div><p>{perspectives[perspective]}</p><p className="muted">Changes review prompts only. This selection grants no permissions or approval rights.</p></div>
    </section>
    <WorkflowNavigation selected={workflow} onSelect={setWorkflow} />
    <div className="workflow-introduction"><p className="eyebrow">{selectedWorkflow.label}</p><p>{selectedWorkflow.description}</p></div>
    <section role="tabpanel" id="workflow-review" aria-labelledby="workflow-tab-review" tabIndex={0} hidden={workflow !== 'review'}>
    <section className="change-start panel" aria-label="Start a change">
      <div className="section-heading"><div><h2>What do you want to change?</h2><p>Describe the outcome, save a design, then ask an independent person to review it.</p></div>
        {authorPermission && <button disabled={busy || !!authorDraft || assistanceLocked || !reviewer.trim()} onClick={() => startAuthoring('create')}>New change</button>}</div>
      {!authorPermission && <p className="muted">You have read access. Select shared work below to inspect a design; author permission is needed to create or revise one.</p>}
    </section>
    {authorDraft && <section className="package-editor panel" aria-label="Change authoring">
      <h2>{authorDraft.kind === 'create' ? 'New change' : authorDraft.kind === 'revise' ? 'Revise design' : 'Request design review'}</h2>
      {authorDraft.id && <p>Change <code>{authorDraft.id}</code> · inspected revision {authorDraft.revision}<br/><code>{authorDraft.digest}</code></p>}
      {authorDraft.reuse && <p className="warning">{authorDraft.reuse === 'replace'
        ? 'This is a complete replacement using retained content. It may remove changes made by another author. Compare it with the latest saved design below, then preview before saving.'
        : 'This is a separate new change using retained input. The earlier creation may still have committed; saving again can create a duplicate.'}</p>}
      {authorDraft.stage === 'editing' ? <>
        <p>Title and intended outcome start a new change. The remaining sections are optional. Saving a revision preserves all other saved fields.</p>
        <PackageFields content={authorDraft.content} original={authorDraft.original} disabled={busy} onChange={content => { setAuthorDraft({ ...authorDraft, content }); setError(''); }} />
        <button disabled={busy} onClick={previewAuthoring}>Preview design</button>
      </> : <>
        <h3>{authorDraft.stage === 'uncertain' ? 'Retained command input' : 'Review before confirming'}</h3>
        <PackageContent content={authorDraft.content} />
        {authorDraft.stage === 'uncertain' ? <div className="warning"><p>The exact input is retained. No retry has been sent. {authorDraft.id ? 'Inspect this change’s latest revision to check what was saved.' : 'Browse shared work without a filter and inspect any matching change. An empty or bounded list cannot prove creation failed; the previous command may still have committed. Dismissing this input allows a separate new creation, which could produce a duplicate.'}</p>
          {authorDraft.id && <button className="secondary" disabled={busy} onClick={() => void inspectLatest(authorDraft.id)}>Inspect saved change</button>}
          {authorDraft.kind === 'revise' && authorDraft.inspected && pkg?.id === authorDraft.id && view && !historical && <details><summary>Latest inspected saved design</summary><PackageContent content={view.revision.content} /></details>}
          {authorDraft.kind !== 'submit' && <button className="secondary" disabled={busy || !authorDraft.inspected || (authorDraft.kind === 'revise' && (!pkg || pkg.id !== authorDraft.id || !view || historical || !!inspectionRequired || !!attachmentRecovery))} onClick={reuseRetainedDraft}>{authorDraft.kind === 'create' ? 'Edit retained input as a separate new change' : 'Use retained content as replacement draft'}</button>}
          <button className="secondary" disabled={busy || !authorDraft.inspected} onClick={() => setAuthorDraft(undefined)}>{authorDraft.kind === 'create' ? 'Dismiss creation input after checking shared work' : 'Dismiss retained input after inspection'}</button></div>
          : <><p>{authorDraft.kind === 'submit' ? 'Request an independent design review for exactly this saved revision.' : 'Save exactly this content as a new draft. Earlier approval will not apply to the new revision.'}</p>
            <button disabled={busy} onClick={() => void confirmAuthoring()}>{authorDraft.kind === 'create' ? 'Create change' : authorDraft.kind === 'revise' ? 'Save new revision' : 'Confirm design review request'}</button>
            {authorDraft.kind !== 'submit' && <button className="secondary" disabled={busy} onClick={() => setAuthorDraft({ ...authorDraft, stage: 'editing' })}>Back to editing</button>}</>}
      </>}
      {authorDraft.stage !== 'uncertain' && <button className="secondary" disabled={busy} onClick={() => setAuthorDraft(undefined)}>Discard unsent draft</button>}
    </section>}
    <form className="lookup panel" onSubmit={event => { event.preventDefault(); void inspectLatest(); }}>
      <label>Change ID<input value={id} maxLength={256} placeholder="CHG-…" disabled={busy} required
        onChange={event => { clearInspection(); setID(event.target.value); }} /></label>
      {!browserAccess && <label>Local reviewer<input value={actor} maxLength={128} disabled={busy} required
        onChange={event => { clearInspection(); setAuthorDraft(undefined); authorWrite.current = undefined; setActor(event.target.value); }} /></label>}
      <button type="submit" disabled={busy || !id.trim() || !reviewer.trim()}>Inspect latest revision</button>
      <p className="local-note">{browserAccess ? 'Inspection is limited to your selected workspace and managed repository.' : 'Local development identity only. These packages are separate from authenticated workspaces.'}</p>
    </form>
    <SharedChanges key={reviewer} access={access} visible={workflow === 'review'} disabled={busy} related={related} onInspect={packageID => void inspectLatest(packageID)} onBrowsed={() => {
      // SharedChanges captures this callback when its read starts. A discovery
      // started before this exact uncertain draft cannot satisfy its recovery.
      if (authorDraft?.stage === 'uncertain' && authorDraft.kind === 'create') {
        setAuthorDraft(previous => previous === authorDraft ? { ...previous, inspected: true } : previous);
      }
    }} onAccessFailure={onAccessFailure} />
    {browserAccess ? <DesignAssistance access={browserAccess} visible={workflow === 'review'} disabled={pending !== '' || !!authorDraft}
      changeId={pkg?.id} target={pkg && view && !historical && !inspectionRequired && !attachmentRecovery && !authorDraft ? view.revision : undefined}
      onBusyChange={collectionWork} onCommandLock={setAssistanceLocked} onInspectionRequired={setInspectionRequired} onAccessFailure={onAccessFailure}/>
      : <section className="panel"><h2>Native assistant help</h2><p>Assisted Design suggestions require authenticated workspace and repository access. They are unavailable in local mode.</p></section>}
    <div className="request-status" role="status" aria-live="polite">{pending || notice}</div>
    {error && <p role="alert" className="error banner">{error}</p>}
    {inspectionRequired && <div role="alert" className="warning banner"><strong>Renewed inspection required.</strong> {inspectionRequired}</div>}
    {attachmentRecovery && <div className="warning banner" role="alert"><strong>Attachment recovery requires both inspections.</strong>
      <p>Package <code>{attachmentRecovery.packageID}</code>: {attachmentRecovery.packageInspected ? 'inspected again' : 'latest inspection required'}. Collection <code>{attachmentRecovery.collectionID}</code>: {attachmentRecovery.collectionInspected ? 'inspected again' : 'inspection required'}. Approval and attachment stay blocked until both are inspected.</p>
      <div className="control-row"><button className="secondary" disabled={busy} onClick={() => void inspectLatest(attachmentRecovery.packageID)}>Inspect recovery package</button>
        <button className="secondary" disabled={busy} onClick={() => requestCollectionInspection(attachmentRecovery.collectionID)}>Inspect recovery collection</button></div>
    </div>}
    {!pkg && !authorDraft && !busy && !error && <section className="empty"><h2>Start with an outcome.</h2><p>Create a change above, or browse shared work to review an existing design.</p></section>}
    {pkg && view && <div className="workbench-grid" aria-busy={busy}>
      <article className="review panel" aria-labelledby="revision-title">
        <div className="status"><span>{pkg.id}</span><strong>{inspectionRequired ? 'INSPECTION STALE' : historical ? 'HISTORICAL VIEW' : pkg.approved ? 'DESIGN APPROVED' : view.revision.submittedAt ? 'REVIEW REQUIRED' : 'DRAFT'}</strong></div>
        {typeof view.revision.content.title === 'string' && view.revision.content.title.trim() && <h2 className="change-title">{visibleControls(view.revision.content.title)}</h2>}
        <div className="section-heading"><h3 id="revision-title">Revision {view.revision.number}</h3><span className="tag">{historical ? 'Preserved record' : 'Latest inspected'}</span></div>
        <dl>
          <dt>Digest</dt><dd><code>{view.revision.digest}</code></dd>
          <dt>Author</dt><dd>{view.revision.author}</dd>
          {browserAccess && <><dt>Workspace</dt><dd>{pkg.workspaceId}</dd><dt>Repository</dt><dd>{pkg.repositoryId}</dd></>}
          <dt>Created</dt><dd>{dateLabel(view.revision.createdAt)}</dd>
          <dt>Submitted</dt><dd>{view.revision.submittedAt ? dateLabel(view.revision.submittedAt) : 'Not submitted'}</dd>
        </dl>
        {!historical && authorPermission && <section className="design-actions" aria-label="Design author actions">
          <h3>Continue this change</h3>
          <p>{view.revision.submittedAt ? 'This revision has been submitted. Editing creates a new draft for a fresh review.' : 'Review the saved draft, then request an independent design review.'}</p>
          <button className="secondary" disabled={busy || !!authorDraft || assistanceLocked || !!inspectionRequired || !!attachmentRecovery} onClick={() => startAuthoring('revise')}>Revise design</button>
          {!view.revision.submittedAt && <button disabled={busy || !!authorDraft || assistanceLocked || !!inspectionRequired || !!attachmentRecovery} onClick={() => startAuthoring('submit')}>Request design review</button>}
        </section>}
        {historical && <div className="warning"><p>Historical inspection is read-only. Its approvals are retained records and are not authority for a later revision.</p><button className="secondary" disabled={busy} onClick={() => void inspectLatest()}>Inspect latest revision again</button></div>}
        <section className="approval-record" aria-labelledby="approvals-title">
          <h3 id="approvals-title">{historical ? 'Historical approval records' : 'Effective approval at inspection'}</h3>
          {view.approvals.length === 0 && <p className="muted">{historical ? 'No approvals were recorded for this revision.' : 'This inspected revision has no effective approval.'}</p>}
          <ul className="record-list">
            {view.approvals.map((approval, index) => <li key={`${approval.reviewer}-${index}`}><strong>{approval.reviewer}</strong> · {dateLabel(approval.createdAt)}<br /><span className="muted">{historical ? 'Historical approval' : 'Design approval'} for revision {approval.revision}</span><br /><code>{approval.digest}</code></li>)}
          </ul>
          {view.approvalsTruncated && <p className="warning">Approval records are truncated. The full approval history is not displayed.</p>}
        </section>
        <RepositoryContext content={view.revision.content} linkedReceipt={inspectedCollection?.receipt} disabled={busy}
          onInspectCollection={browserAccess ? requestCollectionInspection : undefined} />
        {repository && <button className="secondary related-work" disabled={busy} onClick={() => {
          setRelated(previous => ({ repository, sequence: (previous?.sequence ?? 0) + 1 }));
          document.getElementById('shared-work-title')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }}>{browserAccess ? 'Related work with this context label' : 'Related work in this repository'}</button>}
        <section className="content" aria-labelledby="content-title"><h3 id="content-title">Inspected package content</h3><PackageContent content={view.revision.content} /></section>
        <section className="compare-controls" aria-labelledby="compare-title">
          <h3 id="compare-title">Compare content</h3>
          <div className="control-row"><label>Compare inspected revision with<select value={comparisonTarget} disabled={busy}
            onChange={event => { setComparisonTarget(event.target.value); setComparison(undefined); }}>
            <option value="previous" disabled={view.revision.number <= 1}>Previous revision{view.revision.number > 1 ? ` (${view.revision.number - 1})` : ' — none'}</option>
            <option value="latest" disabled={view.revision.number === pkg.revision.number}>Latest inspected revision ({pkg.revision.number})</option>
          </select></label><button className="secondary" disabled={busy || !targetNumber || targetNumber < 1 || targetNumber === view.revision.number} onClick={() => void compare()}>Compare revisions</button></div>
          {view.revision.number === 1 && pkg.revision.number === 1 && <p className="muted">Only one revision has been inspected. There is no earlier revision to compare.</p>}
          {comparison && <Comparison before={comparison} after={view.revision} />}
        </section>
        {!historical && <section className="approval-action" aria-labelledby="approval-title">
          <h3 id="approval-title">Approve inspected design</h3>
          <p>Approval binds only to revision <strong>{view.revision.number}</strong> and this digest:</p><code>{view.revision.digest}</code>
          <p className="muted">Design approval does not assert that code, tests, deployment, or production outcomes were verified.</p>
          {reviewer === view.revision.author && <p className="warning">An independent reviewer is required. You authored this revision.</p>}
          {!approvalPermission && <p className="warning">{browserAccess?.principalKind === 'agent' ? 'Agent identities cannot approve designs. An authorized human reviewer is required.' : 'Your repository permissions do not include design approval.'}</p>}
          {!view.revision.submittedAt && <p className="warning">This draft must be submitted before it can be approved.</p>}
          <button disabled={!canApprove} onClick={() => void approve()}>{pkg.approved ? 'Design approval recorded' : 'Approve inspected revision'}</button>
        </section>}
      </article>
      <aside>
        <History page={history} error={historyError} selected={view.revision.number} disabled={busy} onSelect={revision => void inspectHistory(revision)} onMore={() => void loadMore('history')} />
        <Events page={events} error={eventsError} disabled={busy} onMore={() => void loadMore('events')} />
        <button className="secondary refresh" disabled={busy} onClick={() => void refreshRecords()}>Refresh history and events</button>
      </aside>
    </div>}
    </section>
    <section role="tabpanel" id="workflow-source" aria-labelledby="workflow-tab-source" tabIndex={0} hidden={workflow !== 'source'}>
      <section className="panel attachment-target" aria-label="Package for source attachment">
        <h3>Attach source to a reviewed package</h3>
        {pkg && view ? <><p>{pkg.id} · revision {view.revision.number} · {historical ? 'Historical; attachment unavailable' : inspectionRequired || attachmentRecovery ? 'Renewed inspection required' : 'Latest inspected revision'}</p><code>{view.revision.digest}</code></> : <p>Inspect a package in Review to select the exact revision for attachment.</p>}
        <button className="secondary" onClick={() => setWorkflow('review')}>Review package</button>
        {attachmentRecovery && <p className="warning">Attachment recovery requires both inspections. Review the latest package and inspect this collection again before another decision.</p>}
      </section>
    {browserAccess ? <ContextCollections access={browserAccess} visible={workflow === 'source'} disabled={pending !== ''}
      target={pkg && view && !historical && !authorDraft && !assistanceLocked && !inspectionRequired && !attachmentRecovery && pending === ''
        ? { id: pkg.id, revision: view.revision.number, digest: view.revision.digest, content: view.revision.content } : undefined}
      requestedInspection={requestedCollection} onAccessFailure={onAccessFailure} onBusyChange={collectionWork}
      onInspected={collectionInspected} onAttached={attached}
      onAttachmentUncertain={(packageID, collectionID) => {
        setAttachmentRecovery({ packageID, collectionID, packageInspected: false, collectionInspected: false });
        setInspectionRequired('A context attachment was stale or could not be confirmed. Inspect the latest package and collection before another attachment or approval.');
      }} />
      : <section className="panel local-collections" aria-label="Remote collection availability"><h3>Shared context collections</h3><p className="muted">Remote collection requires authenticated workspace and repository access. It is unavailable in local mode.</p></section>}
      {browserAccess && <RepositoryGraphs access={browserAccess} visible={workflow === 'source'} onAccessFailure={onAccessFailure} />}
    </section>
    <section role="tabpanel" id="workflow-agents" aria-labelledby="workflow-tab-agents" tabIndex={0} hidden={workflow !== 'agents'}>
      {browserAccess ? <CoordinatedRuns access={browserAccess} visible={workflow === 'agents'} onAccessFailure={onAccessFailure} /> : <SharedAccessRequired />}
    </section>
    <section role="tabpanel" id="workflow-delivery" aria-labelledby="workflow-tab-delivery" tabIndex={0} hidden={workflow !== 'delivery'}>
      {browserAccess ? <RepositoryDeliveries access={browserAccess} visible={workflow === 'delivery'} onAccessFailure={onAccessFailure} /> : <SharedAccessRequired />}
    </section>
    <section role="tabpanel" id="workflow-tracker" aria-labelledby="workflow-tab-tracker" tabIndex={0} hidden={workflow !== 'tracker'}>
      {browserAccess ? <WorkTracking access={browserAccess} visible={workflow === 'tracker'} onAccessFailure={onAccessFailure} /> : <SharedAccessRequired />}
    </section>
    <section role="tabpanel" id="workflow-runtime" aria-labelledby="workflow-tab-runtime" tabIndex={0} hidden={workflow !== 'runtime'}>
      {browserAccess ? <RuntimeEvidence access={browserAccess} visible={workflow === 'runtime'} onAccessFailure={onAccessFailure} /> : <SharedAccessRequired />}
    </section>
  </main>;
}

function SharedAccessRequired() {
  return <section className="panel"><h2>Shared workspace access required</h2><p>This workflow needs an authenticated workspace and managed repository. Local packages remain available in Review.</p></section>;
}

export function WorkbenchHeader({ compact = false }: { compact?: boolean }) {
  return <header className={compact ? 'compact-header' : undefined}>
    <img className="brand-logo" src="/brand/conductor-logo.svg" alt="Conductor"/>
    <h1>{compact ? "Engineering intent, orchestrated." : <>Engineering intent,<br />orchestrated.</>}</h1>
    <p className="intro">Shared design, coordinated agents, and evidence for every decision.</p>
  </header>;
}
