package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

func graphReceipt(t *testing.T, ctx context.Context, p *Postgres, s *service.AuthenticatedService, repo, key string, files map[string]string) domain.GraphSource {
	t.Helper()
	selected := accessContext(ctx, "reviewer", "workspace-one", repo)
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	c, err := s.CreateCollection(selected, key, domain.CollectionInput{Commit: collectionCommit, Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	work := collectionWork(t, ctx, p, c.ID)
	var artifacts []domain.ContextArtifact
	for _, path := range c.Input.Paths {
		a := collectionArtifacts(files[path])[0]
		a.Path = path
		artifacts = append(artifacts, a)
	}
	receipt, err := p.CompleteCollection(ctx, c.ID, work.Binding, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	return domain.GraphSource{RepositoryID: repo, CollectionID: c.ID, Digest: receipt.Digest}
}
func graphFixture(t *testing.T) (context.Context, *Postgres, *service.AuthenticatedService, domain.GraphInput) {
	t.Helper()
	ctx, p, s := collectionStore(t)
	if err := p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-reviewer", CanRead: true, CanAuthor: true, CanApprove: true}, {RepositoryID: "repo-two", PrincipalID: "human-reviewer", CanRead: true, CanAuthor: true, CanApprove: true}}}); err != nil {
		t.Fatal(err)
	}
	a := graphReceipt(t, ctx, p, s, "repo-one", "graph-service", map[string]string{"go.mod": "module synthetic/service\n\ngo 1.24\nrequire synthetic/library v1.0.0\n", "service.go": "package service\nimport \"synthetic/library\"\nfunc Execute(){library.Run()}"})
	b := graphReceipt(t, ctx, p, s, "repo-two", "graph-library", map[string]string{"go.mod": "module synthetic/library\n\ngo 1.24\n", "library.go": "package library\nfunc Run(){}"})
	return ctx, p, s, domain.GraphInput{Sources: []domain.GraphSource{a, b}}
}
func TestRepositoryGraphSharedReceiptProvenanceAuthorizationAndRevocation(t *testing.T) {
	ctx, p, s, input := graphFixture(t)
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	g, err := s.CreateRepositoryGraph(reviewer, "graph-create", input)
	if err != nil {
		t.Fatal(err)
	}
	if g.CreatorID != "human-reviewer" || len(g.Snapshot.Sources) != 2 {
		t.Fatalf("graph ownership: %+v", g)
	}
	sum, _ := domain.JSONDigest(g.Snapshot)
	if sum != g.Digest {
		t.Fatal("snapshot digest mismatch")
	}
	again, err := s.CreateRepositoryGraph(reviewer, "graph-create", domain.GraphInput{Sources: []domain.GraphSource{input.Sources[1], input.Sources[0]}})
	if err != nil || !reflect.DeepEqual(g, again) {
		t.Fatalf("canonical retry: %+v %v", again, err)
	}
	read := accessContext(ctx, "reader", "workspace-one", "repo-one")
	if _, err = s.GetRepositoryGraph(read, g.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("hidden endpoint leaked: %v", err)
	}
	page, err := s.ListRepositoryGraphs(read, "", 1)
	if err != nil || len(page.Graphs) != 0 || page.NextBefore != "" {
		t.Fatalf("hidden graph pagination: %+v %v", page, err)
	}
	if _, err = s.QueryRepositoryGraph(read, g.ID, domain.GraphQuery{Search: "Run", Limit: 10}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("hidden search: %v", err)
	}
	if _, err = s.CreateRepositoryGraph(read, "reader-create", input); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("reader authored: %v", err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-two", PrincipalID: "human-reader", CanRead: true}}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRepositoryGraph(read, g.ID)
	if err != nil || got.Digest != g.Digest {
		t.Fatalf("shared data: %+v %v", got, err)
	}
	if _, err = s.GetRepositoryGraph(accessContext(ctx, "reviewer", "workspace-two", "repo-other-workspace"), g.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("workspace isolation: %v", err)
	}
	forged := domain.GraphInput{Sources: append([]domain.GraphSource(nil), input.Sources...)}
	forged.Sources[0].Digest = strings.Repeat("f", 64)
	if _, err = s.CreateRepositoryGraph(reviewer, "forged-source", forged); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("forged receipt: %v", err)
	}
	if _, err = s.CreateRepositoryGraph(reviewer, "graph-create", forged); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("changed input/key: %v", err)
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-two", PrincipalID: "human-reader", CanRead: false}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetRepositoryGraph(read, g.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked relation visible: %v", err)
	}
	var count int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM repository_graph_audit_events WHERE graph_id=$1`, g.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit: %d %v", count, err)
	}
	for _, table := range []string{"repository_graphs", "repository_graph_sources", "repository_graph_audit_events"} {
		if _, err = p.pool.Exec(ctx, `DELETE FROM `+table); err == nil {
			t.Fatalf("mutable graph fact: %s", table)
		}
	}
}
func TestRepositoryGraphConcurrentRetryAndAuditRollback(t *testing.T) {
	ctx, p, s, input := graphFixture(t)
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g, err := s.CreateRepositoryGraph(reviewer, "concurrent", input)
			ids <- g.ID
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for got := range ids {
		if id != "" && id != got {
			t.Fatal("concurrent idempotency forked graph")
		}
		id = got
	}
	if _, err := p.pool.Exec(ctx, `CREATE FUNCTION reject_graph_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic graph audit failure'; END $$; CREATE TRIGGER reject_graph_audit BEFORE INSERT ON repository_graph_audit_events FOR EACH ROW EXECUTE FUNCTION reject_graph_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRepositoryGraph(reviewer, "must-rollback", input); err == nil {
		t.Fatal("audit failure accepted")
	}
	var count int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM repository_graphs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit rollback: %d %v", count, err)
	}
}

