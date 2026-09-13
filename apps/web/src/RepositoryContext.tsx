import { useId } from 'react';
import type { Content } from './api';
import { dateLabel } from './api';
import { remoteSnapshot, sameJSON } from './collections';
import type { CollectionReceipt } from './collections';

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function display(value: unknown): string {
  if (value === undefined || value === null) return 'Not recorded';
  return typeof value === 'string' ? value : JSON.stringify(value);
}

function sourceText(value: string): string {
  // Expose terminal controls as text while retaining normal source whitespace.
  return value.replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g,
    character => `\\u${character.charCodeAt(0).toString(16).padStart(4, '0')}`);
}

const states = ['collected', 'missing', 'unavailable', 'truncated'];

export function RepositoryContext({ content, linkedReceipt, onInspectCollection, disabled = false }: {
  content: Content;
  linkedReceipt?: CollectionReceipt;
  onInspectCollection?: (id: string) => void;
  disabled?: boolean;
}) {
  const titleID = useId();
  const context = content.repositoryContext;
  if (context === undefined) {
    return <section className="panel" aria-labelledby={titleID}>
      <h3 id={titleID}>Repository context</h3>
      <p className="muted">No repository context is attached to this revision. Evidence coverage is unknown.</p>
    </section>;
  }
  if (!object(context) || (context.schemaVersion !== 1 && !remoteSnapshot(context))
      || typeof context.repository !== 'string' || !context.repository
      || typeof context.commit !== 'string' || !context.commit || !Array.isArray(context.artifacts)
      || !context.artifacts.every(artifact => object(artifact) && typeof artifact.path === 'string'
        && typeof artifact.state === 'string' && states.includes(artifact.state))) {
    return <section className="panel" aria-labelledby={titleID}>
      <h3 id={titleID}>Repository context unavailable</h3>
      <p className="warning">This context format is unsupported or incomplete. Its original fields remain visible in the package content.</p>
    </section>;
  }
  // Collection records describe captured source, not independently verified provenance or test results.
  const artifacts = context.artifacts as Record<string, unknown>[];
  const remote = remoteSnapshot(context) ? context : undefined;
  const linked = remote && linkedReceipt?.id === remote.collectionId && sameJSON(remote, linkedReceipt.snapshot);
  return <section className="panel" aria-labelledby={titleID}>
    <div className="section-heading"><h3 id={titleID}>Pinned repository context</h3><span className="tag">Source evidence</span></div>
    <dl>
      <dt>Repository</dt><dd>{display(context.repository)}</dd>
      <dt>Commit</dt><dd><code>{display(context.commit)}</code></dd>
      <dt>Requested ref</dt><dd>{display(context.requestedRef)}</dd>
      <dt>Collected</dt><dd>{typeof context.collectedAt === 'string' ? dateLabel(context.collectedAt) : 'Not recorded'}</dd>
      <dt>Collector</dt><dd>{display(context.collector)}</dd>
    </dl>
    <p className="muted">Collected means source was captured. It does not mean the content or implementation was verified.</p>
    {remote ? <>
      <dl><dt>Collection</dt><dd><code>{remote.collectionId}</code></dd><dt>Provider</dt><dd>{remote.source.provider} · {remote.source.host} · {remote.source.providerId}</dd>
        <dt>Source scope</dt><dd>{remote.source.workspaceId} / {remote.source.repositoryId}</dd><dt>Read profile</dt><dd>{remote.source.profile} · integration version {remote.source.integrationVersion}</dd></dl>
      {linked ? <p className="muted">The complete snapshot matches the server-stored receipt inspected in this repository. This linkage establishes collected source, not passing verification.</p>
        : <p className="warning">Remote receipt reference has not been checked against a server-stored collection in this inspection. JSON metadata alone does not establish trusted linkage.</p>}
      {onInspectCollection && <button className="secondary" disabled={disabled} onClick={() => onInspectCollection(remote.collectionId)}>Inspect linked collection</button>}
    </> : <p className="muted">This package's collection metadata is not independent proof of provenance. Version 1 extension fields remain uninterpreted.</p>}
    <ul className="coverage" aria-label="Context coverage">
      {states.map(state => <li key={state}><strong>{artifacts.filter(item => item.state === state).length}</strong> {state}</li>)}
    </ul>
    {artifacts.length === 0 && <p className="warning">No artifact coverage was recorded.</p>}
    <div className="artifact-list">
      {artifacts.map((artifact, index) => <details key={`${display(artifact.path)}-${index}`}>
        <summary><span className="artifact-path">{display(artifact.path)}</span><span className={`tag ${artifact.state === 'collected' ? '' : 'attention'}`}>{display(artifact.state)}</span></summary>
        {artifact.message !== undefined && <p className="warning">{display(artifact.message)}</p>}
        <dl><dt>Blob</dt><dd><code>{display(artifact.blobOID)}</code></dd><dt>Digest</dt><dd><code>{display(artifact.digest)}</code></dd></dl>
        {typeof artifact.text === 'string'
          ? <><p className="muted">Source text; terminal control characters are shown as escape sequences.</p><pre className="source-text">{sourceText(artifact.text)}</pre></>
          : <p className="muted">No source text is available for this artifact.</p>}
      </details>)}
    </div>
  </section>;
}
