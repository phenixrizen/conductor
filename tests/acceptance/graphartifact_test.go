package acceptance_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

func TestGraphArtifactCrossRepositoryAuthorizationAndExactSource(t *testing.T) {
	f := collectionFixture(t)
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-source-repository", domain.AccessConfig{Repositories: []domain.RepositoryConfig{{ID: "library", WorkspaceID: "team", Provider: "github", Host: "github.com", ProviderID: "303", Name: "synthetic/library"}}, Grants: []domain.GrantConfig{{RepositoryID: "library", PrincipalID: "person-author", CanRead: true, CanAuthor: true}}}); err != nil {
		t.Fatal(err)
	}
	configureCollectionIntegration(t, f, "team", "library", true)
	sources := []domain.GraphSource{}
	for _, repo := range []string{"application", "library"} {
		collection := requestCollection(t, f, "author", "team", repo, "artifact-"+repo)
		receipt := completeCollectionFixture(t, f, collection)
		sources = append(sources, domain.GraphSource{RepositoryID: repo, CollectionID: collection.ID, Digest: receipt.Digest})
	}
	c, err := client.NewAuthenticated(f.server.URL, f.tokens["author"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := c.CreateRepositoryGraph(f.ctx, "cross-repo-artifact", domain.GraphInput{Sources: sources})
	if err != nil {
		t.Fatal(err)
	}
	q := domain.GraphArtifactQuery{GraphDigest: graph.Digest, RepositoryID: sources[1].RepositoryID, CollectionID: sources[1].CollectionID, ReceiptDigest: sources[1].Digest, Path: "README.md"}
	result, err := c.GetRepositoryGraphArtifact(f.ctx, graph.ID, q)
	if err != nil || result.Artifact.Text == nil || !strings.Contains(*result.Artifact.Text, "Synthetic shared source") || result.Source.RepositoryID != "library" || result.Source.Freshness != "unknown" || result.Coverage != "selected_paths" {
		t.Fatalf("cross-repository source: %+v %v", result, err)
	}
	for _, path := range []string{"docs/missing.md", "unretained.go"} {
		copy := q
		copy.Path = path
		v, err := c.GetRepositoryGraphArtifact(f.ctx, graph.ID, copy)
		if err != nil || v.Artifact.Text != nil || path == "docs/missing.md" && v.Artifact.State != "missing" || path == "unretained.go" && v.Artifact.State != "unavailable" {
			t.Fatalf("source gap disappeared: %+v %v", v, err)
		}
	}
	for _, change := range []func(*domain.GraphArtifactQuery){func(q *domain.GraphArtifactQuery) { q.GraphDigest = strings.Repeat("f", 64) }, func(q *domain.GraphArtifactQuery) { q.CollectionID = sources[0].CollectionID }, func(q *domain.GraphArtifactQuery) { q.FullSourceDigest = strings.Repeat("a", 64) }, func(q *domain.GraphArtifactQuery) { q.Path = "../README.md" }, func(q *domain.GraphArtifactQuery) { q.Path = strings.Repeat("x", 1025) }} {
		copy := q
		change(&copy)
		if _, err := c.GetRepositoryGraphArtifact(f.ctx, graph.ID, copy); err == nil {
			t.Fatal("accepted forged source tuple or path")
		}
	}
	values := url.Values{"graphDigest": {q.GraphDigest}, "repositoryId": {q.RepositoryID}, "collectionId": {q.CollectionID}, "receiptDigest": {q.ReceiptDigest}, "path": {q.Path}}
	endpoint := "/api/v1/repository-graphs/" + graph.ID + "/artifact?" + values.Encode()
	f.raw(f.server.URL, "forged.token", "team", "application", "GET", endpoint, nil, nil, 401, nil)
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "GET", endpoint, nil, http.Header{"X-Conductor-Actor": {"forged"}}, 401, nil)
	f.localRequest("author", "GET", endpoint, nil, 503, nil)
	f.request("author", "team", "", "GET", endpoint, nil, 400, nil)
	f.request("other", "other-team", "other-application", "GET", endpoint, nil, 404, nil)
	f.request("author", "team", "application", "GET", endpoint+"&path=README.md", nil, 400, nil)
	f.request("author", "team", "application", "GET", endpoint+"&token=forged", nil, 400, nil)
	if err = f.db.ApplyAccessConfig(f.ctx, "synthetic-artifact-revocation", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "library", PrincipalID: "person-author"}}}); err != nil {
		t.Fatal(err)
	}
	// Revoking any graph endpoint hides text even from a still-readable source.
	for _, source := range sources {
		copy := q
		copy.RepositoryID = source.RepositoryID
		copy.CollectionID = source.CollectionID
		copy.ReceiptDigest = source.Digest
		if _, err := c.GetRepositoryGraphArtifact(f.ctx, graph.ID, copy); err == nil {
			t.Fatal("source text leaked through a revoked graph endpoint")
		}
	}
}

