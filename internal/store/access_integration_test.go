package store

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

func syntheticAccessConfig() domain.AccessConfig {
	return domain.AccessConfig{
		Principals: []domain.PrincipalConfig{
			{ID: "human-author", Issuer: "https://identity.example.test", Subject: "author", Kind: "human", Active: true},
			{ID: "human-reviewer", Issuer: "https://identity.example.test", Subject: "reviewer", Kind: "human", Active: true},
			{ID: "agent-worker", Issuer: "https://identity.example.test", Subject: "worker", Kind: "agent", Active: true},
		},
		Workspaces: []domain.WorkspaceConfig{{ID: "workspace-one", Name: "Synthetic shared workspace"}, {ID: "workspace-two", Name: "Isolated workspace"}},
		Memberships: []domain.MembershipConfig{
			{WorkspaceID: "workspace-one", PrincipalID: "human-author", Active: true},
			{WorkspaceID: "workspace-one", PrincipalID: "human-reviewer", Active: true},
			{WorkspaceID: "workspace-one", PrincipalID: "agent-worker", Active: true},
			{WorkspaceID: "workspace-two", PrincipalID: "human-reviewer", Active: true},
		},
		Repositories: []domain.RepositoryConfig{
			{ID: "repo-one", WorkspaceID: "workspace-one", Provider: "github", Host: "GitHub.Example.Test", ProviderID: "42", Name: "synthetic/shared"},
			{ID: "repo-two", WorkspaceID: "workspace-one", Provider: "gitlab", Host: "gitlab.example.test", ProviderID: "42", Name: "synthetic/private"},
			{ID: "repo-other-workspace", WorkspaceID: "workspace-two", Provider: "github", Host: "github.example.test", ProviderID: "42", Name: "synthetic/shared"},
		},
		Grants: []domain.GrantConfig{
			{RepositoryID: "repo-one", PrincipalID: "human-author", CanRead: true, CanAuthor: true},
			{RepositoryID: "repo-one", PrincipalID: "human-reviewer", CanRead: true, CanAuthor: true, CanApprove: true},
			{RepositoryID: "repo-one", PrincipalID: "agent-worker", CanRead: true, CanAuthor: true, CanApprove: true},
			{RepositoryID: "repo-two", PrincipalID: "human-reviewer", CanRead: true, CanAuthor: true, CanApprove: true},
			{RepositoryID: "repo-other-workspace", PrincipalID: "human-reviewer", CanRead: true, CanAuthor: true, CanApprove: true},
		},
	}
}
func accessContext(ctx context.Context, subject, workspace, repository string) context.Context {
	return domain.WithAccess(ctx, domain.AccessRequest{Identity: domain.AccessIdentity{Issuer: "https://identity.example.test", Subject: subject}, WorkspaceID: workspace, RepositoryID: repository})
}
func provisionAccess(t *testing.T, ctx context.Context, p *Postgres) {
	t.Helper()
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", syntheticAccessConfig()); err != nil {
		t.Fatal(err)
	}
}

