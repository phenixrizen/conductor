package store

import (
	"context"
	"errors"
	"github.com/phenixrizen/conductor/internal/execution"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

func coordinationFixture(t *testing.T) (context.Context, *Postgres, *service.AuthenticatedService, domain.CoordinationPlan) {
	t.Helper()
	ctx, p, s := collectionStore(t)
	input := domain.GraphInput{}
	s = s.WithCoordination()
	config := domain.AccessConfig{ExecutionProfiles: []domain.ExecutionProfileConfig{{WorkspaceID: "workspace-one", ID: "synthetic", Image: "sha256:" + strings.Repeat("a", 64), Profile: domain.WorkerProfile{Adapter: "command/v1", Command: []string{"sh", "-c", "printf new > src/app.txt"}}, Enabled: true}}, Grants: []domain.GrantConfig{
		{RepositoryID: "repo-one", PrincipalID: "human-reviewer", CanRead: true, CanAuthor: true, CanApprove: true},
		{RepositoryID: "repo-two", PrincipalID: "human-reviewer", CanRead: true, CanAuthor: true, CanApprove: true},
		{RepositoryID: "repo-two", PrincipalID: "human-author", CanRead: true, CanAuthor: true},
		{RepositoryID: "repo-two", PrincipalID: "agent-worker", CanRead: true, CanAuthor: true},
	}}
	for _, repo := range []string{"repo-one", "repo-two"} {
		for _, principal := range []string{"human-reviewer", "agent-worker"} {
			config.ExecutionGrants = append(config.ExecutionGrants, domain.ExecutionGrantConfig{RepositoryID: repo, PrincipalID: principal, CanExecute: true})
		}
	}
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", config); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"repo-one", "repo-two"} {
		input.Sources = append(input.Sources, coordinationFullReceipt(t, ctx, p, s, repo))
	}
	profileDigest, _ := domain.JSONDigest(execution.Profile{Adapter: "command/v1", Command: []string{"sh", "-c", "printf new > src/app.txt"}})
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	g, err := s.CreateRepositoryGraph(reviewer, "coordination-graph", input)
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.CoordinationPlan{SchemaVersion: 1, GraphID: g.ID, GraphDigest: g.Digest, MaxParallel: 2, Tasks: []domain.CoordinationTask{}}
	for i, source := range g.Snapshot.Sources {
		author := accessContext(ctx, "author", "workspace-one", source.RepositoryID)
		pkg, err := s.Create(author, "ignored", domain.Content{"intent": "Synthetic coordinated change"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Submit(author, pkg.ID, "ignored", 1); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Approve(accessContext(ctx, "reviewer", "workspace-one", source.RepositoryID), pkg.ID, "ignored", 1, pkg.Revision.Digest); err != nil {
			t.Fatal(err)
		}
		plan.Packages = append(plan.Packages, domain.PackagePin{ChangeID: pkg.ID, RepositoryID: source.RepositoryID, Revision: 1, Digest: pkg.Revision.Digest})
		plan.Repositories = append(plan.Repositories, domain.CoordinationRepository{RepositoryID: source.RepositoryID, Commit: source.Commit, CollectionID: source.CollectionID, ReceiptDigest: source.Digest, FullSourceDigest: source.FullSourceDigest})
		plan.Tasks = append(plan.Tasks, domain.CoordinationTask{ID: []string{"service", "library"}[i], Perspective: "developer", Profile: "synthetic", ProfileDigest: profileDigest, Image: "sha256:" + strings.Repeat("a", 64), Prompt: "Update synthetic source", Scopes: []domain.TaskScope{{RepositoryID: source.RepositoryID, WritablePaths: []string{"src"}}}, TimeoutSeconds: 30})
	}
	return ctx, p, s, plan
}

func TestCoordinationSharedIdentityExecutionAuthorityAndScope(t *testing.T) {
	ctx, p, s, plan := coordinationFixture(t)
	agent := accessContext(ctx, "worker", "workspace-one", "repo-one")
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	run, err := s.CreateCoordination(agent, "shared-proposal", plan)
	if err != nil {
		t.Fatal(err)
	}
	if run.ProposerID != "agent-worker" || run.Authorization != nil || run.Execution != nil || len(run.Receipts) != 0 {
		t.Fatalf("proposal claimed execution: %+v", run)
	}
	again, err := s.CreateCoordination(agent, "shared-proposal", plan)
	if err != nil || again.ID != run.ID {
		t.Fatalf("idempotency: %v", err)
	}
	changed := plan
	changed.MaxParallel = 1
	if _, err = s.CreateCoordination(agent, "shared-proposal", changed); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("key changed plan: %v", err)
	}
	if _, err = s.AuthorizeCoordination(agent, run.ID, run.Digest); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agent execution authority: %v", err)
	}
	if _, err = s.AuthorizeCoordination(reviewer, run.ID, strings.Repeat("0", 64)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("uninspected digest accepted: %v", err)
	}
	got, err := s.GetCoordination(reviewer, run.ID)
	if err != nil || got.Digest != run.Digest {
		t.Fatalf("shared plan: %v", err)
	}
	page, err := s.ListCoordinations(reviewer, "", 1)
	if err != nil || len(page.Runs) != 1 || page.Runs[0].ID != run.ID {
		t.Fatalf("shared page: %+v %v", page, err)
	}
	authorized, err := s.AuthorizeCoordination(reviewer, run.ID, run.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Authorization == nil || authorized.Authorization.Actor != "human-reviewer" || authorized.Execution != nil {
		t.Fatal("authorization is not execution observation")
	}
	if _, err = s.AuthorizeCoordination(reviewer, run.ID, run.Digest); err != nil {
		t.Fatal(err)
	}
	var authorizations, outbox, audits int
	if err = p.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM coordination_authorizations),(SELECT count(*) FROM coordination_outbox),(SELECT count(*) FROM coordination_audit_events WHERE event_type='run.authorized')`).Scan(&authorizations, &outbox, &audits); err != nil || authorizations != 1 || outbox != 1 || audits != 1 {
		t.Fatalf("duplicate authorization: %d %d %d %v", authorizations, outbox, audits, err)
	}
	for _, selected := range []context.Context{accessContext(ctx, "reviewer", "workspace-two", "repo-other-workspace"), accessContext(ctx, "reader", "workspace-one", "repo-one")} {
		if _, err = s.GetCoordination(selected, run.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("hidden plan visible: %v", err)
		}
	}
	if _, err = service.NewAuthenticated(p).CreateCoordination(reviewer, "disabled", plan); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("disabled server accepted: %v", err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-two", PrincipalID: "agent-worker", CanRead: false}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetCoordination(agent, run.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked source exposed: %v", err)
	}
	page, err = s.ListCoordinations(agent, "", 1)
	if err != nil || len(page.Runs) != 0 || page.NextBefore != "" {
		t.Fatalf("revoked pagination exposed: %+v %v", page, err)
	}
}

func TestCoordinationAdmissionSerializesConflictingWriters(t *testing.T) {
	ctx, p, s, plan := coordinationFixture(t)
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	a, err := s.CreateCoordination(reviewer, "first", plan)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateCoordination(reviewer, "second", plan)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		run domain.CoordinationRun
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, run := range []domain.CoordinationRun{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.AuthorizeCoordination(reviewer, run.ID, run.Digest)
			results <- result{got, err}
		}()
	}
	wg.Wait()
	close(results)
	var winner domain.CoordinationRun
	conflicts := 0
	for got := range results {
		if got.err == nil {
			if winner.ID != "" {
				t.Fatal("overlapping writers both admitted")
			}
			winner = got.run
		} else if errors.Is(got.err, domain.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(got.err)
		}
	}
	if winner.ID == "" || conflicts != 1 {
		t.Fatal("missing admitted writer or explicit conflict")
	}
	loser := a
	if loser.ID == winner.ID {
		loser = b
	}
	cancelled, err := s.CancelCoordination(reviewer, winner.ID, winner.Digest)
	if err != nil || cancelled.CancelRequestedAt == nil {
		t.Fatalf("cancel intent: %v", err)
	}
	if _, err = s.AuthorizeCoordination(reviewer, loser.ID, loser.Digest); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("cancel intent released active claim: %v", err)
	}
	var claims int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM coordination_claims WHERE released_at IS NULL`).Scan(&claims); err != nil || claims != 2 {
		t.Fatalf("claims: %d %v", claims, err)
	}
}

