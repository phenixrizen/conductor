import { useEffect, useRef, useState } from 'react';
import { APIError, accessFailure, changePath, dateLabel, errorMessage, request } from './api';
import type { BrowserAccess, WorkPackage } from './api';
import { RepositoryContext } from './RepositoryContext';
import { collectionInput, collectionPath, executionLabel, sameJSON, uncertainMutation, validateCollection, validateCollectionPage } from './collections';
import type { AttachmentTarget, Collection, CollectionExecution, CollectionInput, CollectionPage } from './collections';

type Creation = { input: CollectionInput; key: string; outcome: 'pending' | 'uncertain' | 'recorded' | 'rejected' };
type Confirmation = { kind: 'cancel'; collection: Collection } | { kind: 'attach'; collection: Collection; target: AttachmentTarget };
interface Job { sequence: number; signal: AbortSignal }

export function ContextCollections({ access, disabled, target, requestedInspection, onAccessFailure, onBusyChange, onInspected, onAttached, onAttachmentUncertain }: {
  access: BrowserAccess;
  disabled: boolean;
  target?: AttachmentTarget;
  requestedInspection?: { id: string; sequence: number };
  onAccessFailure?: (failure: APIError) => void;
  onBusyChange: (active: boolean) => boolean;
  onInspected: (value?: Collection, freshInspection?: boolean) => void;
  onAttached: (value: WorkPackage) => void;
  onAttachmentUncertain: (packageID: string, collectionID: string) => void;
}) {
  const [page, setPage] = useState<CollectionPage>();
  const [selected, setSelected] = useState<Collection>();
  const [needsInspection, setNeedsInspection] = useState(false);
  const [confirmation, setConfirmation] = useState<Confirmation>();
  const [commit, setCommit] = useState('');
  const [paths, setPaths] = useState('');
  const [fullSource, setFullSource] = useState(false);
  const [key, setKey] = useState<string>(() => crypto.randomUUID());
  const [creation, setCreation] = useState<Creation>();
  const [pending, setPending] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [now, setNow] = useState(Date.now);
  const sequence = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const working = useRef(false);
  const busy = disabled || pending !== '';
  const lockedDraft = !!creation && creation.outcome !== 'rejected';

  useEffect(() => {
    // Age observations without fetching or changing the inspected request/receipt.
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => { clearInterval(timer); sequence.current++; controller.current?.abort(); onBusyChange(false); };
  }, []);

  useEffect(() => {
    if (requestedInspection) void inspect(requestedInspection.id);
  }, [requestedInspection]);

  // A confirmation cannot survive a change to the package being reviewed.
  useEffect(() => { setConfirmation(undefined); }, [target?.id, target?.revision, target?.digest]);

  function begin(label: string): Job | undefined {
    if (disabled || working.current || !onBusyChange(true)) return;
    working.current = true;
    controller.current?.abort();
    controller.current = new AbortController();
    setPending(label);
    setError('');
    setNotice('');
    setConfirmation(undefined);
    return { sequence: ++sequence.current, signal: controller.current.signal };
  }
  function current(job: Job): boolean { return sequence.current === job.sequence && !job.signal.aborted; }
  function finish(job: Job) {
    if (current(job)) { working.current = false; setPending(''); onBusyChange(false); }
  }
  function failed(job: Job, failure: unknown) {
    if (!current(job)) return;
    if (accessFailure(failure, access)) {
      sequence.current++;
      controller.current?.abort();
      working.current = false;
      setPending(''); setPage(undefined); setSelected(undefined); setConfirmation(undefined);
      setCreation(undefined); setCommit(''); setPaths(''); setFullSource(false); setKey(''); setNotice(''); setError('');
      onInspected(undefined); onBusyChange(false); onAccessFailure?.(failure);
    } else setError(errorMessage(failure));
  }
  function inspected(value?: Collection, freshInspection = false) { setSelected(value); onInspected(value, freshInspection); }

  async function list(more = false) {
    if (more && !page?.nextBefore) return;
    const job = begin(more ? 'Loading more collections…' : 'Refreshing shared collections…');
    if (!job) return;
    if (!more) setPage(undefined);
    try {
      const query = new URLSearchParams({ limit: '20' });
      if (more && page?.nextBefore) query.set('before', page.nextBefore);
      const value = await request<CollectionPage>(`/context-collections?${query}`, access, job.signal);
      if (!current(job)) return;
      validateCollectionPage(value, access);
      // Keep one bounded page in memory. The cursor explicitly marks omitted work.
      setPage(value);
      setNotice('Collection list refreshed. Use an inspection button to read a request and its receipt.');
    } catch (failure) { failed(job, failure); }
    finally { finish(job); }
  }

  async function inspect(id: string) {
    const job = begin('Inspecting collection…');
    if (!job) return;
    inspected(undefined);
    setNeedsInspection(true);
    try {
      const value = await request<Collection>(collectionPath(id), access, job.signal);
      if (!current(job)) return;
      validateCollection(value, access, id);
      inspected(value, true);
      setNeedsInspection(false);
      setNotice('Collection inspected. Progress is a timestamped observation; source coverage is separate.');
    } catch (failure) { failed(job, failure); }
    finally { finish(job); }
  }

  async function create() {
    if (!access.canAuthor || creation?.outcome === 'recorded') return;
    let captured: Creation;
    try {
      captured = creation?.outcome === 'uncertain' ? creation : { input: { ...collectionInput(commit, paths), ...(fullSource ? { fullSource: true } : {}) }, key, outcome: 'pending' };
      if (!/^[\x21-\x2b\x2d-\x7e]{1,128}$/.test(captured.key)) throw new Error('Use a 1–128 character printable ASCII idempotency key without spaces or commas.');
    } catch (failure) { setError(errorMessage(failure)); return; }
    const job = begin('Recording collection request…');
    if (!job) return;
    setCreation({ ...captured, outcome: 'pending' });
    try {
      const value = await request<Collection>('/context-collections', access, job.signal, captured.input, { idempotencyKey: captured.key });
      if (!current(job)) return;
      validateCollection(value, access);
      if (value.requesterId !== access.principalID || !sameJSON(value.input, captured.input)) throw new Error('The response does not match the exact request. Retry the same input and key to recover it.');
      setCreation({ ...captured, outcome: 'recorded' });
      inspected(value); setNeedsInspection(false);
      setNotice('Collection request recorded. This confirms saved intent; it does not confirm execution or complete source coverage. Refresh the list or inspect the collection explicitly to see later observations.');
    } catch (failure) {
      failed(job, failure);
      if (current(job)) {
        const uncertain = uncertainMutation(failure);
        setCreation({ ...captured, outcome: uncertain ? 'uncertain' : 'rejected' });
        if (uncertain) setNotice('Request outcome unknown. The exact input and idempotency key are retained. Retry this same request explicitly to recover its identity.');
      }
    } finally { finish(job); }
  }

  function newRequest() {
    if (busy || creation?.outcome === 'uncertain') return;
    setCreation(undefined); setCommit(''); setPaths(''); setFullSource(false); setKey(crypto.randomUUID()); setError(''); setNotice('');
  }

  async function confirm() {
    const captured = confirmation;
    if (!captured || !access.canAuthor || needsInspection || selected?.id !== captured.collection.id) return;
    if (captured.kind === 'attach' && (!target || target.id !== captured.target.id || target.revision !== captured.target.revision || target.digest !== captured.target.digest)) {
      setConfirmation(undefined); return;
    }
    const job = begin(captured.kind === 'cancel' ? 'Recording cancellation request…' : 'Attaching inspected receipt…');
    if (!job) return;
    try {
      // These commands use only the facts captured by the visible confirmation.
      // No GET, collection refresh, or newer package lookup occurs here.
      if (captured.kind === 'cancel') {
        const value = await request<Collection>(`${collectionPath(captured.collection.id)}/cancellation`, access, job.signal, {});
        if (!current(job)) return;
        validateCollection(value, access, captured.collection.id);
        if (value.requesterId !== access.principalID || value.requesterId !== captured.collection.requesterId
          || value.inputDigest !== captured.collection.inputDigest || !sameJSON(value.input, captured.collection.input)
          || !sameJSON(value.source, captured.collection.source) || (!value.receipt && !value.cancelRequestedAt)) {
          throw new Error('The server did not confirm cancellation intent for this request. Inspect the collection before trying again.');
        }
        inspected(value);
        setNotice(value.receipt ? 'A receipt committed before cancellation. It remains available for inspection.'
          : 'Cancellation requested. Only an observed cancelled execution confirms that it stopped. Refresh the inspected collection to check later.');
      } else {
        const receipt = captured.collection.receipt!;
        const value = await request<WorkPackage>(`${changePath(captured.target.id)}/context-attachments`, access, job.signal,
          { expectedRevision: captured.target.revision, collectionId: captured.collection.id, digest: receipt.digest });
        if (!current(job)) return;
        if (!value || value.id !== captured.target.id || value.workspaceId !== access.workspaceID || value.repositoryId !== access.repositoryID
          || value.revision?.changeId !== captured.target.id || value.revision.number !== captured.target.revision + 1
          || !/^[a-f0-9]{64}$/.test(value.revision.digest) || value.revision.author !== access.principalID
          || value.approved !== false || value.approval !== undefined || value.revision.submittedAt !== undefined
          || !sameJSON(value.revision.content, { ...captured.target.content, repositoryContext: receipt.snapshot })) {
          throw new Error('The attachment response does not match the captured package and receipt. Inspect the latest package before continuing.');
        }
        onAttached(value);
        setNotice(`Receipt attached as revision ${value.revision.number}. Earlier approval does not apply to the new draft.`);
      }
    } catch (failure) {
      failed(job, failure);
      if (current(job) && (uncertainMutation(failure) || (failure instanceof APIError && failure.status === 409))) {
        setNeedsInspection(true);
        if (captured.kind === 'attach') onAttachmentUncertain(captured.target.id, captured.collection.id);
        setNotice(captured.kind === 'attach'
          ? 'Attachment outcome is uncertain or stale. Inspect both the latest package and the collection before another attachment or approval.'
          : 'Cancellation outcome is uncertain or stale. Refresh the inspected collection before another cancellation request.');
      }
    } finally { finish(job); }
  }

  const canCancel = access.canAuthor && !!selected && !needsInspection && selected.requesterId === access.principalID
    && !selected.receipt && !selected.cancelRequestedAt && !['completed', 'cancelled', 'failed', 'timed_out', 'terminated'].includes(selected.execution?.state ?? '');
  const canAttach = access.canAuthor && !!selected?.receipt && !!target && !needsInspection;

  return <section className="context-collections panel" aria-labelledby="collections-title" aria-busy={pending !== ''}>
    <div className="section-heading"><div><p className="eyebrow">SHARED REPOSITORY SOURCE</p><h2 id="collections-title">Shared context collections</h2></div>
      <button className="secondary" disabled={busy} onClick={() => void list()}>Refresh collections</button></div>
    <p className="muted">Capture explicit files at an exact commit for everyone with repository access. A saved request, observed execution, and collected source are separate facts. Updates require an explicit refresh.</p>
    {!access.canAuthor && <p className="warning">Read-only repository access. You can inspect shared collections; author permission and an operator-enabled read integration are required to request or attach source.</p>}
    {access.canAuthor && <details className="collection-create" open={creation?.outcome === 'uncertain' || undefined}>
      <summary>Request repository context</summary>
      <form onSubmit={event => { event.preventDefault(); void create(); }}>
        <div className="control-row"><label htmlFor="collection-commit">Exact commit<input id="collection-commit" value={commit} maxLength={40} placeholder="Full lowercase 40-hex commit ID" required disabled={busy || lockedDraft} onChange={event => setCommit(event.target.value)} /></label>
          <label htmlFor="collection-key">Idempotency key<input id="collection-key" value={key} maxLength={128} required disabled={busy || lockedDraft} onChange={event => setKey(event.target.value)} /></label></div>
        <label htmlFor="collection-paths">Explicit paths (one per line)<textarea id="collection-paths" value={paths} rows={4} maxLength={33000} required disabled={busy || lockedDraft} placeholder={'specs/example/spec.md\ndocs/adr/example.md'} onChange={event => setPaths(event.target.value)} /></label>
        <label className="checkbox-label"><input type="checkbox" checked={fullSource} disabled={busy || lockedDraft} onChange={event => setFullSource(event.target.checked)} /> Include whole-repository source for graph and coding work</label>
        {fullSource && <p className="muted">Retain the original Git commit and complete bounded source bundle, alongside the selected review files. Index coverage has separate file and text limits; gaps remain visible.</p>}
        <p className="muted">Use 1–32 unique relative file paths. The server sorts them and reads the operator-configured repository. Collection does not execute source or modify a package. Keep the same key and input when retrying an unknown result.</p>
        <div className="control-row"><button disabled={busy || creation?.outcome === 'recorded'} type="submit">{creation?.outcome === 'uncertain' ? 'Retry same request' : creation?.outcome === 'recorded' ? 'Request recorded' : 'Request collection'}</button>
          {creation?.outcome === 'recorded' && <button className="secondary" type="button" disabled={busy} onClick={newRequest}>Start a new request</button>}</div>
        {creation?.outcome === 'uncertain' && <p className="warning">Unknown request outcome. Inputs and key are locked for a safe, explicit retry of this same request.</p>}
      </form>
    </details>}
    <div role="status" aria-live="polite" className="request-status">{pending || notice}</div>
    {error && <p className="error" role="alert">{error}</p>}
    {!page && <p className="muted">Collections have not been loaded. Refresh to discover shared requests.</p>}
    {page?.collections.length === 0 && <p className="empty-list">No collections are recorded in this repository.</p>}
    {page && <>
      <ul className="record-list shared-list" aria-label="Shared collections">{page.collections.map(item => <li key={item.id}><button className="shared-card" disabled={busy} aria-label={`Inspect collection ${item.id}`} onClick={() => void inspect(item.id)}>
        <strong>{item.id}</strong><code>{item.commit}</code><span>Requested by {item.requesterId} · {dateLabel(item.createdAt)}</span>
        <span>{item.receiptDigest ? 'Receipt available — inspect coverage' : 'No receipt recorded'}</span>
        {item.cancelRequestedAt && <span>Cancellation requested · {dateLabel(item.cancelRequestedAt)}</span>}
        <span>{executionLabel(item.execution, now)}</span><span>Observed: {dateLabel(item.execution?.observedAt)}</span>
      </button></li>)}</ul>
      {page.nextBefore && <><p className="warning">More collections exist. This page is bounded to 20 requests.</p><button className="secondary" disabled={busy} onClick={() => void list(true)}>Load more collections</button></>}
    </>}
    {selected && <article className="collection-inspection" aria-labelledby="collection-inspection-title">
      <div className="section-heading"><h3 id="collection-inspection-title">Collection inspection</h3><button className="secondary" disabled={busy} onClick={() => void inspect(selected.id)}>Refresh inspected collection</button></div>
      <dl><dt>Collection ID</dt><dd><code>{selected.id}</code></dd><dt>Requester</dt><dd>{selected.requesterId}</dd><dt>Requested</dt><dd>{dateLabel(selected.createdAt)}</dd>
        <dt>Commit</dt><dd><code>{selected.input.commit}</code></dd><dt>Input digest</dt><dd><code>{selected.inputDigest}</code></dd><dt>Paths</dt><dd><ul className="literal-paths">{selected.input.paths.map(path => <li key={path}><code>{path}</code></li>)}</ul></dd>
        <dt>Workspace</dt><dd>{selected.workspaceId}</dd><dt>Repository</dt><dd>{selected.repositoryId}</dd><dt>Provider</dt><dd>{selected.source.provider} · {selected.source.host} · {selected.source.providerId}</dd>
        <dt>Locator</dt><dd>{selected.source.locator || 'Canonical provider ID'}</dd><dt>Read profile</dt><dd>{selected.source.profile} · integration version {selected.source.integrationVersion}</dd></dl>
      <Execution execution={selected.execution} now={now} />
      {selected.input.fullSource && <section aria-label="Whole-repository source"><h4>Whole-repository source</h4>{selected.fullSource ? <><dl>
        <dt>Bundle digest</dt><dd><code>{selected.fullSource.digest}</code></dd><dt>Original tree</dt><dd><code>{selected.fullSource.tree}</code></dd>
        <dt>Files</dt><dd>{selected.fullSource.fileCount} in the source tree; {selected.fullSource.indexedFiles} retained index artifacts</dd><dt>Bundle size</dt><dd>{selected.fullSource.size.toLocaleString()} bytes</dd>
        <dt>Retained</dt><dd>{dateLabel(selected.fullSource.createdAt)}</dd></dl><p className={selected.fullSource.truncated ? 'warning' : 'muted'}>{selected.fullSource.truncated ? 'Index coverage is truncated. The graph must retain these gaps.' : 'Index artifacts fit the collection bounds. Unsupported files and unresolved relationships remain separate evidence gaps.'}</p></>
        : <p className="warning">No whole-repository bundle is recorded. Coding source remains unavailable.</p>}</section>}
      {selected.cancelRequestedAt && <p className="warning">Cancellation requested at {dateLabel(selected.cancelRequestedAt)}. This request is not proof that execution stopped; check the timestamped execution observation.</p>}
      {needsInspection && <p className="warning" role="alert">Renewed collection inspection required before another mutation.</p>}
      {selected.receipt ? <section aria-label="Inspected collection receipt"><h4>Immutable source receipt</h4><dl><dt>Receipt digest</dt><dd><code>{selected.receipt.digest}</code></dd><dt>Committed</dt><dd>{dateLabel(selected.receipt.createdAt)}</dd></dl>
        <RepositoryContext content={{ repositoryContext: selected.receipt.snapshot }} linkedReceipt={selected.receipt} />
      </section> : <p className="warning">No receipt is recorded. Source coverage remains unknown, including when execution is completed or cancellation was requested.</p>}
      <div className="control-row collection-actions">
        {access.canAuthor && selected.requesterId === access.principalID && <button className="secondary" disabled={busy || !canCancel} onClick={() => setConfirmation({ kind: 'cancel', collection: selected })}>Request cancellation</button>}
        {access.canAuthor && selected.receipt && <button disabled={busy || !canAttach} onClick={() => { if (target) setConfirmation({ kind: 'attach', collection: selected, target }); }}>Attach receipt to inspected revision</button>}
      </div>
      {access.canAuthor && selected.receipt && !target && <p className="muted">Inspect a current package revision in this repository to attach this receipt. Historical or stale package views cannot be changed.</p>}
      {selected.requesterId !== access.principalID && !selected.receipt && <p className="muted">Only the currently authorized requester can request cancellation.</p>}
      <details><summary>Complete collection JSON</summary><pre>{JSON.stringify(selected, null, 2)}</pre></details>
    </article>}
    {confirmation && <section className="collection-confirmation warning" role="dialog" aria-labelledby="collection-confirmation-title" onKeyDown={event => { if (event.key === 'Escape') setConfirmation(undefined); }}>
      <h3 id="collection-confirmation-title">{confirmation.kind === 'cancel' ? 'Confirm collection cancellation' : 'Confirm context attachment'}</h3>
      <dl><dt>Collection ID</dt><dd><code>{confirmation.collection.id}</code></dd><dt>Commit</dt><dd><code>{confirmation.collection.input.commit}</code></dd>
        {confirmation.kind === 'attach' && <><dt>Package</dt><dd>{confirmation.target.id}</dd><dt>Revision</dt><dd>{confirmation.target.revision}</dd><dt>Package digest</dt><dd><code>{confirmation.target.digest}</code></dd><dt>Receipt digest</dt><dd><code>{confirmation.collection.receipt?.digest}</code></dd></>}
      </dl>
      <p>{confirmation.kind === 'cancel' ? 'This records a cancellation request for the displayed collection. It cannot undo a receipt already committed and does not confirm execution stopped.'
        : 'Attach the displayed immutable receipt to this exact package revision. This creates a new draft with no effective approval. No source or package refresh occurs in this command.'}</p>
      <div className="control-row"><button autoFocus disabled={busy} onClick={() => void confirm()}>{confirmation.kind === 'cancel' ? 'Confirm cancellation request' : 'Confirm attachment'}</button>
        <button className="secondary" disabled={busy} onClick={() => setConfirmation(undefined)}>Back to inspection</button></div>
    </section>}
  </section>;
}

function Execution({ execution, now }: { execution?: CollectionExecution; now: number }) {
  return <section className="collection-execution" aria-label="Execution observation"><h4>Execution observation</h4><p className="warning">{executionLabel(execution, now)}</p>
    {execution && <dl><dt>Observed</dt><dd>{dateLabel(execution.observedAt)}</dd><dt>Namespace</dt><dd>{execution.namespace}</dd><dt>Workflow</dt><dd><code>{execution.workflowId}</code></dd><dt>Run</dt><dd><code>{execution.runId || 'Unknown acknowledgment — run ID unavailable'}</code></dd></dl>}
    <p className="muted">This is the last recorded workflow observation. Refresh explicitly to inspect later progress. Completion does not mean every path was collected or any check passed.</p>
  </section>;
}