func TestCodeGraphIndexAndReceiptCommitTogether(t *testing.T) {
	ctx, p, s := collectionStore(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	c := collectionRequest(t, author, s, "atomic-codegraph")
	work := collectionWork(t, ctx, p, c.ID)
	artifacts := collectionArtifacts("synthetic source")
	index := domain.CodeGraphIndex{Indexer: domain.CodeGraphIndexer, KernelVersion: "0.1.0", Files: []domain.CodeGraphFile{{Path: "source.txt", State: "unsupported_or_deferred"}}, Nodes: []domain.CodeGraphNode{}, Edges: []domain.CodeGraphEdge{}}
	if _, err := p.pool.Exec(ctx, `CREATE FUNCTION reject_codegraph_index() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic index failure'; END $$; CREATE TRIGGER reject_codegraph_index BEFORE INSERT ON context_codegraph_indexes FOR EACH ROW EXECUTE FUNCTION reject_codegraph_index()`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.CompleteCollectionWithCodeGraph(ctx, c.ID, work.Binding, artifacts, index); err == nil {
		t.Fatal("index failure accepted")
	}
	var count int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM context_receipts WHERE collection_id=$1`, c.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("receipt escaped index rollback: %d %v", count, err)
	}
	if _, err := p.pool.Exec(ctx, `DROP TRIGGER reject_codegraph_index ON context_codegraph_indexes`); err != nil {
		t.Fatal(err)
	}
	receipt, err := p.CompleteCollectionWithCodeGraph(ctx, c.ID, work.Binding, artifacts, index)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM context_codegraph_indexes WHERE collection_id=$1 AND receipt_digest=$2`, c.ID, receipt.Digest).Scan(&count); err != nil || count != 1 {
		t.Fatalf("atomic index missing: %d %v", count, err)
	}
	if _, err = p.pool.Exec(ctx, `DELETE FROM context_codegraph_indexes`); err == nil {
		t.Fatal("mutable CodeGraph index")
	}
}
