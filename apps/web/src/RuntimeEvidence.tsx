import { useWorkflowActivity } from './workflowActivity';
import { useEffect, useRef, useState } from 'react';
import { APIError, accessFailure, dateLabel, errorMessage, request } from './api';
import type { BrowserAccess } from './api';
import { executionLabel, sameJSON, uncertainMutation } from './collections';
import { isHex } from './graphs';
import { parseStrictJSON } from './strictJSON';
import { visibleControls } from './sourceText';
import { runtimeFreshness, runtimeInput, runtimePath, validateRuntime, validateRuntimePage } from './runtime';
import type { RuntimeEvidence as Evidence, RuntimeInput, RuntimePage, RuntimeSignal } from './runtime';

type Job = { sequence: number; signal: AbortSignal };
type Creation = { input: RuntimeInput; key: string; outcome: 'pending' | 'recorded' | 'uncertain' };
const sourceJSON = (v: unknown) => visibleControls(JSON.stringify(v, null, 2));

export function RuntimeEvidence({ access, visible = true, onAccessFailure }: { access: BrowserAccess; visible?: boolean; onAccessFailure?: (failure: APIError) => void }) {
  const [page, setPage] = useState<RuntimePage>();
  const [evidence, setEvidence] = useState<Evidence>();
  const [id, setID] = useState('');
  const [draft, setDraft] = useState('');
  const [preview, setPreview] = useState<RuntimeInput>();
  const [creation, setCreation] = useState<Creation>();
  const [pending, setPending] = useState(''), [notice, setNotice] = useState(''), [error, setError] = useState('');
  const [now, setNow] = useState(Date.now());
  const working = useRef(false), sequence = useRef(0), controller = useRef<AbortController | undefined>(undefined);
  const busy = !!pending, locked = creation?.outcome === 'pending' || creation?.outcome === 'uncertain';

  const workflowActive = useWorkflowActivity(visible, () => {
    sequence.current++; controller.current?.abort(); working.current = false; setPending('');
    setCreation(value => value?.outcome === 'pending' ? { ...value, outcome: 'uncertain' } : value);
  });

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 5000);
    return () => { clearInterval(timer); sequence.current++; controller.current?.abort(); };
  }, []);

  function begin(label: string): Job | undefined {
    if (!workflowActive.current || working.current) return;
    working.current = true; controller.current?.abort(); controller.current = new AbortController();
    setPending(label); setError(''); setNotice('');
    return { sequence: ++sequence.current, signal: controller.current.signal };
  }
  function active(job: Job) { return workflowActive.current && sequence.current === job.sequence && !job.signal.aborted; }
  function finish(job: Job) { if (active(job)) { working.current = false; setPending(''); } }
  function fail(job: Job, failure: unknown) {
    if (!active(job)) return;
    if (failure instanceof APIError && [401, 403, 404].includes(failure.status)) {
      // Invalidate before clearing: a late response must not repopulate private
      // telemetry or an uncertain request after a related source grant is revoked.
      sequence.current++; controller.current?.abort(); working.current = false; setPending('');
      setPage(undefined); setEvidence(undefined); setID(''); setDraft(''); setPreview(undefined); setCreation(undefined);
      if (accessFailure(failure, access)) { onAccessFailure?.(failure); return; }
    }
    setError(visibleControls(errorMessage(failure)));
  }
  async function refresh(more = false) {
    const job = begin('Loading shared runtime evidence…'); if (!job) return;
    try {
      const query = new URLSearchParams({ limit: '20' }); if (more && page?.nextBefore) query.set('before', page.nextBefore);
      const value = await request<RuntimePage>(`/runtime-evidence?${query}`, access, job.signal);
      if (!active(job)) return; validateRuntimePage(value, access); setPage(value);
    } catch (failure) { fail(job, failure); } finally { finish(job); }
  }
  async function inspect(selectedID: string) {
    if (!isHex(selectedID, 32)) { setError('Use the complete 32-character runtime evidence ID.'); return; }
    const job = begin('Inspecting retained runtime evidence…'); if (!job) return; setEvidence(undefined);
    try {
      const value = await request<Evidence>(runtimePath(selectedID), access, job.signal);
      if (!active(job)) return; validateRuntime(value, access, selectedID); setEvidence(value); setID(value.id);
      setNotice('Runtime request inspected. Retained signals, historical criteria and execution observations describe separate facts.');
    } catch (failure) { fail(job, failure); } finally { finish(job); }
  }
  function parse(text: string) {
    if (new TextEncoder().encode(text).length > 65536) throw new Error('Runtime request JSON exceeds 64 KiB.');
    return runtimeInput(parseStrictJSON(text), Date.now());
  }
  function previewDraft() {
    try { setPreview(parse(draft)); setCreation(undefined); setError(''); }
    catch (failure) { setPreview(undefined); setError(errorMessage(failure)); }
  }
  async function importFile(file?: File) {
    if (!file || busy || locked) return; const job = begin('Reading selected runtime request JSON…'); if (!job) return; setPreview(undefined);
    try {
      if (file.size > 65536) throw new Error('Runtime request file exceeds 64 KiB.');
      const bytes = await file.arrayBuffer(); if (!active(job)) return;
      const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes), value = parse(text);
      setDraft(text); setPreview(value); setCreation(undefined); setNotice('Request imported for preview. No telemetry collection has been requested.');
    } catch (failure) { fail(job, failure); } finally { finish(job); }
  }
  async function create() {
    if (!access.canAuthor || !preview || creation?.outcome === 'recorded') return;
    const captured: Creation = creation?.outcome === 'uncertain' ? creation : { input: preview, key: crypto.randomUUID(), outcome: 'pending' };
    const job = begin('Recording the exact runtime evidence request…'); if (!job) return;
    setCreation({ ...captured, outcome: 'pending' }); setEvidence(undefined);
    try {
      // Retry uses the same inspected window and key. It does not refresh a
      // deployment, approved policy, or end time inside the user's decision.
      const value = await request<Evidence>('/runtime-evidence', access, job.signal, captured.input, { idempotencyKey: captured.key });
      if (!active(job)) return; validateRuntime(value, access);
      if (value.requesterId !== access.principalID || !sameJSON(value.input, captured.input)) throw new Error('The returned runtime request differs from the preview. Its outcome is unknown.');
      setCreation({ ...captured, outcome: 'recorded' }); setEvidence(value); setID(value.id); setPage(undefined);
      setNotice('Runtime request recorded. Collection is queued; no passing evidence or production outcome is established.');
    } catch (failure) {
      fail(job, failure); if (active(job)) setCreation(uncertainMutation(failure) ? { ...captured, outcome: 'uncertain' } : undefined);
    } finally { finish(job); }
  }

  return <section className="runtime-evidence panel" aria-labelledby="runtime-title" aria-busy={busy}>
    <div className="section-heading"><div><p className="eyebrow">SHARED DEPLOYMENT OBSERVATIONS</p><h2 id="runtime-title">Runtime evidence</h2></div>
      <button className="secondary" disabled={busy} onClick={() => void refresh()}>Refresh runtime evidence</button></div>
    <p>Inspect Groundcover metrics, logs and traces for an exact deployment, commit and time window. Approved criteria describe specific comparisons; retained telemetry does not establish overall production success.</p>
    <div role="status" aria-live="polite">{pending || notice}</div>{error && <p role="alert" className="error">{error}</p>}
    {!page && <p className="muted">Refresh to discover shared runtime requests in this repository.</p>}
    {page && <><ul className="record-list shared-list" aria-label="Shared runtime requests">{page.evidence.map(r => <li key={r.id}><button className="shared-card" disabled={busy} onClick={() => void inspect(r.id)} aria-label={`Inspect runtime evidence ${r.id}`}>
      <strong>{r.environment}</strong><span>{r.receiptDigest ? 'Retained receipt available' : 'Collection outcome unknown'} · {dateLabel(r.createdAt)}</span><code>{r.commit}</code></button></li>)}</ul>
      {page.evidence.length === 0 && <p>No runtime requests are available in this scope.</p>}
      {page.nextBefore && <><p className="warning">More requests exist. This page is bounded to 20.</p><button className="secondary" disabled={busy} onClick={() => void refresh(true)}>Load more runtime requests</button></>}</>}
    <form className="control-row" onSubmit={e => { e.preventDefault(); void inspect(id); }}><label htmlFor="runtime-id">Runtime evidence ID<input id="runtime-id" value={id} maxLength={32} disabled={busy} onChange={e => setID(e.target.value)} /></label>
      <button className="secondary" disabled={busy || !isHex(id, 32)}>Inspect shared runtime evidence</button></form>
    {access.canAuthor && <details open={creation?.outcome === 'uncertain' || undefined}><summary>Request evidence for an inspected deployment</summary>
      <p>Import or paste deliveryId, deliveryDigest, observationSequence, deploymentId, commit, environment, start, end and requirements. Use whole-second UTC timestamps ending in Z. Each criterion reference needs changeId, revision, digest and criterionId; an empty requirements array requests context without a criterion conclusion.</p>
      <label htmlFor="runtime-file">Runtime request JSON file<input id="runtime-file" type="file" accept=".json,application/json" disabled={busy || locked} onChange={e => { void importFile(e.target.files?.[0]); e.target.value = ''; }} /></label>
      <label htmlFor="runtime-json">Runtime request JSON<textarea id="runtime-json" rows={8} value={draft} maxLength={65536} disabled={busy || locked} onChange={e => { setDraft(e.target.value); setPreview(undefined); setCreation(undefined); }} /></label>
      <button className="secondary" disabled={busy || locked || !draft} onClick={previewDraft}>Preview runtime request</button>
      {preview && <section aria-label="Runtime request preview"><h3>Exact deployment and window preview</h3><RuntimePins input={preview} />
        <button disabled={busy || creation?.outcome === 'recorded'} onClick={() => void create()}>{creation?.outcome === 'uncertain' ? 'Retry exact runtime request' : 'Record runtime request'}</button></section>}
      {creation?.outcome === 'uncertain' && <p className="warning">Request outcome unknown. The exact input and key are retained for explicit retry.</p>}
      {creation?.outcome === 'recorded' && <button className="secondary" disabled={busy} onClick={() => { setCreation(undefined); setPreview(undefined); setDraft(''); }}>Start another runtime request</button>}
    </details>}
    {evidence && <article aria-label="Inspected runtime evidence"><div className="section-heading"><h3>{evidence.input.environment} · {evidence.target.service}</h3><button className="secondary" disabled={busy} onClick={() => void inspect(evidence.id)}>Refresh inspected runtime evidence</button></div>
      <dl><dt>Request ID</dt><dd><code>{evidence.id}</code></dd><dt>Request digest</dt><dd><code>{evidence.digest}</code></dd><dt>Requested by</dt><dd>{evidence.requesterId} · {dateLabel(evidence.createdAt)}</dd></dl>
      <RuntimePins input={evidence.input} />
      <p className="warning">{runtimeFreshness(evidence, now) === 'not_collected' ? 'No retained telemetry receipt. Criteria remain unverified.' : runtimeFreshness(evidence, now) === 'stale' ? 'Stale runtime window. Historical results are not current health evidence.' : 'Window within its configured display age. This is not proof of current application health.'}</p>
      <section aria-label="Runtime execution observation"><h4>Collection execution</h4><p>{executionLabel(evidence.execution, now)}</p><p>A request or retained receipt does not itself prove the Temporal execution has completed. Refresh explicitly for another observation.</p></section>
      <details><summary>Bound integration and provider deployment</summary><pre>{sourceJSON({ target: evidence.target, deployment: evidence.deployment })}</pre></details>
      <section aria-label="Approved runtime policy"><h4>Exact approved criteria ({evidence.criteria.length})</h4>{evidence.criteria.length === 0 && <p>No approved criteria linked. Signals provide context only.</p>}
        {evidence.criteria.map(l => <details key={`${l.requirement.changeId}:${l.requirement.criterionId}`}><summary>{l.requirement.changeId} · {l.requirement.criterionId}</summary><pre>{sourceJSON(l)}</pre></details>)}</section>
      {evidence.receipt && <section aria-label="Retained runtime receipt"><h4>Historical observations</h4><p>Receipt <code>{evidence.receipt.digest}</code> · collected {dateLabel(evidence.receipt.collectedAt)}</p>
        <p className="warning">Broader production outcome: not verified. These results cover only the inspected window and selected criteria.</p>
        {evidence.receipt.evaluations.map(e => <section className="runtime-evaluation" key={`${e.requirement.changeId}:${e.requirement.criterionId}`} aria-label={`Criterion ${e.requirement.criterionId}`}><h5>{e.requirement.criterionId} · {e.state === 'met' ? 'Met in the inspected window' : e.state === 'not_met' ? 'Not met in the inspected window' : 'Not verified'}</h5><p>{visibleControls(e.reason)}{e.value !== undefined && ` · Observed value ${e.value}`}</p><p>Approved criterion digest <code>{e.criterionDigest}</code></p><pre>{sourceJSON(e.requirement)}</pre></section>)}
        {evidence.receipt.signals.map(s => <Signal key={`${s.kind}:${s.metric ?? ''}`} signal={s} />)}
      </section>}
    </article>}
  </section>;
}

