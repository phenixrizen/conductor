package repositorygraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/phenixrizen/conductor/internal/domain"
	"strings"
	"testing"
	"time"
)

func fixtureReceipt(repo, id string, files map[string]string) Receipt {
	s := domain.RepositoryContext{SchemaVersion: 2, Repository: "github:github.com:1", Commit: strings.Repeat("a", 40), RequestedRef: strings.Repeat("a", 40), CollectedAt: time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC), Collector: domain.RemoteContextCollector, CollectionID: strings.Repeat(id, 32), Source: &domain.ContextSource{WorkspaceID: "team", RepositoryID: repo, Provider: "github", Host: "github.com", ProviderID: "1", Locator: "synthetic/source", Profile: "github-rest/2026-03-10", IntegrationVersion: 1}}
	for p, text := range files {
		v := text
		sum := sha256.Sum256([]byte(text))
		s.Artifacts = append(s.Artifacts, domain.ContextArtifact{Path: p, State: "collected", BlobOID: strings.Repeat("b", 40), Digest: hex.EncodeToString(sum[:]), Text: &v})
	}
	digest, _ := domain.JSONDigest(s)
	return Receipt{Source: domain.GraphSource{RepositoryID: repo, CollectionID: s.CollectionID, Digest: digest}, Receipt: domain.CollectionReceipt{ID: s.CollectionID, Digest: digest, Snapshot: s, CreatedAt: s.CollectedAt}}
}
func TestBuildResolvesRepositoryDependenciesAndPreservesUnknowns(t *testing.T) {
	a := fixtureReceipt("service", "a", map[string]string{"go.mod": "module example.test/service\n\ngo 1.24\nrequire example.test/library v1.0.0\n", "main.go": "package service\nimport \"example.test/library\"\nfunc Execute(){library.Run()}\n", "README.md": "Synthetic documentation"})
	b := fixtureReceipt("library", "b", map[string]string{"go.mod": "module example.test/library\n\ngo 1.24\n", "library.go": "package library\nfunc Run(){}\n"})
	g, err := Build(context.Background(), []Receipt{a, b})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range g.Edges {
		if e.Kind == "depends_on" {
			found = true
		}
	}
	if !found || len(g.Gaps) == 0 || g.Sources[0].Freshness != "unknown" {
		t.Fatalf("dependency/coverage: %+v", g)
	}
	sum, _ := domain.JSONDigest(g)
	again, err := Build(context.Background(), []Receipt{a, b})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := domain.JSONDigest(again)
	if sum != got {
		t.Fatal("same receipts produced different graph")
	}
	query, err := domain.QueryGraph(domain.RepositoryGraph{Snapshot: g}, domain.GraphQuery{Search: "Execute", Depth: 1, Limit: 100})
	if err != nil || len(query.Nodes) < 2 {
		t.Fatalf("symbol traversal: %+v %v", query, err)
	}
	limited, err := domain.QueryGraph(domain.RepositoryGraph{Snapshot: g}, domain.GraphQuery{Limit: 1})
	if err != nil || !limited.Truncated || len(limited.Nodes) != 1 {
		t.Fatalf("bounded query: %+v %v", limited, err)
	}
}
func TestBuildRejectsForgedReceiptAndDoesNotExecuteSource(t *testing.T) {
	r := fixtureReceipt("service", "a", map[string]string{"main.go": "package service\nfunc Run(){}"})
	r.Receipt.Snapshot.Artifacts[0].Text = new(string)
	if _, err := Build(context.Background(), []Receipt{r}); err == nil {
		t.Fatal("forged source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, []Receipt{r}); err != context.Canceled {
		t.Fatalf("cancellation: %v", err)
	}
}
func TestBuildNPMAndAmbiguousTargets(t *testing.T) {
	a := fixtureReceipt("service", "a", map[string]string{"package.json": `{"name":"app","dependencies":{"shared":"1"}}`})
	b := fixtureReceipt("library", "b", map[string]string{"package.json": `{"name":"shared"}`})
	c := fixtureReceipt("duplicate", "c", map[string]string{"package.json": `{"name":"shared"}`})
	g, err := Build(context.Background(), []Receipt{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range g.Edges {
		if e.Kind == "depends_on" {
			t.Fatal("ambiguous declaration invented repository relation")
		}
	}
}
