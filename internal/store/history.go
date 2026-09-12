package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

func (p *Postgres) historyExists(ctx context.Context, id string) error {
	var exists bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM changes WHERE id=$1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("find history: %w", err)
	}
	if !exists {
		return domain.ErrNotFound
	}
	return nil
}

func (p *Postgres) History(ctx context.Context, id string, beforeRevision int64, limit int) (domain.HistoryPage, error) {
	if err := domain.ValidateHistoryQuery(id, beforeRevision, limit); err != nil {
		return domain.HistoryPage{}, err
	}
	if err := p.historyExists(ctx, id); err != nil {
		return domain.HistoryPage{}, err
	}
	rows, err := p.pool.Query(ctx, `SELECT r.revision,r.digest,r.author,r.created_at,r.submitted_at,
		(SELECT count(*) FROM approvals a WHERE a.change_id=r.change_id AND a.revision=r.revision AND a.digest=r.digest)
		FROM work_package_revisions r WHERE r.change_id=$1 AND ($2::bigint=0 OR r.revision<$2)
		ORDER BY r.revision DESC LIMIT $3`, id, beforeRevision, limit+1)
	if err != nil {
		return domain.HistoryPage{}, fmt.Errorf("read revision history: %w", err)
	}
	defer rows.Close()
	page := domain.HistoryPage{Revisions: make([]domain.RevisionSummary, 0, limit)}
	for rows.Next() {
		var revision domain.RevisionSummary
		if err := rows.Scan(&revision.Number, &revision.Digest, &revision.Author, &revision.CreatedAt, &revision.SubmittedAt, &revision.ApprovalCount); err != nil {
			return domain.HistoryPage{}, fmt.Errorf("decode revision history: %w", err)
		}
		if len(page.Revisions) == limit {
			page.NextBeforeRevision = page.Revisions[limit-1].Number
			break
		}
		page.Revisions = append(page.Revisions, revision)
	}
	if err := rows.Err(); err != nil {
		return domain.HistoryPage{}, fmt.Errorf("read revision history: %w", err)
	}
	return page, nil
}

func (p *Postgres) Revision(ctx context.Context, id string, revision int64) (domain.RevisionRecord, error) {
	if id == "" || revision < 1 {
		return domain.RevisionRecord{}, domain.ErrInvalidInput
	}
	// Submission and approval can still arrive while a revision is current. Read
	// both from one snapshot so a record cannot pair an unsubmitted revision with
	// an approval that committed after the revision was read.
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return domain.RevisionRecord{}, fmt.Errorf("begin revision inspection: %w", err)
	}
	defer tx.Rollback(ctx)
	record := domain.RevisionRecord{Approvals: make([]domain.Approval, 0)}
	r := &record.Revision
	err = tx.QueryRow(ctx, `SELECT change_id,revision,schema_version,digest,content,author,created_at,submitted_at
		FROM work_package_revisions WHERE change_id=$1 AND revision=$2`, id, revision).Scan(
		&r.ChangeID, &r.Number, &r.SchemaVersion, &r.Digest, &r.Content, &r.Author, &r.CreatedAt, &r.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RevisionRecord{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RevisionRecord{}, fmt.Errorf("read historical revision: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT change_id,revision,digest,reviewer,created_at FROM approvals
		WHERE change_id=$1 AND revision=$2 AND digest=$3 ORDER BY created_at,reviewer LIMIT $4`,
		id, revision, r.Digest, domain.MaxHistoricalApprovals+1)
	if err != nil {
		return domain.RevisionRecord{}, fmt.Errorf("read historical approvals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var approval domain.Approval
		if err := rows.Scan(&approval.ChangeID, &approval.Revision, &approval.Digest, &approval.Reviewer, &approval.CreatedAt); err != nil {
			return domain.RevisionRecord{}, fmt.Errorf("decode historical approval: %w", err)
		}
		if len(record.Approvals) == domain.MaxHistoricalApprovals {
			record.ApprovalsTruncated = true
			break
		}
		record.Approvals = append(record.Approvals, approval)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.RevisionRecord{}, fmt.Errorf("read historical approvals: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RevisionRecord{}, fmt.Errorf("finish revision inspection: %w", err)
	}
	return record, nil
}

func (p *Postgres) Events(ctx context.Context, id string, afterSequence int64, limit int) (domain.AuditPage, error) {
	if err := domain.ValidateHistoryQuery(id, afterSequence, limit); err != nil {
		return domain.AuditPage{}, err
	}
	if err := p.historyExists(ctx, id); err != nil {
		return domain.AuditPage{}, err
	}
	rows, err := p.pool.Query(ctx, `SELECT sequence,change_id,event_type,actor,revision,data,created_at
		FROM audit_events WHERE change_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3`, id, afterSequence, limit+1)
	if err != nil {
		return domain.AuditPage{}, fmt.Errorf("read audit events: %w", err)
	}
	defer rows.Close()
	page := domain.AuditPage{Events: make([]domain.AuditEvent, 0, limit)}
	for rows.Next() {
		var event domain.AuditEvent
		if err := rows.Scan(&event.Sequence, &event.ChangeID, &event.EventType, &event.Actor, &event.Revision, &event.Data, &event.CreatedAt); err != nil {
			return domain.AuditPage{}, fmt.Errorf("decode audit event: %w", err)
		}
		if len(page.Events) == limit {
			page.NextAfterSequence = page.Events[limit-1].Sequence
			break
		}
		page.Events = append(page.Events, event)
	}
	if err := rows.Err(); err != nil {
		return domain.AuditPage{}, fmt.Errorf("read audit events: %w", err)
	}
	return page, nil
}
