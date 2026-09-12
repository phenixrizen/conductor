import { useEffect, useRef, useState } from 'react';
import { dateLabel, errorMessage, request } from './api';
import type { SharedPage } from './api';

export interface RelatedRequest { repository: string; sequence: number }

export function SharedChanges({ actor, disabled, related, onInspect }: {
  actor: string;
  disabled: boolean;
  related?: RelatedRequest;
  onInspect: (id: string) => void;
}) {
  const [repository, setRepository] = useState('');
  const [page, setPage] = useState<SharedPage>();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');
  const [loadedAt, setLoadedAt] = useState('');
  const generation = useRef(0);
  const controller = useRef<AbortController | undefined>(undefined);

  useEffect(() => () => { generation.current++; controller.current?.abort(); }, []);

  async function browse(filter: string, before?: string) {
    controller.current?.abort();
    const token = ++generation.current;
    const abort = new AbortController();
    controller.current = abort;
    setPending(true);
    setError('');
    if (!before) { setPage(undefined); setLoadedAt(''); }
    const query = new URLSearchParams({ limit: '20' });
    if (filter) query.set('repository', filter);
    if (before) query.set('before', before);
    try {
      const value = await request<SharedPage>(`/changes?${query}`, actor, abort.signal);
      if (generation.current !== token || abort.signal.aborted) return;
      setPage(previous => before ? { ...value, changes: [...(previous?.changes ?? []), ...value.changes] } : value);
      setLoadedAt(new Date().toLocaleTimeString());
    } catch (failure) {
      if (generation.current === token && !abort.signal.aborted) setError(errorMessage(failure));
    } finally {
      if (generation.current === token && !abort.signal.aborted) setPending(false);
    }
  }

  useEffect(() => {
    if (!related) return;
    setRepository(related.repository);
    void browse(related.repository);
  }, [related]);

  function changeFilter(value: string) {
    generation.current++;
    controller.current?.abort();
    setPending(false);
    setRepository(value);
    setPage(undefined);
    setError('');
    setLoadedAt('');
  }

  return <section className="shared-work panel" aria-labelledby="shared-work-title" aria-busy={pending}>
    <div className="section-heading"><h2 id="shared-work-title">Shared work</h2><span className="tag">Same server · shared records</span></div>
    <p className="muted">Find work from other developers and agents, or filter by the exact repository identity attached to a package.</p>
    <form className="control-row" onSubmit={event => { event.preventDefault(); void browse(repository); }}>
      <label>Repository filter<input value={repository} placeholder="All repositories" disabled={disabled || pending} maxLength={4096}
        onChange={event => changeFilter(event.target.value)} /></label>
      <button type="submit" className="secondary" disabled={disabled || pending || !actor.trim()}>Browse shared work</button>
      {repository && <button type="button" className="secondary" disabled={disabled || pending || !actor.trim()}
        onClick={() => { setRepository(''); void browse(''); }}>Browse all</button>}
    </form>
    <p role="status" className="muted">{pending ? 'Loading shared work…' : loadedAt ? `List refreshed at ${loadedAt}.` : 'Browse to load shared work packages.'}</p>
    {/* A discovery list is a snapshot. Only an explicit inspection can establish approval inputs. */}
    <p className="muted">This list is a snapshot of recorded packages. It does not show live agent activity or execution progress. Inspect a package to load its current content.</p>
    {error && <p role="alert" className="error">{error}</p>}
    {page?.changes.length === 0 && <p className="empty-list">No shared work matches this repository filter.</p>}
    <ul className="record-list shared-list">
      {page?.changes.map(change => <li key={change.id}>
        <button type="button" className="shared-card" disabled={disabled || pending} onClick={() => onInspect(change.id)} aria-label={`Inspect change ${change.id}`}>
          <span className="history-top"><strong>{change.id}</strong><span className="tag">Revision {change.revision}</span></span>
          <span>{change.author} · {dateLabel(change.createdAt)}</span>
          <span className="shared-state">{change.approved ? 'Design approved' : 'No effective approval'}</span>
          <span>{change.repository || 'No repository context attached'}</span>
          <code>{change.digest.slice(0, 16)}…</code>
        </button>
      </li>)}
    </ul>
    {page?.nextBefore && <button type="button" className="secondary" disabled={disabled || pending}
      onClick={() => void browse(repository, page.nextBefore)}>Load more shared work</button>}
  </section>;
}
