import type { InspectedExecutionArtifact } from './delivery';
import { visibleControls } from './sourceText';

// Review has no lifecycle authority: failed attempts and reports with no patch
// remain readable. Publication eligibility is a separate caller-owned decision.
export function ExecutionArtifact({ value }: { value: InspectedExecutionArtifact }) {
  const a = value.artifact;
  return <><dl><dt>Input digest</dt><dd><code>{a.inputDigest}</code></dd><dt>Profile digest</dt><dd><code>{a.profileDigest}</code></dd><dt>Worker image</dt><dd><code>{a.image}</code></dd><dt>Adapter</dt><dd>{a.adapter} · {a.adapterVersion}</dd><dt>Cleanup</dt><dd>{a.cleanupConfirmed ? 'Confirmed' : 'Unknown; publication blocked'}</dd></dl>
    {value.decodedPatches.length === 0 && <p>No repository patches retained. Inspect the task report and check output below.</p>}
    {value.decodedPatches.map(p => <section key={p.metadata.repositoryId} aria-label={`Patch for ${p.metadata.repositoryId}`}><h4>Patch · {p.metadata.repositoryId}</h4><p>Paths: {p.metadata.paths.map(visibleControls).join(', ')}</p><p>Digest <code>{p.metadata.digest}</code> · {p.bytes.length} bytes</p><pre className="patch-output">{visibleControls(p.text)}</pre></section>)}
    <h4>Producer and independent checks</h4>
    {[a.producer, ...a.checks].map((c, i) => <section key={i} aria-label={i === 0 ? 'Producer evidence' : `Check ${c.id}`}><h5>{i === 0 ? 'Producer' : c.id} · {c.state}</h5><p>{c.repositoryId} · Exit {c.exitCode ?? 'unknown'} · {c.truncated ? 'Output truncated; cannot pass' : 'Complete retained output'}</p><pre>{visibleControls(JSON.stringify(c.argv ?? []))}</pre><p>Output digest <code>{c.outputDigest}</code><br />Source digest <code>{c.sourceDigest}</code></p><pre>{visibleControls(c.output ?? '(No output)')}</pre></section>)}
    {a.checks.length === 0 && <p className="warning">No independent check results retained. Verification is unknown.</p>}
  </>;
}
