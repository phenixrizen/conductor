package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/phenixrizen/conductor/internal/domain"
)

var ErrNotFound = domain.ErrNotFound

type Postgres struct {
	pool   *pgxpool.Pool
	tx     pgx.Tx
	access *transactionAccess
}

func Open(ctx context.Context, url string) (*Postgres, error) {
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return &Postgres{pool: p}, nil
}
func (p *Postgres) Close() { p.pool.Close() }

func (p *Postgres) Create(ctx context.Context, id, actor string, content domain.Content, digest string, now time.Time) (domain.Package, error) {
	tx, err := p.begin(ctx)
	if err != nil {
		return domain.Package{}, err
	}
	defer tx.Rollback(ctx)
	var workspaceID, repositoryID *string
	if p.access != nil {
		if p.access.request.RepositoryID == "" {
			return domain.Package{}, domain.ErrInvalidInput
		}
		if err = p.authorizeRepository(ctx, tx, p.access.request.RepositoryID, "author"); err != nil {
			return domain.Package{}, err
		}
		workspaceID, repositoryID = &p.access.request.WorkspaceID, &p.access.request.RepositoryID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO changes(id,created_at,workspace_id,repository_id) VALUES($1,$2,$3,$4)`, id, now, workspaceID, repositoryID); err != nil {
		return domain.Package{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO work_package_revisions(change_id,revision,schema_version,digest,content,author,created_at) VALUES($1,1,1,$2,$3,$4,$5)`, id, digest, content, actor, now); err != nil {
		return domain.Package{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(change_id,event_type,actor,revision,created_at) VALUES($1,'package.created',$2,1,$3)`, id, actor, now); err != nil {
		return domain.Package{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Package{}, err
	}
	return p.Get(ctx, id)
}

func (p *Postgres) Revise(ctx context.Context, id, actor string, expected int64, content domain.Content, digest string, now time.Time) (domain.Package, error) {
	tx, err := p.begin(ctx)
	if err != nil {
		return domain.Package{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, id); err != nil {
		return domain.Package{}, err
	}
	if err = p.authorizeChange(ctx, tx, id, "author"); err != nil {
		return domain.Package{}, err
	}
	var current int64
	if err = tx.QueryRow(ctx, `SELECT revision FROM work_package_revisions WHERE change_id=$1 ORDER BY revision DESC LIMIT 1`, id).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Package{}, ErrNotFound
		}
		return domain.Package{}, err
	}
	if current != expected {
		return domain.Package{}, domain.ErrConflict
	}
	next := current + 1
	_, err = tx.Exec(ctx, `INSERT INTO work_package_revisions(change_id,revision,schema_version,digest,content,author,created_at) VALUES($1,$2,1,$3,$4,$5,$6)`, id, next, digest, content, actor, now)
	if err != nil {
		return domain.Package{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(change_id,event_type,actor,revision,created_at) VALUES($1,'package.revised',$2,$3,$4)`, id, actor, next, now)
	if err != nil {
		return domain.Package{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Package{}, err
	}
	return p.Get(ctx, id)
}

func (p *Postgres) Submit(ctx context.Context, id, actor string, expected int64, now time.Time) (domain.Package, error) {
	tx, err := p.begin(ctx)
	if err != nil {
		return domain.Package{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, id); err != nil {
		return domain.Package{}, err
	}
	if err = p.authorizeChange(ctx, tx, id, "author"); err != nil {
		return domain.Package{}, err
	}
	var current int64
	if err = tx.QueryRow(ctx, `SELECT revision FROM work_package_revisions WHERE change_id=$1 ORDER BY revision DESC LIMIT 1`, id).Scan(&current); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Package{}, ErrNotFound
		}
		return domain.Package{}, err
	}
	if current != expected {
		return domain.Package{}, domain.ErrConflict
	}
	ct, err := tx.Exec(ctx, `UPDATE work_package_revisions SET submitted_at=$3 WHERE change_id=$1 AND revision=$2 AND submitted_at IS NULL`, id, current, now)
	if err != nil {
		return domain.Package{}, err
	}
	if ct.RowsAffected() == 0 {
		return domain.Package{}, domain.ErrConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(change_id,event_type,actor,revision,created_at) VALUES($1,'review.requested',$2,$3,$4)`, id, actor, current, now)
	if err != nil {
		return domain.Package{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Package{}, err
	}
	return p.Get(ctx, id)
}

func (p *Postgres) Approve(ctx context.Context, id, reviewer string, revision int64, digest string, now time.Time) (domain.Package, error) {
	tx, err := p.begin(ctx)
	if err != nil {
		return domain.Package{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, id); err != nil {
		return domain.Package{}, err
	}
	if err = p.authorizeChange(ctx, tx, id, "approve"); err != nil {
		return domain.Package{}, err
	}
	r, err := getRevision(ctx, tx, id)
	if err != nil {
		return domain.Package{}, err
	}
	if err = domain.ValidateApproval(r, revision, digest, reviewer); err != nil {
		return domain.Package{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO approvals(change_id,revision,digest,reviewer,created_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, id, revision, digest, reviewer, now)
	if err != nil {
		return domain.Package{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(change_id,event_type,actor,revision,data,created_at) VALUES($1,'review.approved',$2,$3,jsonb_build_object('digest',$4::text),$5)`, id, reviewer, revision, digest, now)
	if err != nil {
		return domain.Package{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Package{}, err
	}
	return p.Get(ctx, id)
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func getRevision(ctx context.Context, q rowQuerier, id string) (domain.Revision, error) {
	var r domain.Revision
	err := q.QueryRow(ctx, `SELECT change_id,revision,schema_version,digest,content,author,created_at,submitted_at FROM work_package_revisions WHERE change_id=$1 ORDER BY revision DESC LIMIT 1`, id).Scan(&r.ChangeID, &r.Number, &r.SchemaVersion, &r.Digest, &r.Content, &r.Author, &r.CreatedAt, &r.SubmittedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	return r, err
}
func (p *Postgres) Get(ctx context.Context, id string) (domain.Package, error) {
	if err := p.authorizeChange(ctx, p.queries(), id, "read"); err != nil {
		return domain.Package{}, err
	}
	// Read the latest revision and its effective approval in one statement. A
	// concurrent edit cannot pair content from one snapshot with approval from another.
	var pkg domain.Package
	r := &pkg.Revision
	var reviewer *string
	var approvalTime *time.Time
	err := p.queries().QueryRow(ctx, `SELECT c.id,COALESCE(c.workspace_id,''),COALESCE(c.repository_id,''),
 r.change_id,r.revision,r.schema_version,r.digest,r.content,r.author,r.created_at,r.submitted_at,a.reviewer,a.created_at
 FROM changes c JOIN LATERAL (SELECT * FROM work_package_revisions WHERE change_id=c.id ORDER BY revision DESC LIMIT 1) r ON true
 LEFT JOIN LATERAL (SELECT reviewer,created_at FROM approvals WHERE change_id=c.id AND revision=r.revision AND digest=r.digest ORDER BY created_at DESC,reviewer LIMIT 1) a ON true
 WHERE c.id=$1`, id).Scan(&pkg.ID, &pkg.WorkspaceID, &pkg.RepositoryID, &r.ChangeID, &r.Number, &r.SchemaVersion, &r.Digest, &r.Content, &r.Author, &r.CreatedAt, &r.SubmittedAt, &reviewer, &approvalTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return pkg, ErrNotFound
	}
	if err != nil {
		return pkg, fmt.Errorf("read package: %w", err)
	}
	if reviewer != nil && approvalTime != nil {
		pkg.Approval = &domain.Approval{ChangeID: id, Revision: r.Number, Digest: r.Digest, Reviewer: *reviewer, CreatedAt: *approvalTime}
		pkg.Approved = true
	}
	return pkg, nil
}
