import { useEffect, useRef, useState } from 'react';
import { APIError, accessFailure, dateLabel, errorMessage } from './api';
import type { BrowserAccess, Revision } from './api';
import { sameJSON, uncertainMutation } from './collections';
import { PackageContent, packageFields } from './PackageContent';
import { visibleControls } from './sourceText';
import { useWorkflowActivity } from './workflowActivity';
import { applyAssistance, createAssistance, getAssistance, listAssistance, suggestedContent, textBytes, validateAssistance, validateAssistancePage } from './assistance';
import type { ApplySuggestionInput, AssistanceInput, AssistancePage, DesignAssistance as Assistance, DesignSection } from './assistance';

type Command = ({ kind: 'request'; input: AssistanceInput; base: Revision } | { kind: 'apply'; input: ApplySuggestionInput; record: Assistance }) & {
  key: string; stage: 'preview' | 'sending' | 'uncertain';
};
interface Job { sequence: number; signal: AbortSignal }

export function DesignAssistance({ access, visible, disabled, changeId, target, onBusyChange, onCommandLock, onInspectionRequired, onAccessFailure }: {
  access: BrowserAccess; visible: boolean; disabled: boolean; changeId?: string; target?: Revision;
  onBusyChange: (active: boolean) => boolean;
  onCommandLock: (locked: boolean) => void;
  onInspectionRequired: (message: string) => void;
  onAccessFailure?: (failure: APIError) => void;
}) {
  const [page, setPage] = useState<AssistancePage>();
  const [record, setRecord] = useState<Assistance>();
  const [inspectionID, setInspectionID] = useState('');
  const [instruction, setInstruction] = useState('');
  const [sections, setSections] = useState<DesignSection[]>(['design']);
  const [selected, setSelected] = useState<DesignSection[]>([]);
  const [editing, setEditing] = useState(false);
  const [command, setCommand] = useState<Command>();
  const [pending, setPending] = useState('');
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');
  const sequence = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const working = useRef(false);
  const inFlight = useRef<Command | undefined>(undefined);
  const busy = disabled || pending !== '';
  const humanAuthor = access.principalKind === 'human' && access.canAuthor;
  const active = useWorkflowActivity(visible, () => {
    sequence.current++; controller.current?.abort(); working.current = false; setPending(''); onBusyChange(false);
    const sent = inFlight.current; inFlight.current = undefined;
    if (sent) {
      setCommand({ ...sent, stage: 'uncertain' }); onCommandLock(true);
      if (sent.kind === 'apply') onInspectionRequired('A suggestion application was interrupted. Recover the retained command, then inspect the latest Change.');
    } else setCommand(previous => previous?.stage === 'preview' ? undefined : previous);
  });
  useEffect(() => () => { sequence.current++; controller.current?.abort(); onBusyChange(false); }, []);
  useEffect(() => {
    // A fresh package selection invalidates unsubmitted confirmations; an
    // uncertain command retains its original pins even while another is read.
    setCommand(previous => previous?.stage === 'preview' ? undefined : previous);
    setEditing(false);
  }, [target?.changeId, target?.number, target?.digest]);
  useEffect(() => { setPage(undefined); }, [changeId]);

  function begin(label: string): Job | undefined {
    if (!active.current || disabled || working.current || !onBusyChange(true)) return;
    working.current = true; controller.current?.abort(); controller.current = new AbortController();
    setPending(label); setError(''); setNotice('');
    setCommand(previous => previous?.stage === 'preview' ? undefined : previous);
    return { sequence: ++sequence.current, signal: controller.current.signal };
  }
  function current(job: Job) { return active.current && sequence.current === job.sequence && !job.signal.aborted; }
  function finish(job: Job) {
    if (current(job)) { working.current = false; inFlight.current = undefined; setPending(''); onBusyChange(false); }
  }
  function failed(job: Job, failure: unknown) {
    if (!current(job)) return;
    if (accessFailure(failure, access)) {
      sequence.current++; controller.current?.abort(); working.current = false; inFlight.current = undefined;
      setPending(''); setPage(undefined); setRecord(undefined); setInspectionID(''); setCommand(undefined); setInstruction(''); setSections([]); setSelected([]); setEditing(false); setNotice(''); setError('');
      onBusyChange(false); onCommandLock(false); onAccessFailure?.(failure);
    } else setError(errorMessage(failure));
  }
  function inspected(value: Assistance) {
    setRecord(value); setInspectionID(value.id); setSelected(value.suggestion ? packageFields.map(([key]) => key).filter(key => Object.hasOwn(value.suggestion!.sections, key)) : []);
  }
  async function list(more = false) {
    if (more && !page?.nextBefore) return;
    const job = begin('Reading assistance requests…'); if (!job) return;
    if (!more) setPage(undefined);
    try {
      const value = await listAssistance(changeId, more ? page?.nextBefore : undefined, access, job.signal);
      if (!current(job)) return;
      validateAssistancePage(value, changeId); setPage(value);
      setNotice('Request list inspected. Open a request to read its saved Design and suggestion.');
    } catch (failure) { failed(job, failure); } finally { finish(job); }
  }
  async function inspect(id: string) {
    const job = begin('Checking the saved request for a suggestion…'); if (!job) return;
    setRecord(undefined); setInspectionID(id); setSelected([]);
    try {
      const value = await getAssistance(id, access, job.signal);
      if (!current(job)) return;
      validateAssistance(value, access, id); inspected(value);
      setNotice(value.suggestion ? 'Suggestion inspected. Review each proposed section before applying.' : 'No suggestion is recorded yet. Ask your native assistant to handle this request.');
    } catch (failure) { failed(job, failure); } finally { finish(job); }
  }
  function previewRequest() {
    if (!active.current || busy || !humanAuthor || !target || command?.stage === 'uncertain') return;
    if (!instruction.trim() || textBytes(instruction) > 4096 || sections.length === 0) {
      setError('Describe the help wanted in 1–4096 UTF-8 bytes and select at least one editable section.'); return;
    }
    const input: AssistanceInput = { changeId: target.changeId, expectedRevision: target.number, expectedDigest: target.digest, instruction, sections: [...sections] };
    setError(''); setCommand({ kind: 'request', input, base: target, key: crypto.randomUUID(), stage: 'preview' });
  }
  const matchingBase = !!record && !!target && record.input.changeId === target.changeId && record.base.number === target.number && record.base.digest === target.digest;
  const canApply = humanAuthor && !!record?.suggestion && !record.application && record.requesterId === access.principalID && matchingBase && !command;
  function previewApplication() {
    if (!active.current || busy || !canApply || !record?.suggestion || !target || selected.length === 0) return;
    const content = suggestedContent(record, selected);
    if (sameJSON(content, record.base.content)) { setError('The selected suggestions do not change the saved Design.'); return; }
    if (textBytes(JSON.stringify({ expectedRevision: target.number, content })) + 1 > 1024 * 1024) { setError('The resulting Design exceeds the 1 MiB command limit. Select fewer sections.'); return; }
    setError(''); setCommand({ kind: 'apply', record, key: crypto.randomUUID(), stage: 'preview', input: {
      requestDigest: record.digest, suggestionDigest: record.suggestion.digest, expectedRevision: target.number, expectedDigest: target.digest, sections: [...selected],
    } });
  }
  async function confirm() {
    const captured = command;
    if (!captured || !humanAuthor || (captured.stage !== 'preview' && captured.stage !== 'uncertain')) return;
    const job = begin(captured.kind === 'request' ? 'Saving assistance request…' : 'Applying selected suggestions…'); if (!job) return;
    inFlight.current = captured; setCommand({ ...captured, stage: 'sending' }); onCommandLock(true);
    try {
      // Confirmation is a single exact POST. Recovery reuses both the key and
      // input; it never fetches or rebases onto a newer Design.
      const value = captured.kind === 'request'
        ? await createAssistance(captured.input, captured.key, access, job.signal)
        : await applyAssistance(captured.record.id, captured.input, captured.key, access, job.signal);
      if (!current(job)) return;
      validateAssistance(value, access, captured.kind === 'apply' ? captured.record.id : undefined);
      if (captured.kind === 'request') {
        // Revision/content pins are exact. Submission may occur between the
        // earlier inspection and request capture without changing those pins.
        if (value.requesterId !== access.principalID || !sameJSON(value.input, captured.input) || !sameJSON(value.base.content, captured.base.content) || value.base.author !== captured.base.author) throw new Error('The saved request does not match the confirmed Design and instruction. Recover the same request.');
        setNotice('Assistance request saved. Its recorded facts are shown below. This command does not revise the Change.');
      } else {
        if (value.digest !== captured.record.digest || value.suggestion?.digest !== captured.input.suggestionDigest || !value.application
          || value.application.appliedBy !== access.principalID || !sameJSON([...value.application.sections].sort(), [...captured.input.sections].sort())
          || !sameJSON(value.base, captured.record.base)) throw new Error('The application response does not match the selected suggestion. Recover the same command.');
        setNotice(`Selected suggestions produced draft revision ${value.application.revision}. Inspect the latest Change to continue.`);
        onInspectionRequired(`Suggestions produced revision ${value.application.revision}. The displayed Design is the earlier inspection; inspect the latest Change before another decision.`);
      }
      inspected(value); setCommand(undefined); onCommandLock(false); setEditing(false);
    } catch (failure) {
      failed(job, failure);
      if (current(job)) {
        if (uncertainMutation(failure)) {
          setCommand({ ...captured, stage: 'uncertain' }); onCommandLock(true);
          if (captured.kind === 'apply') onInspectionRequired('The suggestion application outcome is uncertain. Recover the exact retained command, then inspect the latest Change.');
        } else {
          setCommand(undefined); onCommandLock(false);
          if (failure instanceof APIError && failure.status === 409) onInspectionRequired('The assistance command conflicted with saved work. Inspect the latest Change and request again if its Design changed.');
        }
      }
    } finally { finish(job); }
  }
  function toggle(list: DesignSection[], key: DesignSection) { return list.includes(key) ? list.filter(value => value !== key) : [...list, key]; }
  const reviewed = command?.kind === 'apply' ? command.record : record;

  return <section className="design-assistance panel" aria-labelledby="assistance-title" aria-busy={pending !== ''}>
    <div className="section-heading"><div><p className="eyebrow">NATIVE ASSISTANT HELP</p><h2 id="assistance-title">Improve a Design with an assistant</h2></div>
      <button className="secondary" disabled={busy} onClick={() => void list()}>Browse assistance requests</button></div>
    <p>Ask Codex, Claude Code, or Antigravity to suggest selected sections. Review the suggestion here and choose what to save.</p>
    <p className="muted">Your native assistant handles its own sign-in and usage. Saving a request does not start an assistant. Suggestions are unverified prose; they grant no approval.</p>
    {humanAuthor ? <button className={record || editing ? 'secondary' : undefined} disabled={busy || !target || !!command} onClick={() => { setEditing(true); setRecord(undefined); setInspectionID(''); setSelected([]); setError(''); setSections(packageFields.map(([key]) => key).filter(key => (!Object.hasOwn(target!.content, key) || typeof target!.content[key] === 'string') && key === 'design')); }}>Ask for design help</button>
      : <p className="muted">Human author permission is required to request help or apply your own request. You can inspect shared suggestions.</p>}
    {humanAuthor && !target && <p className="muted">Inspect a saved Change’s latest revision to request help. Historical or stale inspection cannot start a request.</p>}
    {editing && target && !command && <div className="assistance-form">
      <h3>What would you like help with?</h3>
      <p>Change <code>{target.changeId}</code> · revision {target.number}<br/><code>{target.digest}</code></p>
      <fieldset disabled={busy}><legend>Sections the assistant may suggest</legend>{packageFields.map(([key, label]) => {
        const editable = !Object.hasOwn(target.content, key) || typeof target.content[key] === 'string';
        return <label className="section-choice" key={key}><input type="checkbox" checked={sections.includes(key)} disabled={!editable} onChange={() => setSections(toggle(sections, key))}/>{label}{!editable && <span className="muted"> — structured content is preserved</span>}</label>;
      })}</fieldset>
      <label htmlFor="assistance-instruction">Help wanted<textarea id="assistance-instruction" aria-label="Help wanted" value={instruction} maxLength={4096} rows={4} disabled={busy} placeholder="Clarify the design tradeoffs and suggest a practical verification plan." onChange={event => setInstruction(event.target.value)}/></label>
      <p className="muted">{textBytes(instruction)} / 4096 UTF-8 bytes. The assistant receives the complete saved Design as the basis.</p>
      <button disabled={busy} onClick={previewRequest}>Preview assistance request</button>
      <button className="secondary" disabled={busy} onClick={() => setEditing(false)}>Cancel unsent request</button>
    </div>}
    {command && <div className={command.stage === 'uncertain' ? 'assistance-confirmation warning' : 'assistance-confirmation'} aria-label="Assistance confirmation">
      <h3>{command.stage === 'uncertain' ? 'Retained assistance command' : command.kind === 'request' ? 'Review assistance request' : 'Review selected application'}</h3>
      <p>Recovery key <code>{command.key}</code></p>
      {command.kind === 'request' ? <><p>Change <code>{command.input.changeId}</code> · revision {command.input.expectedRevision}<br/><code>{command.input.expectedDigest}</code></p>
        <p>Requested sections: {command.input.sections.join(', ')}</p><p className="package-prose">{visibleControls(command.input.instruction)}</p>
        <h4>Complete saved Design shared with the assistant</h4><PackageContent content={command.base.content}/>
      </> : <><p>Request <code>{command.record.id}</code><br/>Request digest <code>{command.input.requestDigest}</code><br/>Suggestion digest <code>{command.input.suggestionDigest}</code></p>
        <p>Apply to revision {command.input.expectedRevision}<br/><code>{command.input.expectedDigest}</code></p>
        <p>Selected sections: {command.input.sections.join(', ')}</p><h4>Complete resulting Design</h4><PackageContent content={suggestedContent(command.record, command.input.sections)}/>
        <p>This creates a draft attributed to you. An independent person must review it; earlier approval does not carry forward.</p></>}
      {command.stage === 'uncertain' && <p>The result was not confirmed. The exact input and key are retained. No automatic retry has been sent. Retry the same command to recover its saved result.</p>}
      <button disabled={busy} onClick={() => void confirm()}>{command.stage === 'uncertain' ? 'Retry same assistance command' : command.kind === 'request' ? 'Save assistance request' : 'Apply selected suggestions'}</button>
      {command.stage === 'preview' && <button className="secondary" disabled={busy} onClick={() => setCommand(undefined)}>Cancel assistance confirmation</button>}
    </div>}
    <div role="status" aria-live="polite">{pending || notice}</div>
    {error && <p role="alert" className="error">{visibleControls(error)}</p>}
    {inspectionID && !record && command?.kind !== 'apply' && <button className="secondary" disabled={busy} onClick={() => void inspect(inspectionID)}>Retry request inspection</button>}
    {page && <div className="assistance-list"><h3>{changeId ? 'Requests for this Change' : 'Requests in this repository'}</h3>
      {page.requests.length === 0 && <p>No assistance requests are recorded in this page.</p>}
      <ul className="record-list">{page.requests.map(item => <li key={item.id}><button className="secondary" disabled={busy} onClick={() => void inspect(item.id)} aria-label={`Inspect assistance request ${item.id}`}>{visibleControls(item.input.instruction)}</button>
        <p>Revision {item.input.expectedRevision} · {item.appliedRevision ? `Produced revision ${item.appliedRevision}` : item.hasSuggestion ? 'Suggestion recorded' : 'Awaiting a suggestion'}<br/><code>{item.id}</code></p></li>)}</ul>
      {page.nextBefore && <><p className="warning">More requests exist. Only this bounded page is displayed.</p><button className="secondary" disabled={busy} onClick={() => void list(true)}>Next assistance page</button></>}
    </div>}
    {reviewed && <article className="assistance-record" aria-label="Inspected assistance request">
      <div className="section-heading"><h3>{reviewed.application ? 'Suggestions applied' : reviewed.suggestion ? 'Review the suggestion' : 'Continue in your native assistant'}</h3>
        <button className="secondary" disabled={busy} onClick={() => void inspect(reviewed.id)}>Check for suggestion</button></div>
      <dl><dt>Request</dt><dd><code>{reviewed.id}</code></dd><dt>Request digest</dt><dd><code>{reviewed.digest}</code></dd><dt>Requested by</dt><dd>{visibleControls(reviewed.requesterId)}</dd><dt>Requested</dt><dd>{dateLabel(reviewed.createdAt)}</dd><dt>Saved basis</dt><dd>Change <code>{reviewed.input.changeId}</code> · revision {reviewed.base.number}<br/><code>{reviewed.base.digest}</code></dd></dl>
      <p className="package-prose">{visibleControls(reviewed.input.instruction)}</p>
      {!reviewed.suggestion && <><p>No suggestion is recorded. A native assistant must be configured with the Conductor assistance MCP connection. Paste this request into that assistant:</p>
        <pre className="native-handoff">{`Use Conductor to help with Design assistance request ${reviewed.id} in workspace ${access.workspaceID}, repository ${access.repositoryID}. Read it with conductor_get_design_assistance. Review the saved Design and instructions, then submit only the requested section suggestions with conductor_propose_design_sections. Keep my Change unchanged; I will review and apply suggestions in Conductor.`}</pre></>}
      {reviewed.suggestion && <><p>Suggested by <strong>{visibleControls(reviewed.suggestion.agentId)}</strong> · {dateLabel(reviewed.suggestion.createdAt)}<br/><code>{reviewed.suggestion.digest}</code></p>
        {reviewed.suggestion.note && <div><h4>Assistant note — unverified</h4><p className="package-prose">{visibleControls(reviewed.suggestion.note)}</p></div>}
        {packageFields.filter(([key]) => Object.hasOwn(reviewed.suggestion!.sections, key)).map(([key, label]) => <section className="suggestion-section" key={key}>
          <h4>{label}</h4><div className="suggestion-comparison"><div><h5>Saved before</h5><p className="package-prose">{Object.hasOwn(reviewed.base.content, key) ? visibleControls(String(reviewed.base.content[key]) || '(Empty text)') : '(Section absent)'}</p></div>
            <div><h5>Proposed</h5><p className="package-prose">{visibleControls(reviewed.suggestion!.sections[key] || '(Empty text)')}</p></div></div>
          {!reviewed.application && <label className="section-choice"><input type="checkbox" checked={selected.includes(key)} disabled={busy || !canApply} onChange={() => setSelected(toggle(selected, key))}/>Apply {label}</label>}
        </section>)}
        {!reviewed.application && <>{reviewed.requesterId !== access.principalID && <p className="muted">Only the human who requested this assistance may apply it.</p>}
          {!matchingBase && <p className="warning">The current inspection does not match the request’s saved basis. Inspect that Change’s latest revision. If it changed, create a new assistance request; suggestions are never automatically rebased.</p>}
          <button disabled={busy || !canApply || selected.length === 0} onClick={previewApplication}>Preview selected suggestions</button></>}
      </>}
      {reviewed.application && <div className="warning"><p>These selected sections produced draft revision {reviewed.application.revision}: {reviewed.application.sections.join(', ')}.<br/><code>{reviewed.application.revisionDigest}</code></p>
        <p>Applied by {visibleControls(reviewed.application.appliedBy)}. This is a historical production fact, not the current revision or approval. Inspect the latest Change to continue.</p></div>}
    </article>}
  </section>;
}
