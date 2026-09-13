import { useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { APIError, accessFailure, changePath, dateLabel, errorMessage, request } from './api';
import type { BrowserAccess, EventPage, HistoryPage, RequestAccess, Revision, RevisionView, WorkPackage } from './api';
import { Comparison } from './Comparison';
import { RepositoryContext } from './RepositoryContext';
import { Events, History } from './Records';
import { SharedChanges } from './SharedChanges';
import type { RelatedRequest } from './SharedChanges';
import { ContextCollections } from './ContextCollections';
import type { Collection } from './collections';

const perspectives = {
  Architect: 'Inspect boundaries, contracts, constraints, and the decisions behind this design.',
  'QC / QA': 'Inspect acceptance criteria, verification plans, and missing or unexecuted evidence.',
  Developer: 'Inspect scope, task dependencies, implementation constraints, and verification commands.',
  Product: 'Inspect the intended outcome, business rules, and measurable acceptance criteria.',
};

interface Job { token: number; signal: AbortSignal; access: RequestAccess }
interface AttachmentRecovery { packageID: string; collectionID: string; packageInspected: boolean; collectionInspected: boolean }

export function ReviewWorkbench({ access: browserAccess, sessionControls, onAccessFailure }: {
  access?: BrowserAccess;
  sessionControls?: ReactNode;
  onAccessFailure?: (failure: APIError) => void;
}) {
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
  const access: RequestAccess = browserAccess ?? { mode: 'local', actor };
  const reviewer = access.mode === 'browser' ? access.principalID : access.actor;
  const approvalPermission = access.mode === 'local' || (access.principalKind === 'human' && access.canApprove);

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
    if (working.current || collectionWorking.current) return;
    working.current = true;
    controller.current?.abort();
    controller.current = new AbortController();
    const token = ++sequence.current;
    setPending(label);
    setError('');
    return { token, signal: controller.current.signal, access };
  }

  // Cancellation alone cannot stop an already-resolved response from replacing a newer inspection.
  function current(job: Job) { return sequence.current === job.token && !job.signal.aborted; }

  function finish(job: Job) {
    if (current(job)) { working.current = false; setPending(''); }
  }

  function failed(job: Job, failure: unknown) {
    if (!current(job)) return;
    if (accessFailure(failure, job.access)) {
      clearInspection();
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

  const canApprove = !!pkg && !!view && !historical && !inspectionRequired && !attachmentRecovery && !busy
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
    setRequestedCollection(previous => ({ id: collectionID, sequence: (previous?.sequence ?? 0) + 1 }));
    document.getElementById('collections-title')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  return <main>
    <WorkbenchHeader />
    {sessionControls}
    <form className="lookup panel" onSubmit={event => { event.preventDefault(); void inspectLatest(); }}>
      <label>Change ID<input value={id} maxLength={256} placeholder="CHG-…" disabled={busy} required
        onChange={event => { clearInspection(); setID(event.target.value); }} /></label>
      {!browserAccess && <label>Local reviewer<input value={actor} maxLength={128} disabled={busy} required
        onChange={event => { clearInspection(); setActor(event.target.value); }} /></label>}
      <button type="submit" disabled={busy || !id.trim() || !reviewer.trim()}>Inspect latest revision</button>
      <p className="local-note">{browserAccess ? 'Inspection is limited to your selected workspace and managed repository.' : 'Local development identity only. These packages are separate from authenticated workspaces.'}</p>
    </form>
    <section className="perspective" aria-label="Review perspective guidance">
      <label htmlFor="review-perspective">Review perspective<select id="review-perspective" value={perspective}
        onChange={event => setPerspective(event.target.value as keyof typeof perspectives)}>
        {Object.keys(perspectives).map(value => <option key={value}>{value}</option>)}
      </select></label>
      <div><p>{perspectives[perspective]}</p><p className="muted">Changes review prompts only. This selection grants no permissions or approval rights.</p></div>
    </section>
    <SharedChanges key={reviewer} access={access} disabled={busy} related={related} onInspect={packageID => void inspectLatest(packageID)} onAccessFailure={onAccessFailure} />
    {browserAccess ? <ContextCollections access={browserAccess} disabled={pending !== ''}
      target={pkg && view && !historical && !inspectionRequired && !attachmentRecovery && pending === ''
        ? { id: pkg.id, revision: view.revision.number, digest: view.revision.digest, content: view.revision.content } : undefined}
      requestedInspection={requestedCollection} onAccessFailure={onAccessFailure} onBusyChange={collectionWork}
      onInspected={collectionInspected} onAttached={attached}
      onAttachmentUncertain={(packageID, collectionID) => {
        setAttachmentRecovery({ packageID, collectionID, packageInspected: false, collectionInspected: false });
        setInspectionRequired('A context attachment was stale or could not be confirmed. Inspect the latest package and collection before another attachment or approval.');
      }} />
      : <section className="panel local-collections" aria-label="Remote collection availability"><h3>Shared context collections</h3><p className="muted">Remote collection requires authenticated workspace and repository access. It is unavailable in local mode.</p></section>}
    <div className="request-status" role="status" aria-live="polite">{pending || notice}</div>
    {error && <p role="alert" className="error banner">{error}</p>}
    {inspectionRequired && <div role="alert" className="warning banner"><strong>Renewed inspection required.</strong> {inspectionRequired}</div>}
    {attachmentRecovery && <div className="warning banner" role="alert"><strong>Attachment recovery requires both inspections.</strong>
      <p>Package <code>{attachmentRecovery.packageID}</code>: {attachmentRecovery.packageInspected ? 'inspected again' : 'latest inspection required'}. Collection <code>{attachmentRecovery.collectionID}</code>: {attachmentRecovery.collectionInspected ? 'inspected again' : 'inspection required'}. Approval and attachment stay blocked until both are inspected.</p>
      <div className="control-row"><button className="secondary" disabled={busy} onClick={() => void inspectLatest(attachmentRecovery.packageID)}>Inspect recovery package</button>
        <button className="secondary" disabled={busy} onClick={() => requestCollectionInspection(attachmentRecovery.collectionID)}>Inspect recovery collection</button></div>
    </div>}
    {!pkg && !busy && !error && <section className="empty"><h2>A clear record for every decision.</h2><p>Enter a change ID to inspect its revision, context, approvals, and audit history.</p></section>}
    {pkg && view && <div className="workbench-grid" aria-busy={busy}>
      <article className="review panel" aria-labelledby="revision-title">
        <div className="status"><span>{pkg.id}</span><strong>{inspectionRequired ? 'INSPECTION STALE' : historical ? 'HISTORICAL VIEW' : pkg.approved ? 'DESIGN APPROVED' : view.revision.submittedAt ? 'REVIEW REQUIRED' : 'DRAFT'}</strong></div>
        <div className="section-heading"><h2 id="revision-title">Revision {view.revision.number}</h2><span className="tag">{historical ? 'Preserved record' : 'Latest inspected'}</span></div>
        <dl>
          <dt>Digest</dt><dd><code>{view.revision.digest}</code></dd>
          <dt>Author</dt><dd>{view.revision.author}</dd>
          {browserAccess && <><dt>Workspace</dt><dd>{pkg.workspaceId}</dd><dt>Repository</dt><dd>{pkg.repositoryId}</dd></>}
          <dt>Created</dt><dd>{dateLabel(view.revision.createdAt)}</dd>
          <dt>Submitted</dt><dd>{view.revision.submittedAt ? dateLabel(view.revision.submittedAt) : 'Not submitted'}</dd>
        </dl>
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
        <section className="content" aria-labelledby="content-title"><h3 id="content-title">Inspected package content</h3><p className="muted">Complete structured content, including fields this workbench does not interpret.</p><pre>{JSON.stringify(view.revision.content, null, 2)}</pre></section>
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
  </main>;
}

export function WorkbenchHeader() {
  return <header>
    <p className="eyebrow">CONDUCTOR / REVIEW WORKBENCH</p>
    <h1>Engineering intent,<br />orchestrated.</h1>
    <p className="intro">Inspect the design. Follow its history. Approve the exact content you reviewed.</p>
  </header>;
}
