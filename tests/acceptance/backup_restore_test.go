package acceptance_test

import (
	"bytes"
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/phenixrizen/conductor/internal/databaseops"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOwnedBackupRestore(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BACKUP") != "1" {
		t.Skip("set CONDUCTOR_TEST_BACKUP=1 for owned PostgreSQL archive/restore acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	root, _ := filepath.Abs("../..")
	db := newRestartDatabase(t, ctx)
	admin, err := pgx.Connect(ctx, db.url)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(ctx)
	for _, name := range []string{"backup_source", "backup_restored", "backup_failed"} {
		if _, err = admin.Exec(ctx, `CREATE DATABASE `+name+` TEMPLATE template0`); err != nil {
			t.Fatal(err)
		}
	}
	sourceURL := strings.Replace(db.url, "/conductor?", "/backup_source?", 1)
	restoredURL := strings.Replace(db.url, "/conductor?", "/backup_restored?", 1)
	failedURL := strings.Replace(db.url, "/conductor?", "/backup_failed?", 1)
	migrations, err := databaseops.ReadMigrations(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if err = databaseops.Migrate(ctx, db.url, migrations, len(migrations), "synthetic-legacy-operator"); err != nil {
		t.Fatal("explicit legacy baseline", err)
	}
	if err = databaseops.Migrate(ctx, db.url, migrations, len(migrations), "synthetic-legacy-operator"); err == nil {
		t.Fatal("baseline replaced recorded history")
	}
	if err = databaseops.Migrate(ctx, sourceURL, migrations, 0, "synthetic-operator"); err != nil {
		t.Fatal(err)
	}
	if err = databaseops.Migrate(ctx, sourceURL, migrations, 0, "synthetic-operator"); err != nil {
		t.Fatal("idempotent migration", err)
	}
	bad := append([]databaseops.Migration{}, migrations...)
	bad[0].Digest = strings.Repeat("0", 64)
	if databaseops.Migrate(ctx, sourceURL, bad, 0, "synthetic-operator") == nil {
		t.Fatal("changed shipped migration accepted")
	}
	bad = append([]databaseops.Migration{}, migrations...)
	bad[1].SQL = "invalid SQL"
	if databaseops.Migrate(ctx, failedURL, bad, 0, "synthetic-operator") == nil {
		t.Fatal("bad migration succeeded")
	}
	failed, _ := pgx.Connect(ctx, failedURL)
	var clean bool
	if err = failed.QueryRow(ctx, `SELECT to_regclass('changes') IS NULL AND to_regclass('conductor_migrations') IS NULL`).Scan(&clean); err != nil || !clean {
		t.Fatal("migration rollback", err)
	}
	failed.Close(ctx)
	binary := filepath.Join(t.TempDir(), "conductord")
	runRestartCommand(t, ctx, 90*time.Second, root, nil, "go", "build", "-o", binary, "./cmd/conductord")
	server := startRestartServer(t, ctx, binary, sourceURL)
	author := reviewClient(t, server.url, "synthetic-author")
	reviewer := reviewClient(t, server.url, "synthetic-reviewer")
	pkg, err := author.Create(ctx, domain.Content{"intent": "Synthetic backup continuity", "repositoryContext": jsonObject(t, committedContext(t, ctx, "synthetic/restart-example")), "future": map[string]any{"retain": true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = author.Submit(ctx, pkg.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = reviewer.Approve(ctx, pkg.ID, 1, pkg.Revision.Digest); err != nil {
		t.Fatal(err)
	}
	dispatchBefore := seedBackupDispatch(t, ctx, sourceURL)
	assistanceBefore := seedBackupAssistance(t, ctx, sourceURL)
	before := captureRestartState(t, ctx, reviewer, pkg.ID)
	server.stop(t)
	operator := filepath.Join(t.TempDir(), "conductor-db")
	build := exec.CommandContext(ctx, "go", "build", "-o", operator, "./cmd/conductor-db")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, e := build.CombinedOutput(); e != nil {
		t.Fatal(string(b), e)
	}
	contents, _ := os.ReadFile(operator)
	runRestartCommand(t, ctx, 30*time.Second, "", bytes.NewReader(contents), "docker", "exec", "-i", db.name, "sh", "-c", "cat > /tmp/conductor-db && chmod 700 /tmp/conductor-db")
	insideURL := "postgres://conductor:conductor_restart_test@127.0.0.1:5432/backup_source?sslmode=disable"
	run := func(target string, args ...string) (string, error) {
		base := []string{"exec", "-e", "DATABASE_URL=" + target, db.name, "/tmp/conductor-db"}
		return restartCommand(ctx, 60*time.Second, "", nil, "docker", append(base, args...)...)
	}
	if out, e := run(insideURL, "backup", "--output", "/tmp/owned.dump"); e != nil {
		t.Fatal(out, e)
	}
	if _, e := run(insideURL, "backup", "--output", "/tmp/owned.dump"); e == nil {
		t.Fatal("backup overwrite accepted")
	}
	if _, e := run(insideURL, "restore", "--input", "/tmp/owned.dump"); e == nil {
		t.Fatal("nonempty restore accepted")
	}
	if out, e := run(strings.Replace(insideURL, "backup_source", "backup_restored", 1), "restore", "--input", "/tmp/owned.dump"); e != nil {
		t.Fatal(out, e)
	}
	restored := startRestartServer(t, ctx, binary, restoredURL)
	after := captureRestartState(t, ctx, reviewClient(t, restored.url, "synthetic-reviewer"), pkg.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("restored immutable package/history/approval/audit differ")
	}
	restored.stop(t)
	if afterDispatch := captureBackupDispatch(t, ctx, restoredURL); afterDispatch != dispatchBefore {
		t.Fatal("restore changed scoped request, audit, uncertain dispatch lease or Temporal target binding")
	}
	if afterAssistance := captureBackupAssistance(t, ctx, restoredURL); afterAssistance != assistanceBefore {
		t.Fatal("restore changed design assistance requests, suggestions, applications, command keys, revisions or audit")
	}
	target, _ := pgx.Connect(ctx, restoredURL)
	var count int
	err = target.QueryRow(ctx, `SELECT count(*) FROM conductor_migrations WHERE origin='executed'`).Scan(&count)
	target.Close(ctx)
	if err != nil || count != len(migrations) {
		t.Fatal("migration ledger not retained", err)
	}
	runRestartCommand(t, ctx, 10*time.Second, "", nil, "docker", "exec", db.name, "sh", "-c", "printf x >> /tmp/owned.dump")
	if _, e := run(strings.Replace(insideURL, "backup_source", "backup_failed", 1), "restore", "--input", "/tmp/owned.dump"); e == nil {
		t.Fatal("modified archive accepted")
	}
	t.Log("Real PostgreSQL17 pg_dump/pg_restore retained exact approval/history/audit and migration ledger plus immutable Design assistance facts/keys in a distinct empty database; failed migration, overwrites, nonempty restore and archive tampering rejected")
}

func seedBackupDispatch(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	p, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	cfg := domain.AccessConfig{Principals: []domain.PrincipalConfig{{ID: "backup-agent", Issuer: "https://backup.example.invalid", Subject: "agent", Kind: "agent", Active: true}}, Workspaces: []domain.WorkspaceConfig{{ID: "backup-workspace", Name: "Synthetic backup workspace"}}, Memberships: []domain.MembershipConfig{{WorkspaceID: "backup-workspace", PrincipalID: "backup-agent", Active: true}}, Repositories: []domain.RepositoryConfig{{ID: "backup-repository", WorkspaceID: "backup-workspace", Provider: "github", Host: "github.com", ProviderID: "123", Name: "synthetic/backup"}}, Grants: []domain.GrantConfig{{RepositoryID: "backup-repository", PrincipalID: "backup-agent", CanRead: true, CanAuthor: true}}, ContextIntegrations: []domain.ContextIntegrationConfig{{RepositoryID: "backup-repository", WorkspaceID: "backup-workspace", Profile: "github-rest/2026-03-10", Locator: "synthetic/backup", CredentialID: "synthetic-read", Enabled: true}}}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", cfg); err != nil {
		t.Fatal(err)
	}
	scoped := domain.WithAccess(ctx, domain.AccessRequest{Identity: domain.AccessIdentity{Issuer: "https://backup.example.invalid", Subject: "agent"}, WorkspaceID: "backup-workspace", RepositoryID: "backup-repository"})
	if _, err = service.NewAuthenticated(p).WithCollections().CreateCollection(scoped, "backup-context", domain.CollectionInput{Commit: strings.Repeat("a", 40), Paths: []string{"README.md"}}); err != nil {
		t.Fatal(err)
	}
	dispatch, err := p.ClaimContextDispatch(ctx, strings.Repeat("b", 64), "backup-namespace")
	if err != nil || dispatch == nil {
		t.Fatal("claim exact durable dispatch", err)
	}
	return captureBackupDispatch(t, ctx, url)
}
func captureBackupDispatch(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var value string
	err = conn.QueryRow(ctx, `SELECT jsonb_build_object('request',(SELECT to_jsonb(c) FROM context_collections c),'outbox',(SELECT to_jsonb(o) FROM context_outbox o),'binding',(SELECT to_jsonb(b) FROM context_runtime_bindings b),'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY sequence) FROM context_audit_events a))::text`).Scan(&value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func seedBackupAssistance(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	p, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	cfg := domain.AccessConfig{
		Principals:  []domain.PrincipalConfig{{ID: "backup-human", Issuer: "https://backup.example.invalid", Subject: "human", Kind: "human", Active: true}},
		Memberships: []domain.MembershipConfig{{WorkspaceID: "backup-workspace", PrincipalID: "backup-human", Active: true}},
		Grants:      []domain.GrantConfig{{RepositoryID: "backup-repository", PrincipalID: "backup-human", CanRead: true, CanAuthor: true}},
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", cfg); err != nil {
		t.Fatal(err)
	}
	access := domain.AccessRequest{Identity: domain.AccessIdentity{Issuer: "https://backup.example.invalid", Subject: "human"}, WorkspaceID: "backup-workspace", RepositoryID: "backup-repository"}
	human := domain.WithAccess(ctx, access)
	access.Identity.Subject = "agent"
	agent := domain.WithAccess(ctx, access)
	s := service.NewAuthenticated(p)
	pkg, err := s.Create(human, "ignored", domain.Content{"title": "Synthetic restored assistance", "intent": "Preserve exact facts", "design": "Before", "extension": map[string]any{"retained": true}})
	if err != nil {
		t.Fatal(err)
	}
	in := domain.AssistanceInput{ChangeID: pkg.ID, ExpectedRevision: 1, ExpectedDigest: pkg.Revision.Digest, Instruction: "Improve the design", Sections: []string{"design"}}
	request, err := s.RequestDesignAssistance(human, "backup-request", in)
	if err != nil {
		t.Fatal(err)
	}
	request, err = s.ProposeDesignSections(agent, request.ID, "backup-suggestion", domain.SuggestionInput{RequestDigest: request.Digest, Sections: map[string]string{"design": "After"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ApplyDesignSuggestion(human, request.ID, "backup-application", domain.ApplySuggestionInput{RequestDigest: request.Digest, SuggestionDigest: request.Suggestion.Digest, ExpectedRevision: 1, ExpectedDigest: pkg.Revision.Digest, Sections: []string{"design"}})
	if err != nil {
		t.Fatal(err)
	}
	return captureBackupAssistance(t, ctx, url)
}
func captureBackupAssistance(t *testing.T, ctx context.Context, url string) string {
	t.Helper()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var value string
	err = conn.QueryRow(ctx, `SELECT jsonb_build_object(
 'request',(SELECT to_jsonb(r) FROM design_assistance_requests r),
 'suggestion',(SELECT to_jsonb(s) FROM design_assistance_suggestions s),
 'application',(SELECT to_jsonb(a) FROM design_assistance_applications a),
 'commands',(SELECT jsonb_agg(to_jsonb(c) ORDER BY operation) FROM design_assistance_commands c),
 'revisions',(SELECT jsonb_agg(to_jsonb(r) ORDER BY r.revision) FROM work_package_revisions r JOIN design_assistance_requests d ON d.change_id=r.change_id),
 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY sequence) FROM audit_events a JOIN design_assistance_requests d ON d.change_id=a.change_id))::text`).Scan(&value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
