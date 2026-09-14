package store

import (
	"context"
	"fmt"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func (p *Postgres) List(ctx context.Context, repository, before string, limit int) (domain.ChangePage, error) {
	cursor, err := domain.ValidateChangeQuery(repository, before, limit)
	if err != nil {
		return domain.ChangePage{}, err
	}
	var beforeTime *time.Time
	var beforeID string
	if cursor != nil {
		beforeTime, beforeID = &cursor.CreatedAt, cursor.ID
	}
	var workspaceID, principalID, repositoryID string
	if p.access != nil {
		workspaceID, principalID, repositoryID = p.access.request.WorkspaceID, p.access.principal.ID, p.access.request.RepositoryID
	}
	// One statement reads a consistent latest-revision/approval snapshot for all
	// clients. Creation coordinates keep pagination stable when another actor
	// revises a package; labels and text previews come only from that same revision.
	// PostgreSQL's character bounds preserve UTF-8 without fetching entire content.
	rows, err := p.queries().Query(ctx, `SELECT COALESCE(c.workspace_id,''),COALESCE(c.repository_id,''),c.id,r.revision,r.digest,r.author,c.created_at,
		EXISTS(SELECT 1 FROM approvals a WHERE a.change_id=c.id AND a.revision=r.revision AND a.digest=r.digest),
		r.repository,r.title,r.title_truncated,r.intent,r.intent_truncated
		FROM changes c JOIN LATERAL (
			SELECT revision,digest,author,
				CASE WHEN jsonb_typeof(content->'repositoryContext'->'repository')='string'
				THEN content->'repositoryContext'->>'repository' ELSE '' END AS repository,
				CASE WHEN jsonb_typeof(content->'title')='string'
				THEN left(content->>'title',$8) ELSE '' END AS title,
				CASE WHEN jsonb_typeof(content->'title')='string'
				THEN char_length(content->>'title')>$8 ELSE false END AS title_truncated,
				CASE WHEN jsonb_typeof(content->'intent')='string'
				THEN left(content->>'intent',$9) ELSE '' END AS intent,
				CASE WHEN jsonb_typeof(content->'intent')='string'
				THEN char_length(content->>'intent')>$9 ELSE false END AS intent_truncated
			FROM work_package_revisions WHERE change_id=c.id ORDER BY revision DESC LIMIT 1
		) r ON true
		WHERE ($1::text='' OR r.repository=$1)
		AND (($5::text='' AND c.workspace_id IS NULL AND c.repository_id IS NULL) OR
		(c.workspace_id=$5 AND EXISTS(SELECT 1 FROM repository_grants g WHERE g.repository_id=c.repository_id AND g.principal_id=$6 AND g.can_read)))
		AND ($7::text='' OR c.repository_id=$7)
		AND ($2::timestamptz IS NULL OR (c.created_at,c.id)<($2,$3::text))
		ORDER BY c.created_at DESC,c.id DESC LIMIT $4`, repository, beforeTime, beforeID, limit+1, workspaceID, principalID, repositoryID,
		domain.MaxChangeSummaryTitleRunes, domain.MaxChangeSummaryIntentRunes)
	if err != nil {
		return domain.ChangePage{}, fmt.Errorf("list changes: %w", err)
	}
	defer rows.Close()
	page := domain.ChangePage{Changes: make([]domain.ChangeSummary, 0, limit)}
	for rows.Next() {
		var change domain.ChangeSummary
		if err := rows.Scan(&change.WorkspaceID, &change.RepositoryID, &change.ID, &change.Revision, &change.Digest, &change.Author, &change.CreatedAt, &change.Approved, &change.Repository,
			&change.Title, &change.TitleTruncated, &change.Intent, &change.IntentTruncated); err != nil {
			return domain.ChangePage{}, fmt.Errorf("decode change summary: %w", err)
		}
		if len(page.Changes) == limit {
			last := page.Changes[limit-1]
			page.NextBefore, err = domain.EncodeChangeCursor(last.CreatedAt, last.ID)
			if err != nil {
				return domain.ChangePage{}, fmt.Errorf("encode changes cursor: %w", err)
			}
			break
		}
		page.Changes = append(page.Changes, change)
	}
	if err := rows.Err(); err != nil {
		return domain.ChangePage{}, fmt.Errorf("list changes: %w", err)
	}
	return page, nil
}