func TestAuthenticatedStoreIsolationAndAttribution(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	provisionAccess(t, ctx, p)
	local := service.New(p)
	secure := service.NewAuthenticated(p)
	legacy := integrationCreate(t, ctx, local)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	agent := accessContext(ctx, "worker", "workspace-one", "repo-one")
	created, err := secure.Create(author, "client-forged-actor", domain.Content{"intent": "shared synthetic design", "repository": "label does not confer authority"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision.Author != "human-author" || created.WorkspaceID != "workspace-one" || created.RepositoryID != "repo-one" {
		t.Fatalf("server attribution/ownership lost: %+v", created)
	}
	if _, err = secure.Submit(author, created.ID, "human-reviewer", 1); err != nil {
		t.Fatal(err)
	}
	if _, err = secure.Approve(agent, created.ID, "human-reviewer", 1, created.Revision.Digest); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agent approval accepted: %v", err)
	}
	approved, err := secure.Approve(reviewer, created.ID, "client-forged-actor", 1, created.Revision.Digest)
	if err != nil || approved.Approval == nil || approved.Approval.Reviewer != "human-reviewer" {
		t.Fatalf("server reviewer attribution: %+v %v", approved, err)
	}
	checks := []struct {
		name string
		run  func() error
	}{
		{"local get shared", func() error { _, e := local.Get(ctx, created.ID); return e }},
		{"local revise shared", func() error {
			_, e := local.Revise(ctx, created.ID, "human-author", 1, domain.Content{"intent": "forged edit"})
			return e
		}},
		{"local history shared", func() error { _, e := local.History(ctx, created.ID, 0, 20); return e }},
		{"local historical revision shared", func() error { _, e := local.Revision(ctx, created.ID, 1); return e }},
		{"local audit shared", func() error { _, e := local.Events(ctx, created.ID, 0, 20); return e }},
		{"shared get legacy", func() error { _, e := secure.Get(author, legacy.ID); return e }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("isolation failure: %v", err)
			}
		})
	}
	localPage, err := local.List(ctx, "", "", 20)
	if err != nil || len(localPage.Changes) != 1 || localPage.Changes[0].ID != legacy.ID {
		t.Fatalf("local discovery leaked: %+v %v", localPage, err)
	}
	sharedPage, err := secure.List(author, "", "", 20)
	if err != nil || len(sharedPage.Changes) != 1 || sharedPage.Changes[0].ID != created.ID {
		t.Fatalf("shared discovery leaked: %+v %v", sharedPage, err)
	}
	private, err := secure.Create(accessContext(ctx, "reviewer", "workspace-one", "repo-two"), "ignored", domain.Content{"intent": "private repository design"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = secure.Get(author, private.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("private repository visible: %v", err)
	}
	if _, err = secure.Get(accessContext(ctx, "reviewer", "workspace-two", ""), created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-workspace package visible: %v", err)
	}
	repos, err := secure.Repositories(agent)
	if err != nil || len(repos.Repositories) != 1 || repos.Repositories[0].CanApprove || repos.Repositories[0].Host != "github.example.test" {
		t.Fatalf("repository capabilities/identity: %+v %v", repos, err)
	}
	retained, err := local.Get(ctx, legacy.ID)
	if err != nil || retained.Revision.Digest != legacy.Revision.Digest || retained.WorkspaceID != "" {
		t.Fatalf("legacy record changed: %+v %v", retained, err)
	}
}

func TestAccessCommandHoldsPermissionsUntilCommit(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	provisionAccess(t, ctx, p)
	request, _ := domain.AccessFromContext(accessContext(ctx, "author", "workspace-one", "repo-one"))
	transaction, err := p.BeginAccess(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback(ctx)
	created, err := service.New(transaction).Create(ctx, transaction.Principal().ID, domain.Content{"intent": "permission locks cover command commit"})
	if err != nil {
		t.Fatal(err)
	}
	revoke := domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-author", CanRead: true}}}
	// The completed inner command is still inside the authorization transaction.
	// An operator cannot revoke its grant between that check and the final commit.
	limited, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	err = p.ApplyAccessConfig(limited, "synthetic-operator", revoke)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("grant update did not wait for command commit: %v", err)
	}
	if err = transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", revoke); err != nil {
		t.Fatal(err)
	}
	secure := service.NewAuthenticated(p)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	if _, err = secure.Revise(author, created.ID, "ignored", 1, domain.Content{"intent": "must be denied after revocation"}); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("revoked author capability used: %v", err)
	}
	current, err := secure.Get(author, created.ID)
	if err != nil || current.Revision.Number != 1 {
		t.Fatalf("denied mutation changed content: %+v %v", current, err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Memberships: []domain.MembershipConfig{{WorkspaceID: "workspace-one", PrincipalID: "human-author", Active: false}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = secure.Get(author, created.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("revoked workspace membership used: %v", err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Principals: []domain.PrincipalConfig{{ID: "human-author", Issuer: request.Identity.Issuer, Subject: "author", Kind: "human", Active: false}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = secure.Session(author); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatalf("inactive principal authenticated: %v", err)
	}
}

func TestAccessConfigurationRollbackAndImmutableIdentity(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	provisionAccess(t, ctx, p)
	var initialAudit int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM access_audit_events`).Scan(&initialAudit); err != nil {
		t.Fatal(err)
	}
	changed := domain.AccessConfig{Workspaces: []domain.WorkspaceConfig{{ID: "should-rollback", Name: "Must not commit"}}, Repositories: []domain.RepositoryConfig{{ID: "repo-one", WorkspaceID: "workspace-one", Provider: "github", Host: "github.example.test", ProviderID: "different-provider-id", Name: "Changed"}}}
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", changed); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("canonical identity changed: %v", err)
	}
	var leaked, audits int
	if err := p.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM workspaces WHERE id='should-rollback'),(SELECT count(*) FROM access_audit_events)`).Scan(&leaked, &audits); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 || audits != initialAudit {
		t.Fatalf("failed provisioning partially committed: workspace=%d audit=%d", leaked, audits)
	}
	if _, err := p.pool.Exec(ctx, `UPDATE access_principals SET kind='human' WHERE id='agent-worker'`); err == nil {
		t.Fatal("database allowed agent to become human")
	}
	if _, err := p.pool.Exec(ctx, `UPDATE managed_repositories SET host='other.example.test' WHERE id='repo-one'`); err == nil {
		t.Fatal("database allowed canonical identity reassignment")
	}
	secure := service.NewAuthenticated(p)
	created, err := secure.Create(accessContext(ctx, "author", "workspace-one", "repo-one"), "ignored", domain.Content{"intent": "immutable ownership"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE changes SET repository_id='repo-two' WHERE id=$1`, created.ID); err == nil {
		t.Fatal("database allowed package repository reassignment")
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO changes(id,workspace_id,repository_id) VALUES('invalid-composite-scope','workspace-two','repo-one')`); err == nil {
		t.Fatal("database allowed cross-workspace repository reference")
	}
	duplicate := domain.AccessConfig{Repositories: []domain.RepositoryConfig{{ID: "duplicate-canonical", WorkspaceID: "workspace-one", Provider: "github", Host: "GITHUB.EXAMPLE.TEST", ProviderID: "42", Name: "Another name"}}}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", duplicate); err == nil {
		t.Fatal("host case created duplicate canonical repository")
	}
	// The database audit failure must roll back a valid grant change too.
	if _, err = p.pool.Exec(ctx, `CREATE FUNCTION reject_access_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic access audit failure'; END $$; CREATE TRIGGER reject_access_audit BEFORE INSERT ON access_audit_events FOR EACH ROW EXECUTE FUNCTION reject_access_audit()`); err != nil {
		t.Fatal(err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-author"}}}); err == nil {
		t.Fatal("grant committed without access audit")
	}
	if _, err = secure.Get(accessContext(ctx, "author", "workspace-one", "repo-one"), created.ID); err != nil {
		t.Fatalf("failed audited revocation changed permissions: %v", err)
	}
}

func TestAccessMigrationPreservesLegacyReview(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	// Build a separate pre-authentication schema containing an approved package;
	// this exercises an actual 001 -> 002 upgrade rather than empty bootstrap.
	schema := p.pool.Config().ConnConfig.RuntimeParams["search_path"] + "_upgrade"
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := p.pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	config := p.pool.Config().Copy()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := p.pool.Exec(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop upgrade schema: %v", err)
		}
	})
	first, err := os.ReadFile("../../migrations/001_work_packages.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(first)); err != nil {
		t.Fatal(err)
	}
	content := domain.Content{"intent": "retain legacy review", "repository": "must not become authority", "futureExtension": map[string]any{"preserve": true}}
	digest, err := domain.Digest(content)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO changes(id,created_at) VALUES('legacy-review',$1)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO work_package_revisions(change_id,revision,schema_version,digest,content,author,created_at,submitted_at) VALUES('legacy-review',1,1,$1,$2,'legacy-author',$3,$3)`, digest, content, now); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO approvals(change_id,revision,digest,reviewer,created_at) VALUES('legacy-review',1,$1,'legacy-reviewer',$2)`, digest, now); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(change_id,event_type,actor,revision,data,created_at) VALUES('legacy-review','review.approved','legacy-reviewer',1,jsonb_build_object('digest',$1::text),$2)`, digest, now); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	next, err := os.ReadFile("../../migrations/002_access.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(next)); err != nil {
		t.Fatal(err)
	}
	upgraded := &Postgres{pool: pool}
	legacy, err := service.New(upgraded).Get(ctx, "legacy-review")
	if err != nil || legacy.WorkspaceID != "" || legacy.RepositoryID != "" || legacy.Revision.Digest != digest || legacy.Revision.Author != "legacy-author" || !legacy.Approved || legacy.Approval.Reviewer != "legacy-reviewer" || !reflect.DeepEqual(legacy.Revision.Content, content) {
		t.Fatalf("upgrade changed legacy content/authority: %+v %v", legacy, err)
	}
	events, err := upgraded.Events(ctx, legacy.ID, 0, 20)
	if err != nil || len(events.Events) != 1 || events.Events[0].Actor != "legacy-reviewer" || events.Events[0].Data["digest"] != digest {
		t.Fatalf("upgrade changed audit: %+v %v", events, err)
	}
	provisionAccess(t, ctx, upgraded)
	if _, err = service.NewAuthenticated(upgraded).Get(accessContext(ctx, "author", "workspace-one", "repo-one"), legacy.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("legacy package acquired shared authority: %v", err)
	}
}
