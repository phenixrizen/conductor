import { useEffect, useRef, useState } from 'react';
import { APIError, accessFailure, dateLabel, errorMessage, request } from './api';
import type { BrowserAccess, RepositoryPage } from './api';
import { collectionPath, sameJSON, uncertainMutation, validateCollection, validateCollectionPage } from './collections';
import type { Collection, CollectionPage } from './collections';
import { graphPath, isHex, validateGraph, validateGraphData, validateGraphPage, validateGraphArtifact } from './graphs';
import type { GraphArtifact, GraphPage, GraphQuery, GraphSource, RepositoryGraph } from './graphs';
import { RepositoryContext } from './RepositoryContext';
import { visibleControls } from './sourceText';

type Job = { sequence: number; signal: AbortSignal };
type Creation = { input: { sources: GraphSource[] }; key: string; outcome: 'pending' | 'recorded' | 'uncertain' | 'rejected' };

export function RepositoryGraphs({ access, onAccessFailure, onInspected }: {
  access: BrowserAccess; onAccessFailure?: (failure: APIError) => void; onInspected?: (graph?: RepositoryGraph) => void;
}) {
  const [page, setPage] = useState<GraphPage>();
  const [graph, setGraph] = useState<RepositoryGraph>();
  const [query, setQuery] = useState<GraphQuery>();
  const [artifact, setArtifact] = useState<GraphArtifact>();
  const [artifactRepositoryID, setArtifactRepositoryID] = useState(access.repositoryID);
  const [artifactPath, setArtifactPath] = useState('');
  const [search, setSearch] = useState('');
  const [depth, setDepth] = useState(1);
  const [repositories, setRepositories] = useState<RepositoryPage>();
  const [repositoryID, setRepositoryID] = useState(access.repositoryID);
  const [collections, setCollections] = useState<CollectionPage>();
  const [collection, setCollection] = useState<Collection>();
  const [useFullSource, setUseFullSource] = useState(true);
  const [sources, setSources] = useState<GraphSource[]>([]);
  const [creation, setCreation] = useState<Creation>();
  const [pending, setPending] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const sequence = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);
  const working = useRef(false);
  const busy = pending !== '';
  const locked = creation?.outcome === 'uncertain' || creation?.outcome === 'pending' || creation?.outcome === 'recorded';
  useEffect(() => () => { sequence.current++; controller.current?.abort(); }, []);

  function begin(label: string): Job | undefined {
    if (working.current) return;
    working.current = true; controller.current?.abort(); controller.current = new AbortController();
    setPending(label); setError(''); setNotice('');
    return { sequence: ++sequence.current, signal: controller.current.signal };
  }
  function current(job: Job) { return sequence.current === job.sequence && !job.signal.aborted; }
  function finish(job: Job) { if (current(job)) { working.current = false; setPending(''); } }
  function inspected(value?: RepositoryGraph) { setArtifact(undefined); setArtifactPath(''); setArtifactRepositoryID(access.repositoryID); setGraph(value); setQuery(undefined); onInspected?.(value); }
  function fail(job: Job, failure: unknown) {
    if (!current(job)) return;
    if (failure instanceof APIError && [401, 403, 404].includes(failure.status)) {
      inspected(undefined); setPage(undefined); setRepositories(undefined); setCollections(undefined); setCollection(undefined); setSources([]); setCreation(undefined);
      // Invalidate the operation before its catch/finally handlers can restore a
      // captured source tuple that the server has just made inaccessible.
      sequence.current++; controller.current?.abort(); working.current = false; setPending('');
      if (accessFailure(failure, access)) { onAccessFailure?.(failure); return; }
    }
    setError(errorMessage(failure));
  }
  async function list(more = false) {
    const job = begin('Refreshing shared graphs…'); if (!job) return;
    try {
      const q = new URLSearchParams({ limit: '20' }); if (more && page?.nextBefore) q.set('before', page.nextBefore);
      const value = await request<GraphPage>(`/repository-graphs?${q}`, access, job.signal);
      if (!current(job)) return; validateGraphPage(value); setPage(value);
    } catch (failure) { fail(job, failure); } finally { finish(job); }
  }
  async function inspect(id: string) {
    const job = begin('Inspecting repository graph…'); if (!job) return; inspected(undefined);
    try {
      const value = await request<RepositoryGraph>(graphPath(id), access, job.signal);
      if (!current(job)) return; validateGraph(value, access, id); inspected(value);
      setNotice('Graph inspected. Relationships describe retained source; freshness, coverage and verification are separate facts.');
    } catch (failure) { fail(job, failure); } finally { finish(job); }
  }
  async function queryGraph(nodeID?: string) {
    const selected = graph; if (!selected) return;
    const job = begin('Searching retained relationships…'); if (!job) return; setQuery(undefined);
    try {
      const q = new URLSearchParams({ depth: String(depth), limit: '100' }); q.set(nodeID ? 'nodeId' : 'search', nodeID ?? search);
      const value = await request<GraphQuery>(`${graphPath(selected.id)}/query?${q}`, access, job.signal);
      if (!current(job)) return; validateGraphData(value, access, 100);
      if (value.graphId !== selected.id || value.digest !== selected.digest || !sameJSON(value.sources, selected.snapshot.sources)) throw new Error('The query does not match the inspected graph. Inspect it again.');
      setQuery(value);
    } catch (failure) { fail(job, failure); } finally { finish(job); }
  }
  async function readGraphSource(repositoryID=artifactRepositoryID,path=artifactPath) {
    const selected=graph,source=graph?.snapshot.sources.find(s=>s.repositoryId===repositoryID);
    if(!selected||!source||!path||new TextEncoder().encode(path).length>1024)return;
    const job=begin('Reading exact retained source from the inspected graph…');if(!job)return;setArtifact(undefined);setArtifactRepositoryID(repositoryID);setArtifactPath(path);
    try{
      // The graph anchor and credential stay fixed. A related source is selected
      // only inside the exact inspected graph; the API checks every source grant.
      const q=new URLSearchParams({graphDigest:selected.digest,repositoryId:source.repositoryId,collectionId:source.collectionId,receiptDigest:source.digest,path});if(source.fullSourceDigest)q.set('fullSourceDigest',source.fullSourceDigest);
      const value=await request<GraphArtifact>(`${graphPath(selected.id)}/artifact?${q}`,access,job.signal);if(!current(job))return;
      await validateGraphArtifact(value,selected,source,path);if(!current(job))return;setArtifact(value);setNotice('Retained source inspected. Collection is not verification, and freshness remains unknown.');
    }catch(failure){fail(job,failure);}finally{finish(job);}
  }
  async function discoverRepositories() {
    const job = begin('Loading available graph repositories…'); if (!job) return;
    try {
      const value = await request<RepositoryPage>('/repositories', access, job.signal);
      if (!current(job)) return;
      if (!value || !Array.isArray(value.repositories) || value.repositories.length > 100 || typeof value.truncated !== 'boolean' || value.repositories.some(r => !r || r.workspaceId !== access.workspaceID || r.canRead !== true || typeof r.canAuthor !== 'boolean' || typeof r.id !== 'string' || typeof r.name !== 'string') || new Set(value.repositories.map(r => r.id)).size !== value.repositories.length) throw new Error('Repository discovery is incomplete. Refresh access before adding sources.');
      setRepositories(value);
    } catch (failure) { fail(job, failure); } finally { finish(job); }
  }
  function sourceAccess(): BrowserAccess | undefined {
    const repo = repositories?.repositories.find(r => r.id === repositoryID);
    if (!repo || !repo.canRead) return;
    return { ...access, repositoryID: repo.id, canAuthor: repo.canAuthor, canApprove: repo.canApprove };
  }
  async function listSourceCollections(more = false) {
    const selected = sourceAccess(); if (!selected) return;
    const job = begin('Loading source receipts…'); if (!job) return; setCollection(undefined);
    try {
      const q = new URLSearchParams({ limit: '20' }); if (more && collections?.nextBefore) q.set('before', collections.nextBefore);
      const value = await request<CollectionPage>(`/context-collections?${q}`, selected, job.signal);
      if (!current(job)) return; validateCollectionPage(value, selected); setCollections(value);
    } catch (failure) { fail(job, failure); } finally { finish(job); }
  }
  async function inspectSource(id: string) {
    const selected = sourceAccess(); if (!selected) return;
    const job = begin('Inspecting source receipt…'); if (!job) return; setCollection(undefined);
    try {
      const value = await request<Collection>(collectionPath(id), selected, job.signal);
      if (!current(job)) return; validateCollection(value, selected, id); setCollection(value); setUseFullSource(!!value.fullSource);
    } catch (failure) { fail(job, failure); } finally { finish(job); }
  }
  function addSource() {
    if (!collection?.receipt || busy || locked || sources.length >= 16 && !sources.some(s => s.repositoryId === collection.repositoryId)) return;
    const source: GraphSource = { repositoryId: collection.repositoryId, collectionId: collection.id, digest: collection.receipt.digest,
      ...(useFullSource && collection.fullSource ? { fullSourceDigest: collection.fullSource.digest } : {}) };
    setSources(previous => [...previous.filter(s => s.repositoryId !== source.repositoryId), source]); setCreation(undefined);
  }
  async function create() {
    if (!access.canAuthor || !sources.some(s => s.repositoryId === access.repositoryID) || creation?.outcome === 'recorded') return;
    // Retain exactly the inspected source tuples across an uncertain response.
    // No receipt refresh occurs inside this creation or its explicit retry.
    const captured: Creation = creation?.outcome === 'uncertain' ? creation : { input: { sources: sources.map(s => ({ ...s })) }, key: crypto.randomUUID(), outcome: 'pending' };
    const job = begin('Recording shared graph…'); if (!job) return; setCreation({ ...captured, outcome: 'pending' });
    try {
      const value = await request<RepositoryGraph>('/repository-graphs', access, job.signal, captured.input, { idempotencyKey: captured.key });
      if (!current(job)) return; validateGraph(value, access);
      if (value.creatorId !== access.principalID || value.snapshot.sources.length !== captured.input.sources.length || captured.input.sources.some(s => !value.snapshot.sources.some(v => v.repositoryId === s.repositoryId && v.collectionId === s.collectionId && v.digest === s.digest && v.fullSourceDigest === s.fullSourceDigest))) throw new Error('The saved graph does not match the requested sources. Its creation outcome is unknown.');
      setCreation({ ...captured, outcome: 'recorded' }); setPage(undefined); inspected(value); setNotice('Shared graph recorded. All source repositories must remain readable to inspect it.');
    } catch (failure) { fail(job, failure); if (current(job)) setCreation({ ...captured, outcome: uncertainMutation(failure) ? 'uncertain' : 'rejected' }); }
    finally { finish(job); }
  }

  const names = new Map(query?.nodes.map(n => [n.id, n.name]) ?? []);
  return <section className="repository-graphs panel" aria-labelledby="graphs-title" aria-busy={busy}>
    <div className="section-heading"><div><p className="eyebrow">SHARED ENGINEERING CONTEXT</p><h2 id="graphs-title">Repository relationships</h2></div><button className="secondary" disabled={busy} onClick={() => void list()}>Refresh graphs</button></div>
    <p className="muted">Explore dependencies across repositories using retained source. Everyone reads the same graph, subject to access to every included repository.</p>
    <div role="status" aria-live="polite">{pending || notice}</div>{error && <p role="alert" className="error">{error}</p>}
    {!page && <p className="muted">Refresh to discover graphs available in this repository.</p>}{page?.graphs.length === 0 && <p>No shared graphs are available in this scope.</p>}
    {page && <><ul className="record-list shared-list" aria-label="Shared repository graphs">{page.graphs.map(item => <li key={item.id}><button className="shared-card" disabled={busy} onClick={() => void inspect(item.id)} aria-label={`Inspect graph ${item.id}`}><strong>{item.id}</strong><code>{item.digest}</code><span>{dateLabel(item.createdAt)}</span></button></li>)}</ul>{page.nextBefore && <><p className="warning">More graphs exist. This page is bounded to 20.</p><button className="secondary" disabled={busy} onClick={() => void list(true)}>Load more graphs</button></>}</>}
    {access.canAuthor && <details className="graph-create" open={creation?.outcome === 'uncertain' || undefined}><summary>Build a graph from inspected receipts</summary>
      <p>Choose one retained source per repository, including <strong>{access.repositoryID}</strong>. Each source requires author permission. At most 16 repositories may be included.</p>
      <button className="secondary" disabled={busy || !!locked} onClick={() => void discoverRepositories()}>Load graph repositories</button>
      {repositories && <><label htmlFor="graph-source-repository">Source repository<select id="graph-source-repository" aria-label="Source repository" value={repositoryID} disabled={busy || !!locked} onChange={event => { setRepositoryID(event.target.value); setCollection(undefined); setCollections(undefined); }}><option value="">Choose a source repository</option>{repositories.repositories.map(r => <option key={r.id} value={r.id} disabled={!r.canAuthor}>{r.name} ({r.id}){r.canAuthor ? '' : ' — read only'}</option>)}</select></label><button className="secondary" disabled={busy || !!locked || !sourceAccess()} onClick={() => void listSourceCollections()}>Load source receipts</button>{repositories.truncated && <p className="warning">Repository discovery is truncated. Missing repositories cannot be selected from this response.</p>}</>}
      {collections && <><ul className="record-list source-receipts">{collections.collections.map(c => <li key={c.id}><button className="secondary" disabled={busy || !!locked || !c.receiptDigest} onClick={() => void inspectSource(c.id)}>Inspect source {c.id}</button><code>{c.commit}</code><span>{c.receiptDigest ? 'Retained receipt' : 'Receipt unavailable'}</span></li>)}</ul>{collections.collections.length === 0 && <p>No source collections are available.</p>}{collections.nextBefore && <button className="secondary" disabled={busy || !!locked} onClick={() => void listSourceCollections(true)}>Load more source receipts</button>}</>}
      {collection && <section className="source-inspection"><h3>Inspected graph source</h3><dl><dt>Repository</dt><dd>{collection.repositoryId}</dd><dt>Collection</dt><dd><code>{collection.id}</code></dd><dt>Commit</dt><dd><code>{collection.input.commit}</code></dd><dt>Receipt digest</dt><dd><code>{collection.receipt?.digest ?? 'Unavailable'}</code></dd></dl>
        {collection.receipt && <details><summary>Inspect retained review artifacts</summary><RepositoryContext content={{ repositoryContext: collection.receipt.snapshot }} linkedReceipt={collection.receipt} /></details>}
        {collection.fullSource ? <><label className="checkbox-label"><input type="checkbox" checked={useFullSource} disabled={busy || !!locked} onChange={event => setUseFullSource(event.target.checked)} /> Use the retained whole-repository index</label><p>Bundle digest: <code>{collection.fullSource.digest}</code></p><p>{collection.fullSource.indexedFiles} index artifacts from {collection.fullSource.fileCount} source files. {collection.fullSource.truncated ? 'Index coverage is truncated.' : 'Source artifacts fit the index bounds; inspect graph gaps for unsupported content.'}</p></> : <p className="warning">This receipt contains selected review paths only. Request whole-repository source in Shared context collections for coding work.</p>}
        <button disabled={busy || !!locked || !collection.receipt || sources.length >= 16 && !sources.some(s => s.repositoryId === collection.repositoryId)} onClick={addSource}>Add inspected source</button></section>}
      <ul className="record-list" aria-label="Graph sources to record">{sources.map(s => <li key={s.repositoryId}><strong>{s.repositoryId}</strong> · <code>{s.collectionId}</code><br /><code>{s.digest}</code><p>{s.fullSourceDigest ? <>Whole-source digest: <code>{s.fullSourceDigest}</code></> : 'Selected review paths only'}</p><button className="secondary" disabled={busy || !!locked} onClick={() => setSources(previous => previous.filter(v => v.repositoryId !== s.repositoryId))}>Remove {s.repositoryId}</button></li>)}</ul>
      <button disabled={busy || !sources.some(s => s.repositoryId === access.repositoryID) || creation?.outcome === 'recorded'} onClick={() => void create()}>{creation?.outcome === 'uncertain' ? 'Retry exact graph request' : 'Record shared graph'}</button>
      {creation?.outcome === 'uncertain' && <p className="warning">Creation outcome unknown. Source tuples and idempotency key are retained for this exact retry.</p>}
      {creation?.outcome === 'recorded' && <button className="secondary" disabled={busy} onClick={() => { setCreation(undefined); setSources([]); setCollection(undefined); }}>Start another graph</button>}
    </details>}
    {graph && <article className="graph-inspection"><div className="section-heading"><h3>Inspected repository graph</h3><button className="secondary" disabled={busy} onClick={() => void inspect(graph.id)}>Refresh inspected graph</button></div>
      <dl><dt>Graph</dt><dd><code>{graph.id}</code></dd><dt>Digest</dt><dd><code>{graph.digest}</code></dd><dt>Indexer</dt><dd>{graph.snapshot.indexer}</dd><dt>Retained</dt><dd>{dateLabel(graph.createdAt)}</dd><dt>Structure</dt><dd>{graph.snapshot.nodes.length} nodes · {graph.snapshot.edges.length} relationships</dd></dl>
      <ul className="record-list" aria-label="Inspected graph sources">{graph.snapshot.sources.map(s => <li key={s.repositoryId}><strong>{s.repositoryId}</strong><p>Commit <code>{s.commit}</code></p><p>Collection <code>{s.collectionId}</code> · source digest <code>{s.digest}</code></p><p>{s.fullSourceDigest ? <>Whole-source digest <code>{s.fullSourceDigest}</code></> : 'Selected review paths only'} · Freshness: {s.freshness}</p></li>)}</ul>
      {graph.snapshot.truncated && <p className="warning">The graph is truncated. Missing relationships cannot be assumed absent.</p>}
      <GraphGaps gaps={graph.snapshot.gaps} />
      <form className="control-row" onSubmit={e=>{e.preventDefault();void readGraphSource();}}><label htmlFor="graph-artifact-repository">Retained source repository<select id="graph-artifact-repository" aria-label="Retained source repository" value={artifactRepositoryID} disabled={busy} onChange={e=>{setArtifactRepositoryID(e.target.value);setArtifact(undefined);}}>{graph.snapshot.sources.map(s=><option key={s.repositoryId} value={s.repositoryId}>{s.repositoryId}</option>)}</select></label><label htmlFor="graph-artifact-path">Retained source path<input id="graph-artifact-path" value={artifactPath} maxLength={1024} disabled={busy} onChange={e=>{setArtifactPath(e.target.value);setArtifact(undefined);}}/></label><button className="secondary" disabled={busy||!artifactPath} type="submit">Read graph source</button></form>
      {artifact&&<section aria-label="Inspected graph source text"><h4>{visibleControls(artifact.source.repositoryId)} · {visibleControls(artifact.artifact.path)}</h4><dl><dt>Graph digest</dt><dd><code>{artifact.digest}</code></dd><dt>Collection</dt><dd><code>{artifact.source.collectionId}</code></dd><dt>Source commit</dt><dd><code>{artifact.source.commit}</code></dd><dt>Receipt digest</dt><dd><code>{artifact.source.digest}</code></dd><dt>Source coverage</dt><dd>{artifact.coverage} · {artifact.coverageTruncated?'Truncated':'Bounded'} · freshness {artifact.source.freshness}</dd><dt>Artifact state</dt><dd>{artifact.artifact.state}</dd><dt>Text digest</dt><dd><code>{artifact.artifact.digest??'Unavailable'}</code></dd></dl>{artifact.artifact.message&&<p className="warning">{visibleControls(artifact.artifact.message)}</p>}{artifact.artifact.text!==undefined?<pre className="source-text">{visibleControls(artifact.artifact.text)}</pre>:<p className="warning">No complete source text is retained for this path.</p>}<p className="muted">Retained source is not passing verification or proof of current repository state.</p></section>}

      <form className="control-row" onSubmit={event => { event.preventDefault(); void queryGraph(); }}><label htmlFor="graph-search">Find a symbol or path<input id="graph-search" maxLength={256} value={search} disabled={busy} onChange={event => { setSearch(event.target.value); setQuery(undefined); }} /></label><label htmlFor="graph-depth">Relationship depth<select id="graph-depth" aria-label="Relationship depth" value={depth} disabled={busy} onChange={event => { setDepth(Number(event.target.value)); setQuery(undefined); }}>{[0, 1, 2, 3, 4, 5].map(n => <option key={n}>{n}</option>)}</select></label><button disabled={busy}>Search relationships</button></form>
      {query && <section aria-label="Graph query results"><h4>Retained relationships</h4>{query.truncated && <p className="warning">Query results are truncated. At most 100 nodes are shown.</p>}{query.nodes.length === 0 && <p>No matching nodes were returned within this graph's coverage.</p>}
        <div className="graph-table"><table><thead><tr><th>Symbol / file</th><th>Repository</th><th>Source</th><th>Explore</th></tr></thead><tbody>{query.nodes.map(n => <tr key={n.id}><td>{n.name}<br /><span className="muted">{n.kind}</span></td><td>{n.repositoryId}</td><td><code>{n.path ?? 'Path unavailable'}{n.line ? `:${n.line}` : ''}</code>{n.artifactDigest && <details><summary>Source digest</summary><code>{n.artifactDigest}</code></details>}</td><td><button className="secondary" disabled={busy || !isHex(n.id, 64)} onClick={() => void queryGraph(n.id)}>Inspect relations</button>{n.path&&<button className="secondary" disabled={busy} onClick={()=>void readGraphSource(n.repositoryId,n.path)}>Read source</button>}</td></tr>)}</tbody></table></div>
        <ul className="record-list" aria-label="Graph relationships">{query.edges.map((e, i) => <li key={`${e.from}-${e.to}-${i}`}><strong>{names.get(e.from)}</strong> → <strong>{names.get(e.to)}</strong> · {e.kind}<p>{e.evidence}</p><p className="muted">Provenance: {e.provenance ?? 'Unspecified; structural inference only'}</p></li>)}</ul></section>}
    </article>}
  </section>;
}

function GraphGaps({ gaps }: { gaps: RepositoryGraph['snapshot']['gaps'] }) {
  return <details className="graph-gaps" open={gaps.length > 0 || undefined}><summary>Coverage gaps ({gaps.length})</summary>{gaps.length === 0 ? <p>No gaps were recorded in the collected scope. This does not prove repository completeness or passing verification.</p> : <ul>{gaps.map((g, i) => <li key={i}><strong>{g.repositoryId}</strong>{g.path && <> · <code>{g.path}</code></>} · {g.state}<p>{g.message}</p></li>)}</ul>}</details>;
}