func TestCoordinationRequiresCurrentApprovedPinsAndExecutionGrant(t *testing.T) {
	ctx, p, s, plan := coordinationFixture(t)
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	run, err := s.CreateCoordination(reviewer, "stale", plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ExecutionGrants: []domain.ExecutionGrantConfig{{RepositoryID: "repo-two", PrincipalID: "human-reviewer", CanExecute: false}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeCoordination(reviewer, run.ID, run.Digest); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("revoked execution accepted: %v", err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ExecutionGrants: []domain.ExecutionGrantConfig{{RepositoryID: "repo-two", PrincipalID: "human-reviewer", CanExecute: true}}}); err != nil {
		t.Fatal(err)
	}
	pin := plan.Packages[1]
	if _, err = s.Revise(accessContext(ctx, "author", "workspace-one", pin.RepositoryID), pin.ChangeID, "ignored", 1, domain.Content{"intent": "A newer design"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthorizeCoordination(reviewer, run.ID, run.Digest); !errors.Is(err, domain.ErrStaleApproval) {
		t.Fatalf("stale plan authorized: %v", err)
	}
	var count int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM coordination_authorizations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale fact persisted: %d %v", count, err)
	}
}

func TestCoordinationAuditRollbackAndImmutableFacts(t *testing.T) {
	ctx, p, s, plan := coordinationFixture(t)
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	run, err := s.CreateCoordination(reviewer, "retained", plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `CREATE FUNCTION reject_coordination_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic audit failure'; END $$; CREATE TRIGGER reject_coordination_audit BEFORE INSERT ON coordination_audit_events FOR EACH ROW EXECUTE FUNCTION reject_coordination_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateCoordination(reviewer, "rollback-create", plan); err == nil {
		t.Fatal("audit failure accepted proposal")
	}
	if _, err = s.AuthorizeCoordination(reviewer, run.ID, run.Digest); err == nil {
		t.Fatal("audit failure accepted execution")
	}
	var runs, auth, claims, outbox int
	if err = p.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM coordination_runs),(SELECT count(*) FROM coordination_authorizations),(SELECT count(*) FROM coordination_claims),(SELECT count(*) FROM coordination_outbox)`).Scan(&runs, &auth, &claims, &outbox); err != nil || runs != 1 || auth != 0 || claims != 0 || outbox != 0 {
		t.Fatalf("partial commit: %d %d %d %d %v", runs, auth, claims, outbox, err)
	}
	for _, table := range []string{"coordination_runs", "coordination_repositories", "coordination_tasks", "coordination_audit_events"} {
		if _, err = p.pool.Exec(ctx, `DELETE FROM `+table); err == nil {
			t.Fatalf("mutable fact: %s", table)
		}
	}
}

func TestCoordinationExecutionGrantHeldThroughAuthorizationCommit(t *testing.T) {
	ctx, p, s, plan := coordinationFixture(t)
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	run, err := s.CreateCoordination(reviewer, "lock", plan)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := domain.AccessFromContext(reviewer)
	tx, err := p.BeginAccess(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	scoped := tx.(*Postgres)
	if _, err = scoped.AuthorizeCoordination(ctx, run.ID, run.Digest); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{ExecutionGrants: []domain.ExecutionGrantConfig{{RepositoryID: "repo-two", PrincipalID: "human-reviewer", CanExecute: false}}})
	}()
	select {
	case err := <-done:
		t.Fatalf("revocation escaped authority transaction: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err = s.CancelCoordination(reviewer, run.ID, run.Digest); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("revocation ineffective: %v", err)
	}
}

// These transaction fixtures contain an actual Git bundle and explicitly report
// text files as unsupported graph input. Native CodeGraph extraction is covered
// by the separate opted-in integration acceptance, never implied by this fixture.
func coordinationFullReceipt(t *testing.T, ctx context.Context, p *Postgres, s *service.AuthenticatedService, repo string) domain.GraphSource {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.invalid"}
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("synthetic Git: %v %s", err, b)
		}
		return strings.TrimSpace(string(b))
	}
	run("init", "--quiet", "--initial-branch=main")
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	text := "old\n"
	if err := os.WriteFile(filepath.Join(root, "src/app.txt"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	blob := run("hash-object", "src/app.txt")
	run("add", ".")
	run("commit", "--quiet", "-m", "Synthetic fixture")
	data := domain.SourceBundleData{Commit: run("rev-parse", "HEAD"), Tree: run("rev-parse", "HEAD^{tree}"), FileCount: 1, Artifacts: []domain.ContextArtifact{{Path: "src/app.txt", State: "collected", Text: &text, BlobOID: blob, Digest: execution.Sum([]byte(text))}}}
	name := filepath.Join(t.TempDir(), "source.bundle")
	run("bundle", "create", name, "--all")
	var err error
	data.Bundle, err = os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	data.Digest = execution.Sum(data.Bundle)
	c, err := s.CreateCollection(accessContext(ctx, "reviewer", "workspace-one", repo), "coordination-full-source", domain.CollectionInput{Commit: data.Commit, Paths: []string{"src/app.txt"}, FullSource: true})
	if err != nil {
		t.Fatal(err)
	}
	w := collectionWork(t, ctx, p, c.ID)
	index := domain.CodeGraphIndex{Indexer: domain.CodeGraphIndexer, KernelVersion: "0.1.0", Files: []domain.CodeGraphFile{{Path: "src/app.txt", State: "unsupported_or_deferred"}}, Nodes: []domain.CodeGraphNode{}, Edges: []domain.CodeGraphEdge{}}
	receipt, err := p.CompleteCollectionWithSource(ctx, c.ID, w.Binding, data.Artifacts, data, index)
	if err != nil {
		t.Fatal(err)
	}
	return domain.GraphSource{RepositoryID: repo, CollectionID: c.ID, Digest: receipt.Digest, FullSourceDigest: data.Digest}
}
