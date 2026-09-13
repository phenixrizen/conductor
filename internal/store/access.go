package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

type queryExecutor interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}
type transactionAccess struct {
	request   domain.AccessRequest
	principal domain.Principal
}

func (p *Postgres) queries() queryExecutor {
	if p.tx != nil {
		return p.tx
	}
	return p.pool
}

// Scoped commands use savepoints in the authorization transaction. Releasing a
// savepoint keeps its permission/advisory locks until the outer commit; the same
// command implementation remains available for isolated local development.
func (p *Postgres) begin(ctx context.Context) (pgx.Tx, error) {
	if p.tx != nil {
		return p.tx.Begin(ctx)
	}
	return p.pool.Begin(ctx)
}
func (p *Postgres) beginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if p.tx != nil {
		return p.tx.Begin(ctx)
	}
	return p.pool.BeginTx(ctx, options)
}
func (p *Postgres) Principal() domain.Principal        { return p.access.principal }
func (p *Postgres) Commit(ctx context.Context) error   { return p.tx.Commit(ctx) }
func (p *Postgres) Rollback(ctx context.Context) error { return p.tx.Rollback(ctx) }

func principalInTransaction(ctx context.Context, tx pgx.Tx, identity domain.AccessIdentity) (domain.Principal, error) {
	var principal domain.Principal
	var active bool
	if identity.Issuer == "" || identity.Subject == "" || len(identity.Issuer) > 2048 || len(identity.Subject) > 512 {
		return principal, domain.ErrUnauthenticated
	}
	err := tx.QueryRow(ctx, `SELECT id,kind,active FROM access_principals WHERE issuer=$1 AND subject=$2 FOR SHARE`, identity.Issuer, identity.Subject).Scan(&principal.ID, &principal.Kind, &active)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !active {
		return principal, domain.ErrUnauthenticated
	}
	if err != nil {
		return principal, fmt.Errorf("resolve principal: %w", err)
	}
	return principal, nil
}
func (p *Postgres) BeginAccess(ctx context.Context, request domain.AccessRequest) (service.AccessTransaction, error) {
	if domain.ValidateAccessID(request.WorkspaceID) != nil {
		return nil, domain.ErrInvalidInput
	}
	if request.RepositoryID != "" && domain.ValidateAccessID(request.RepositoryID) != nil {
		return nil, domain.ErrInvalidInput
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = tx.Rollback(ctx)
		}
	}()
	principal, err := principalInTransaction(ctx, tx, request.Identity)
	if err != nil {
		return nil, err
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT active FROM workspace_memberships WHERE workspace_id=$1 AND principal_id=$2 FOR SHARE`, request.WorkspaceID, principal.ID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !active {
		return nil, domain.ErrForbidden
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace membership: %w", err)
	}
	success = true
	return &Postgres{pool: p.pool, tx: tx, access: &transactionAccess{request: request, principal: principal}}, nil
}

func (p *Postgres) authorizeRepository(ctx context.Context, q queryExecutor, repositoryID, action string) error {
	if p.access == nil {
		return domain.ErrForbidden
	}
	var read, author, approve bool
	err := q.QueryRow(ctx, `SELECT g.can_read,g.can_author,g.can_approve FROM managed_repositories r JOIN repository_grants g ON g.repository_id=r.id WHERE r.id=$1 AND r.workspace_id=$2 AND g.principal_id=$3 FOR SHARE OF r,g`, repositoryID, p.access.request.WorkspaceID, p.access.principal.ID).Scan(&read, &author, &approve)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !read {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("authorize repository: %w", err)
	}
	return domain.ValidateRepositoryAction(p.access.principal, read, author, approve, action)
}
func (p *Postgres) authorizeChange(ctx context.Context, q queryExecutor, id, action string) error {
	var workspaceID, repositoryID *string
	err := q.QueryRow(ctx, `SELECT workspace_id,repository_id FROM changes WHERE id=$1`, id).Scan(&workspaceID, &repositoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("read package ownership: %w", err)
	}
	if p.access == nil {
		if workspaceID != nil || repositoryID != nil {
			return domain.ErrNotFound
		}
		return nil
	}
	if workspaceID == nil || repositoryID == nil || *workspaceID != p.access.request.WorkspaceID {
		return domain.ErrNotFound
	}
	if selected := p.access.request.RepositoryID; selected != "" && selected != *repositoryID {
		return domain.ErrNotFound
	}
	return p.authorizeRepository(ctx, q, *repositoryID, action)
}

func (p *Postgres) Session(ctx context.Context, request domain.AccessRequest) (domain.Session, error) {
	session := domain.Session{Workspaces: make([]domain.Workspace, 0)}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return session, err
	}
	defer tx.Rollback(ctx)
	session.Principal, err = principalInTransaction(ctx, tx, request.Identity)
	if err != nil {
		return session, err
	}
	rows, err := tx.Query(ctx, `SELECT w.id,w.name FROM workspaces w JOIN workspace_memberships m ON m.workspace_id=w.id WHERE m.principal_id=$1 AND m.active ORDER BY w.id LIMIT $2`, session.Principal.ID, domain.MaxAccessPageSize+1)
	if err != nil {
		return session, err
	}
	defer rows.Close()
	for rows.Next() {
		var workspace domain.Workspace
		if err = rows.Scan(&workspace.ID, &workspace.Name); err != nil {
			return session, err
		}
		if len(session.Workspaces) == domain.MaxAccessPageSize {
			session.Truncated = true
			break
		}
		session.Workspaces = append(session.Workspaces, workspace)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return session, err
	}
	return session, tx.Commit(ctx)
}
func (p *Postgres) Repositories(ctx context.Context, request domain.AccessRequest) (domain.RepositoryPage, error) {
	page := domain.RepositoryPage{Repositories: make([]domain.ManagedRepository, 0)}
	gate, err := p.BeginAccess(ctx, request)
	if err != nil {
		return page, err
	}
	defer gate.Rollback(ctx)
	scoped := gate.(*Postgres)
	rows, err := scoped.tx.Query(ctx, `SELECT r.id,r.workspace_id,r.provider,r.host,r.provider_id,r.name,g.can_read,g.can_author,g.can_approve FROM managed_repositories r JOIN repository_grants g ON g.repository_id=r.id WHERE r.workspace_id=$1 AND g.principal_id=$2 AND g.can_read ORDER BY r.id LIMIT $3`, request.WorkspaceID, scoped.Principal().ID, domain.MaxAccessPageSize+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var r domain.ManagedRepository
		if err = rows.Scan(&r.ID, &r.WorkspaceID, &r.Provider, &r.Host, &r.ProviderID, &r.Name, &r.CanRead, &r.CanAuthor, &r.CanApprove); err != nil {
			return page, err
		}
		if len(page.Repositories) == domain.MaxAccessPageSize {
			page.Truncated = true
			break
		}
		r.CanApprove = r.CanApprove && scoped.Principal().Kind == "human"
		page.Repositories = append(page.Repositories, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return page, err
	}
	return page, gate.Commit(ctx)
}

// ApplyAccessConfig is a trusted operator path. Omitted records are unchanged;
// false active/capability values revoke access without erasing attribution.
func (p *Postgres) ApplyAccessConfig(ctx context.Context, operator string, config domain.AccessConfig) error {
	if err := domain.ValidateActor(operator); err != nil {
		return err
	}
	if err := domain.ValidateAccessConfig(config); err != nil {
		return err
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, v := range config.Principals {
		tag, e := tx.Exec(ctx, `INSERT INTO access_principals(id,issuer,subject,kind,active) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET active=excluded.active WHERE access_principals.issuer=excluded.issuer AND access_principals.subject=excluded.subject AND access_principals.kind=excluded.kind`, v.ID, v.Issuer, v.Subject, v.Kind, v.Active)
		if e != nil {
			return fmt.Errorf("provision principal: %w", e)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("%w: principal identity is immutable", domain.ErrInvalidInput)
		}
	}
	for _, v := range config.Workspaces {
		if _, err = tx.Exec(ctx, `INSERT INTO workspaces(id,name) VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET name=excluded.name`, v.ID, v.Name); err != nil {
			return fmt.Errorf("provision workspace: %w", err)
		}
	}
	for _, v := range config.Memberships {
		if _, err = tx.Exec(ctx, `INSERT INTO workspace_memberships(workspace_id,principal_id,active) VALUES($1,$2,$3) ON CONFLICT(workspace_id,principal_id) DO UPDATE SET active=excluded.active`, v.WorkspaceID, v.PrincipalID, v.Active); err != nil {
			return fmt.Errorf("provision membership: %w", err)
		}
	}
	for _, v := range config.Repositories {
		host, _ := domain.NormalizeRepositoryHost(v.Host)
		tag, e := tx.Exec(ctx, `INSERT INTO managed_repositories(id,workspace_id,provider,host,provider_id,name) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(id) DO UPDATE SET name=excluded.name WHERE managed_repositories.workspace_id=excluded.workspace_id AND managed_repositories.provider=excluded.provider AND managed_repositories.host=excluded.host AND managed_repositories.provider_id=excluded.provider_id`, v.ID, v.WorkspaceID, v.Provider, host, v.ProviderID, v.Name)
		if e != nil {
			return fmt.Errorf("provision repository: %w", e)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("%w: repository identity is immutable", domain.ErrInvalidInput)
		}
	}
	for _, v := range config.Grants {
		if _, err = tx.Exec(ctx, `INSERT INTO repository_grants(repository_id,principal_id,can_read,can_author,can_approve) VALUES($1,$2,$3,$4,$5) ON CONFLICT(repository_id,principal_id) DO UPDATE SET can_read=excluded.can_read,can_author=excluded.can_author,can_approve=excluded.can_approve`, v.RepositoryID, v.PrincipalID, v.CanRead, v.CanAuthor, v.CanApprove); err != nil {
			return fmt.Errorf("provision repository grant: %w", err)
		}
	}
	for _, v := range config.ContextIntegrations {
		if err = p.applyContextIntegration(ctx, tx, v); err != nil {
			return fmt.Errorf("configure context integration: %w", err)
		}
	}
	for _, v := range config.ExecutionGrants {
		if _, err = tx.Exec(ctx, `INSERT INTO execution_grants(repository_id,principal_id,can_execute,can_publish) VALUES($1,$2,$3,$4) ON CONFLICT(repository_id,principal_id) DO UPDATE SET can_execute=excluded.can_execute,can_publish=excluded.can_publish`, v.RepositoryID, v.PrincipalID, v.CanExecute, v.CanPublish); err != nil {
			return fmt.Errorf("provision execution grant: %w", err)
		}
	}
	for _, v := range config.ExecutionProfiles {
		profile, digest, err := ValidatedExecutionProfile(v.Profile)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO execution_profiles(workspace_id,id,profile_digest,image,configuration,enabled) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(workspace_id,id) DO UPDATE SET profile_digest=excluded.profile_digest,image=excluded.image,configuration=excluded.configuration,enabled=excluded.enabled`, v.WorkspaceID, v.ID, digest, v.Image, profile, v.Enabled); err != nil {
			return fmt.Errorf("provision execution profile: %w", err)
		}
	}
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO access_audit_events(operator,event_type,data) VALUES($1,'access.configured',$2)`, operator, data); err != nil {
		return fmt.Errorf("audit access configuration: %w", err)
	}
	return tx.Commit(ctx)
}