// The exact Git bundle is real; the index fact is explicitly synthetic and marks
// all extraction unsupported. This test establishes artifact authorization and
// retention only, not successful CodeGraph extraction or provider compatibility.
func TestGraphArtifactFullSourceBeyondSelectedPaths(t *testing.T) {
	f := collectionFixture(t)
	source := bundleFixture(t)
	var collection domain.Collection
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", domain.CollectionInput{Commit: source.Commit, Paths: []string{"README.md"}, FullSource: true}, http.Header{"Idempotency-Key": {"artifact-full"}}, 202, &collection)
	var binding string
	if err := f.sql.QueryRow(f.ctx, `SELECT binding FROM context_collections WHERE id=$1`, collection.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	index := domain.CodeGraphIndex{Indexer: domain.CodeGraphIndexer, KernelVersion: "synthetic-retained-fixture"}
	for _, artifact := range source.Artifacts {
		index.Files = append(index.Files, domain.CodeGraphFile{Path: artifact.Path, State: "unsupported_or_deferred"})
	}
	receipt, err := f.db.CompleteCollectionWithSource(f.ctx, collection.ID, binding, source.Artifacts[:1], source, index)
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewAuthenticated(f.server.URL, f.tokens["author"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := c.CreateRepositoryGraph(f.ctx, "artifact-full-graph", domain.GraphInput{Sources: []domain.GraphSource{{RepositoryID: "application", CollectionID: collection.ID, Digest: receipt.Digest, FullSourceDigest: source.Digest}}})
	if err != nil {
		t.Fatal(err)
	}
	q := domain.GraphArtifactQuery{GraphDigest: graph.Digest, RepositoryID: "application", CollectionID: collection.ID, ReceiptDigest: receipt.Digest, FullSourceDigest: source.Digest, Path: "fixture.go"}
	result, err := c.GetRepositoryGraphArtifact(f.ctx, graph.ID, q)
	if err != nil || result.Artifact.Text == nil || !strings.Contains(*result.Artifact.Text, "func Caller()") || result.Coverage != "full_source" || result.Source.Commit != source.Commit {
		t.Fatalf("full source artifact: %+v %v", result, err)
	}
	q.FullSourceDigest = ""
	if _, err = c.GetRepositoryGraphArtifact(f.ctx, graph.ID, q); err == nil {
		t.Fatal("silently widened a selected-path source to full source")
	}
}

func TestGraphArtifactRetainedTextBound(t *testing.T) {
	f := collectionFixture(t)
	c, err := client.NewAuthenticated(f.server.URL, f.tokens["author"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{domain.MaxContextArtifactBytes, domain.MaxContextArtifactBytes + 1} {
		var collection domain.Collection
		key := "bounded-source"
		if size > domain.MaxContextArtifactBytes {
			key = "oversized-source"
		}
		f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", domain.CollectionInput{Commit: strings.Repeat("a", 40), Paths: []string{"large.txt"}}, http.Header{"Idempotency-Key": {key}}, 202, &collection)
		var binding string
		if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM context_collections WHERE id=$1`, collection.ID).Scan(&binding); err != nil {
			t.Fatal(err)
		}
		text := strings.Repeat("<", size)
		sum := sha256.Sum256([]byte(text))
		artifact := domain.ContextArtifact{Path: "large.txt", State: "collected", Text: &text, Digest: hex.EncodeToString(sum[:]), BlobOID: strings.Repeat("b", 40)}
		receipt, err := f.db.CompleteCollection(f.ctx, collection.ID, binding, []domain.ContextArtifact{artifact})
		if size > domain.MaxContextArtifactBytes {
			if err == nil {
				t.Fatal("retained oversized source")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		graph, err := c.CreateRepositoryGraph(f.ctx, key, domain.GraphInput{Sources: []domain.GraphSource{{RepositoryID: "application", CollectionID: collection.ID, Digest: receipt.Digest}}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := c.GetRepositoryGraphArtifact(f.ctx, graph.ID, domain.GraphArtifactQuery{GraphDigest: graph.Digest, RepositoryID: "application", CollectionID: collection.ID, ReceiptDigest: receipt.Digest, Path: "large.txt"})
		if err != nil || result.Artifact.Text == nil || *result.Artifact.Text != text {
			t.Fatal("bounded source lost original bytes", err)
		}
	}
}
