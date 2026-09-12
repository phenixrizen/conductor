import type { EventPage, HistoryPage } from './api';
import { dateLabel } from './api';

export function History({ page, error, selected, disabled, onSelect, onMore }: {
  page?: HistoryPage; error: string; selected: number; disabled: boolean;
  onSelect: (revision: number) => void; onMore: () => void;
}) {
  return <section className="panel history" aria-labelledby="history-title">
    <h3 id="history-title">Revision history</h3>
    <p className="muted">Newest first. Select a revision to inspect its preserved content and approval records.</p>
    {error && <p role="alert" className="error">{error}</p>}
    {!page && !error && <p role="status">Loading history…</p>}
    {page?.revisions.length === 0 && <p>No revisions were returned.</p>}
    <ol className="record-list">
      {page?.revisions.map(revision => <li key={revision.number}>
        <button className={`history-button ${selected === revision.number ? 'selected' : ''}`} disabled={disabled}
          onClick={() => onSelect(revision.number)} aria-label={`Inspect historical revision ${revision.number}`}>
          <span className="history-top"><strong>Revision {revision.number}</strong>{selected === revision.number && <span className="tag">Displayed</span>}</span>
          <span>{revision.author} · {dateLabel(revision.createdAt)}</span>
          <span>{revision.submittedAt ? 'Submitted' : 'Draft'} · {revision.approvalCount} recorded {revision.approvalCount === 1 ? 'approval' : 'approvals'}</span>
          <code>{revision.digest.slice(0, 16)}…</code>
        </button>
      </li>)}
    </ol>
    {!!page?.nextBeforeRevision && <button className="secondary" disabled={disabled} onClick={onMore}>Load older revisions</button>}
  </section>;
}

export function Events({ page, error, disabled, onMore }: {
  page?: EventPage; error: string; disabled: boolean; onMore: () => void;
}) {
  return <section className="panel" aria-labelledby="events-title">
    <h3 id="events-title">Audit events</h3>
    <p className="muted">Recorded actions, oldest first. Event timestamps describe when an action was recorded.</p>
    {error && <p role="alert" className="error">{error}</p>}
    {!page && !error && <p role="status">Loading events…</p>}
    {page?.events.length === 0 && <p>No audit events were returned.</p>}
    <ol className="record-list events">
      {page?.events.map(event => <li key={event.sequence}>
        <div className="event-heading"><strong>{event.eventType.replaceAll('_', ' ')}</strong><span className="tag">#{event.sequence}</span></div>
        <p>Revision {event.revision} · {event.actor}</p>
        <time dateTime={event.createdAt}>{dateLabel(event.createdAt)}</time>
        <details><summary>Event data</summary><pre>{JSON.stringify(event.data, null, 2)}</pre></details>
      </li>)}
    </ol>
    {!!page?.nextAfterSequence && <button className="secondary" disabled={disabled} onClick={onMore}>Load more events</button>}
  </section>;
}
