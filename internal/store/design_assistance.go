package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/domain"
)

func (p *Postgres) assistanceAccess(ctx context.Context, action, kind string) error {
	if p.tx == nil || p.access == nil || p.access.request.RepositoryID == "" {
		return domain.ErrForbidden
	}
	if err := p.authorizeRepository(ctx, p.tx, p.access.request.RepositoryID, action); err != nil {
		return err
	}
	if kind != "" && p.Principal().Kind != kind {
		return domain.ErrForbidden
	}
	return nil
}

// Every command key is scoped to the current principal, canonical repository and
// operation. Authorization locks precede replay; the exact committed key wins
// over subsequent revision drift. The same key cannot target another request.
func (p *Postgres) assistanceReplay(ctx context.Context, operation, key, id string, input any) (string, string, error) {
	digest, err := domain.JSONDigest(struct {
		ID    string `json:"id"`
		Input any    `json:"input"`
	}{id, input})
	if err != nil {
		return "", "", err
	}
	lock, err := domain.JSONDigest([]string{p.access.request.RepositoryID, p.Principal().ID, operation, key})
	if err != nil {
		return "", "", err
	}
	if _, err = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "design-command:"+lock); err != nil {
		return "", "", err
	}
	var previousID, previousDigest string
	err = p.tx.QueryRow(ctx, `SELECT request_id,input_digest FROM design_assistance_commands WHERE repository_id=$1 AND principal_id=$2 AND operation=$3 AND idempotency_key=$4`, p.access.request.RepositoryID, p.Principal().ID, operation, key).Scan(&previousID, &previousDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", digest, nil
	}
	if err != nil {
		return "", "", err
	}
	if previousDigest != digest {
		return "", "", domain.ErrIdempotencyConflict
	}
	return previousID, digest, nil
}
func (p *Postgres) recordAssistanceCommand(ctx context.Context, operation, key, digest, id string) error {
	_, err := p.tx.Exec(ctx, `INSERT INTO design_assistance_commands(repository_id,principal_id,operation,idempotency_key,input_digest,request_id) VALUES($1,$2,$3,$4,$5,$6)`, p.access.request.RepositoryID, p.Principal().ID, operation, key, digest, id)
	return err
}
func (p *Postgres) assistanceAudit(ctx context.Context, value domain.DesignAssistance, kind string, revision int64, data map[string]string) error {
	data["assistanceId"] = value.ID
	_, err := p.tx.Exec(ctx, `INSERT INTO audit_events(change_id,event_type,actor,revision,data,created_at) VALUES($1,$2,$3,$4,$5,clock_timestamp())`, value.Input.ChangeID, kind, p.Principal().ID, revision, data)
	return err
}
func (p *Postgres) lockAssistance(ctx context.Context, id string) error {
	_, err := p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "design-assistance:"+id)
	return err
}
func (p *Postgres) RequestDesignAssistance(ctx context.Context, id, key string, in domain.AssistanceInput) (domain.DesignAssistance, error) {
	var zero domain.DesignAssistance
	if !domain.IsLowerHex(id, 32) || domain.ValidateCollectionKey(key) != nil || domain.ValidateAssistanceInput(in) != nil {
		return zero, domain.ErrInvalidInput
	}
	if err := p.assistanceAccess(ctx, "author", "human"); err != nil {
		return zero, err
	}
	previous, digest, err := p.assistanceReplay(ctx, "request", key, "", in)
	if err != nil {
		return zero, err
	}
	if previous != "" {
		return p.GetDesignAssistance(ctx, previous)
	}
	if _, err = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.ChangeID); err != nil {
		return zero, err
	}
	if err = p.authorizeChange(ctx, p.tx, in.ChangeID, "author"); err != nil {
		return zero, err
	}
	base, err := getRevision(ctx, p.tx, in.ChangeID)
	if err != nil {
		return zero, err
	}
	if err = domain.ValidateAssistanceBase(in, base); err != nil {
		return zero, err
	}
	actual, err := domain.Digest(base.Content)
	if err != nil || actual != base.Digest {
		return zero, domain.ErrUnavailable
	}
	base.CreatedAt = base.CreatedAt.UTC()
	normalizeOptionalTime(base.SubmittedAt)
	record := domain.DesignAssistance{ID: id, WorkspaceID: p.access.request.WorkspaceID, RepositoryID: p.access.request.RepositoryID, RequesterID: p.Principal().ID, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Input: in, Base: base}
	record.Digest, err = domain.DesignAssistanceDigest(record)
	if err != nil {
		return zero, err
	}
	_, err = p.tx.Exec(ctx, `INSERT INTO design_assistance_requests(id,workspace_id,repository_id,requester_id,change_id,revision,revision_digest,digest,input,base,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, record.WorkspaceID, record.RepositoryID, record.RequesterID, in.ChangeID, base.Number, base.Digest, record.Digest, in, base, record.CreatedAt)
	if err != nil {
		return zero, err
	}
	if err = p.assistanceAudit(ctx, record, "design_assistance.requested", base.Number, map[string]string{"requestDigest": record.Digest}); err != nil {
		return zero, err
	}
	if err = p.recordAssistanceCommand(ctx, "request", key, digest, id); err != nil {
		return zero, err
	}
	return record, nil
}
func (p *Postgres) GetDesignAssistance(ctx context.Context, id string) (domain.DesignAssistance, error) {
	var record domain.DesignAssistance
	if !domain.IsLowerHex(id, 32) {
		return record, domain.ErrInvalidInput
	}
	if err := p.assistanceAccess(ctx, "read", ""); err != nil {
		return record, err
	}
	// The immutable base and optional facts share one statement snapshot. Query
	// scope is fixed before loading any private instructions or proposed text.
	err := p.tx.QueryRow(ctx, `SELECT r.id,r.workspace_id,r.repository_id,r.requester_id,r.created_at,r.digest,r.input,r.base,s.fact,a.fact
 FROM design_assistance_requests r LEFT JOIN design_assistance_suggestions s ON s.request_id=r.id LEFT JOIN design_assistance_applications a ON a.request_id=r.id
 WHERE r.id=$1 AND r.workspace_id=$2 AND r.repository_id=$3`, id, p.access.request.WorkspaceID, p.access.request.RepositoryID).Scan(&record.ID, &record.WorkspaceID, &record.RepositoryID, &record.RequesterID, &record.CreatedAt, &record.Digest, &record.Input, &record.Base, &record.Suggestion, &record.Application)
	if errors.Is(err, pgx.ErrNoRows) {
		return record, domain.ErrNotFound
	}
	if err != nil {
		return domain.DesignAssistance{}, fmt.Errorf("read design assistance: %w", err)
	}
	record.CreatedAt = record.CreatedAt.UTC()
	if err = validateStoredAssistance(record); err != nil {
		return domain.DesignAssistance{}, err
	}
	return record, nil
}
func validateStoredAssistance(record domain.DesignAssistance) error {
	digest, err := domain.DesignAssistanceDigest(record)
	if err != nil || digest != record.Digest || domain.ValidateAssistanceBase(record.Input, record.Base) != nil {
		return domain.ErrUnavailable
	}
	digest, err = domain.Digest(record.Base.Content)
	if err != nil || digest != record.Base.Digest {
		return domain.ErrUnavailable
	}
	if s := record.Suggestion; s != nil {
		digest, err = domain.AssistanceSuggestionDigest(record.Digest, *s)
		if err != nil || digest != s.Digest || domain.ValidateAssistanceSuggestion(record, domain.SuggestionInput{RequestDigest: record.Digest, Sections: s.Sections, Note: s.Note}) != nil {
			return domain.ErrUnavailable
		}
	}
	if a := record.Application; a != nil {
		if record.Suggestion == nil || a.AppliedBy != record.RequesterID {
			return domain.ErrUnavailable
		}
		digest, err = domain.AssistanceApplicationDigest(record.Digest, record.Suggestion.Digest, *a)
		if err != nil || digest != a.Digest || a.Revision != record.Base.Number+1 {
			return domain.ErrUnavailable
		}
		_, contentDigest, mergeErr := domain.MergeAssistanceSections(record, record.Base, domain.ApplySuggestionInput{RequestDigest: record.Digest, SuggestionDigest: record.Suggestion.Digest, ExpectedRevision: record.Base.Number, ExpectedDigest: record.Base.Digest, Sections: a.Sections})
		if mergeErr != nil || contentDigest != a.RevisionDigest {
			return domain.ErrUnavailable
		}
	}
	return nil
}
func (p *Postgres) ListDesignAssistance(ctx context.Context, in domain.AssistanceListOptions) (domain.AssistancePage, error) {
	page := domain.AssistancePage{Requests: make([]domain.AssistanceSummary, 0)}
	cursor, err := domain.ValidateAssistanceListOptions(in)
	if err != nil {
		return page, err
	}
	if err = p.assistanceAccess(ctx, "read", ""); err != nil {
		return page, err
	}
	if in.ChangeID != "" {
		if err = p.authorizeChange(ctx, p.tx, in.ChangeID, "read"); err != nil {
			return page, err
		}
	}
	var beforeTime *time.Time
	beforeID := ""
	if cursor != nil {
		beforeTime = &cursor.CreatedAt
		beforeID = cursor.ID
	}
	rows, err := p.tx.Query(ctx, `SELECT r.id,r.requester_id,r.created_at,r.digest,r.input,s.request_id IS NOT NULL,COALESCE(a.revision,0)
 FROM design_assistance_requests r LEFT JOIN design_assistance_suggestions s ON s.request_id=r.id LEFT JOIN design_assistance_applications a ON a.request_id=r.id
 WHERE r.workspace_id=$1 AND r.repository_id=$2 AND ($3::text='' OR r.change_id=$3)
 AND ($4::timestamptz IS NULL OR (r.created_at,r.id)<($4,$5::text)) ORDER BY r.created_at DESC,r.id DESC LIMIT $6`, p.access.request.WorkspaceID, p.access.request.RepositoryID, in.ChangeID, beforeTime, beforeID, in.Limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item domain.AssistanceSummary
		if err = rows.Scan(&item.ID, &item.RequesterID, &item.CreatedAt, &item.Digest, &item.Input, &item.HasSuggestion, &item.AppliedRevision); err != nil {
			return domain.AssistancePage{}, err
		}
		if len(page.Requests) == in.Limit {
			last := page.Requests[len(page.Requests)-1]
			page.NextBefore, err = domain.EncodeChangeCursor(last.CreatedAt, last.ID)
			if err != nil {
				return domain.AssistancePage{}, err
			}
			break
		}
		item.CreatedAt = item.CreatedAt.UTC()
		page.Requests = append(page.Requests, item)
	}
	return page, rows.Err()
}
func (p *Postgres) ProposeDesignSections(ctx context.Context, id, key string, in domain.SuggestionInput) (domain.DesignAssistance, error) {
	var zero domain.DesignAssistance
	if !domain.IsLowerHex(id, 32) || domain.ValidateCollectionKey(key) != nil || domain.ValidateSuggestionInput(in) != nil {
		return zero, domain.ErrInvalidInput
	}
	if err := p.assistanceAccess(ctx, "author", "agent"); err != nil {
		return zero, err
	}
	previous, digest, err := p.assistanceReplay(ctx, "suggestion", key, id, in)
	if err != nil {
		return zero, err
	}
	if previous != "" {
		return p.GetDesignAssistance(ctx, previous)
	}
	if err = p.lockAssistance(ctx, id); err != nil {
		return zero, err
	}
	record, err := p.GetDesignAssistance(ctx, id)
	if err != nil {
		return zero, err
	}
	if err = domain.ValidateAssistanceSuggestion(record, in); err != nil {
		return zero, err
	}
	if record.Suggestion != nil {
		return zero, domain.ErrConflict
	}
	fact := domain.AssistanceSuggestion{AgentID: p.Principal().ID, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Sections: in.Sections, Note: in.Note}
	fact.Digest, err = domain.AssistanceSuggestionDigest(record.Digest, fact)
	if err != nil {
		return zero, err
	}
	_, err = p.tx.Exec(ctx, `INSERT INTO design_assistance_suggestions(request_id,digest,agent_id,fact) VALUES($1,$2,$3,$4)`, id, fact.Digest, fact.AgentID, fact)
	if err != nil {
		return zero, err
	}
	if err = p.assistanceAudit(ctx, record, "design_assistance.suggested", record.Base.Number, map[string]string{"requestDigest": record.Digest, "suggestionDigest": fact.Digest}); err != nil {
		return zero, err
	}
	if err = p.recordAssistanceCommand(ctx, "suggestion", key, digest, id); err != nil {
		return zero, err
	}
	record.Suggestion = &fact
	return record, nil
}
func (p *Postgres) ApplyDesignSuggestion(ctx context.Context, id, key string, in domain.ApplySuggestionInput) (domain.DesignAssistance, error) {
	var zero domain.DesignAssistance
	if !domain.IsLowerHex(id, 32) || domain.ValidateCollectionKey(key) != nil || domain.ValidateApplySuggestionInput(in) != nil {
		return zero, domain.ErrInvalidInput
	}
	if err := p.assistanceAccess(ctx, "author", "human"); err != nil {
		return zero, err
	}
	previous, digest, err := p.assistanceReplay(ctx, "application", key, id, in)
	if err != nil {
		return zero, err
	}
	if previous != "" {
		record, err := p.GetDesignAssistance(ctx, previous)
		if err != nil {
			return zero, err
		}
		if record.RequesterID != p.Principal().ID {
			return zero, domain.ErrForbidden
		}
		return record, nil
	}
	if err = p.lockAssistance(ctx, id); err != nil {
		return zero, err
	}
	record, err := p.GetDesignAssistance(ctx, id)
	if err != nil {
		return zero, err
	}
	if record.RequesterID != p.Principal().ID {
		return zero, domain.ErrForbidden
	}
	if record.Application != nil {
		return zero, domain.ErrConflict
	}
	if _, err = p.tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, record.Input.ChangeID); err != nil {
		return zero, err
	}
	if err = p.authorizeChange(ctx, p.tx, record.Input.ChangeID, "author"); err != nil {
		return zero, err
	}
	current, err := getRevision(ctx, p.tx, record.Input.ChangeID)
	if err != nil {
		return zero, err
	}
	content, contentDigest, err := domain.MergeAssistanceSections(record, current, in)
	if err != nil {
		return zero, err
	}
	var existed bool
	if err = p.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM work_package_revisions WHERE change_id=$1 AND digest=$2)`, record.Input.ChangeID, contentDigest).Scan(&existed); err != nil {
		return zero, err
	}
	if existed {
		return zero, domain.ErrConflict
	}
	// This savepoint reuses normal receipt-link validation and revision/audit
	// commands. The outer access transaction also owns the assistance fact/key.
	produced, err := p.Revise(ctx, record.Input.ChangeID, p.Principal().ID, current.Number, content, contentDigest, time.Now().UTC())
	if err != nil {
		return zero, err
	}
	fact := domain.AssistanceApplication{AppliedBy: p.Principal().ID, CreatedAt: time.Now().UTC().Truncate(time.Microsecond), Revision: produced.Revision.Number, RevisionDigest: produced.Revision.Digest, Sections: in.Sections}
	fact.Digest, err = domain.AssistanceApplicationDigest(record.Digest, record.Suggestion.Digest, fact)
	if err != nil {
		return zero, err
	}
	_, err = p.tx.Exec(ctx, `INSERT INTO design_assistance_applications(request_id,digest,applied_by,change_id,revision,revision_digest,fact) VALUES($1,$2,$3,$4,$5,$6,$7)`, id, fact.Digest, fact.AppliedBy, record.Input.ChangeID, fact.Revision, fact.RevisionDigest, fact)
	if err != nil {
		return zero, err
	}
	if err = p.assistanceAudit(ctx, record, "design_assistance.applied", fact.Revision, map[string]string{"requestDigest": record.Digest, "suggestionDigest": record.Suggestion.Digest, "applicationDigest": fact.Digest}); err != nil {
		return zero, err
	}
	if err = p.recordAssistanceCommand(ctx, "application", key, digest, id); err != nil {
		return zero, err
	}
	record.Application = &fact
	return record, nil
}
