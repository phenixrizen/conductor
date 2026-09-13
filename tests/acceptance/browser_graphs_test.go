package acceptance_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

// Native indexing and source acquisition have separate actual-process acceptance.
// These retained fixtures isolate browser commands, provenance and authorization.
func TestBrowserRepositoryGraphs(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for actual repository graph workbench acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	f := newConfiguredBrowserFixture(t, browserFixtureOptions{withUI: true, collections: true})
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections(), f.verifier))
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Repositories: []domain.RepositoryConfig{{ID: "library", WorkspaceID: "team", Provider: "gitlab", Host: "gitlab.com", ProviderID: "101", Name: "synthetic/library"}}, Grants: []domain.GrantConfig{
		{RepositoryID: "library", PrincipalID: "person-author", CanRead: true, CanAuthor: true},
		{RepositoryID: "library", PrincipalID: "person-reviewer", CanRead: true, CanAuthor: true},
		{RepositoryID: "library", PrincipalID: "person-reader", CanRead: true},
	}}); err != nil {
		t.Fatal(err)
	}
	data := bundleFixture(t)
	index := domain.CodeGraphIndex{Indexer: domain.CodeGraphIndexer, KernelVersion: "browser-fixture-unexecuted", Files: []domain.CodeGraphFile{}, Nodes: []domain.CodeGraphNode{}, Edges: []domain.CodeGraphEdge{}}
	for _, a := range data.Artifacts {
		index.Files = append(index.Files, domain.CodeGraphFile{Path: a.Path, State: "unsupported_or_deferred"})
	}
	var collections []domain.Collection
	var receipts []domain.CollectionReceipt
	for _, repo := range []string{"application", "library"} {
		profile, locator := "github-rest/2026-03-10", "synthetic/application"
		if repo == "library" {
			profile, locator = "gitlab-rest/v4-19.3", ""
		}
		if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{ContextIntegrations: []domain.ContextIntegrationConfig{{WorkspaceID: "team", RepositoryID: repo, Profile: profile, Locator: locator, CredentialID: "synthetic-read-credential", Enabled: true}}}); err != nil {
			t.Fatal(err)
		}
		var c domain.Collection
		f.raw(f.server.URL, f.tokens["author"], "team", repo, "POST", "/api/v1/context-collections", domain.CollectionInput{Commit: data.Commit, Paths: []string{"README.md"}, FullSource: true}, http.Header{"Idempotency-Key": {"browser-graph-" + repo}}, 202, &c)
		var binding string
		if err := f.sql.QueryRow(f.ctx, `SELECT binding FROM context_collections WHERE id=$1`, c.ID).Scan(&binding); err != nil {
			t.Fatal(err)
		}
		receipt, err := f.db.CompleteCollectionWithSource(f.ctx, c.ID, binding, data.Artifacts[:1], data, index)
		if err != nil {
			t.Fatal(err)
		}
		collections = append(collections, c)
		receipts = append(receipts, receipt)
	}
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/graphs.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_COLLECTION_ID="+collections[0].ID, "CONDUCTOR_BROWSER_OTHER_COLLECTION_ID="+collections[1].ID, "CONDUCTOR_BROWSER_RECEIPT_DIGEST="+receipts[0].Digest, "CONDUCTOR_BROWSER_OTHER_RECEIPT_DIGEST="+receipts[1].Digest, "CONDUCTOR_BROWSER_BUNDLE_DIGEST="+data.Digest, "CONDUCTOR_BROWSER_COMMIT="+data.Commit)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser graph acceptance: %v\n%s", err, output)
	}
	t.Log(string(output))
	var count int
	if err = f.sql.QueryRow(f.ctx, `SELECT count(*) FROM repository_graphs WHERE creator_id='person-reviewer'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("lost acknowledgement duplicated graph: %d", count)
	}
	var full bool
	if err = f.sql.QueryRow(f.ctx, `SELECT (input->>'fullSource')::boolean FROM context_collections WHERE requester_id='person-reviewer'`).Scan(&full); err != nil || !full {
		t.Fatalf("browser omitted full source intent: %v %v", full, err)
	}
}