function RuntimePins({ input }: { input: RuntimeInput }) {
  return <><dl><dt>Delivery</dt><dd><code>{input.deliveryId}</code></dd><dt>Delivery digest</dt><dd><code>{input.deliveryDigest}</code></dd><dt>Provider observation / deployment</dt><dd>{input.observationSequence} / {visibleControls(input.deploymentId)}</dd><dt>Exact commit</dt><dd><code>{input.commit}</code></dd><dt>Environment</dt><dd>{input.environment}</dd><dt>UTC window</dt><dd>{input.start} → {input.end}</dd></dl><details><summary>Requested criterion references ({input.requirements.length})</summary><pre>{sourceJSON(input.requirements)}</pre></details></>;
}

function Signal({ signal: s }: { signal: RuntimeSignal }) {
  return <section className="runtime-signal" aria-label={`Runtime signal ${s.metric ?? s.kind}`}><h5>{s.metric ?? s.kind} · {s.state}</h5><p>Correlation: {s.correlation} · Coverage: {s.coverage}</p>
    <p>{dateLabel(s.collectedAt)} · <code>{s.endpoint}</code></p><dl><dt>Query digest</dt><dd><code>{s.queryDigest}</code></dd><dt>Response digest</dt><dd><code>{s.responseDigest ?? 'Unavailable'}</code></dd></dl>
    {s.series.map((series, i) => <details key={i}><summary>Metric series {i + 1} · {series.points.length} retained points</summary><pre>{sourceJSON(series.labels)}</pre><table><thead><tr><th>Query time (UTC)</th><th>Value</th></tr></thead><tbody>{series.points.map((point, j) => <tr key={j}><td>{point.at}</td><td>{point.value}</td></tr>)}</tbody></table></details>)}
    {s.records.length > 0 && <details><summary>Inspect all {s.records.length} retained {s.kind} records</summary>{s.records.map((record, i) => <section key={i}><p>{dateLabel(record.at)}</p><pre>{sourceJSON(record.data)}</pre></section>)}</details>}
    {!s.series.length && !s.records.length && <p className="warning">No retained source rows. Missing or unavailable data cannot establish a passing criterion.</p>}
  </section>;
}
