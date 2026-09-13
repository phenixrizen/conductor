package acceptance_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/phenixrizen/conductor/internal/codegraph"
	"github.com/phenixrizen/conductor/internal/collectionworker"
	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/repositorycontext/remote"
	"github.com/phenixrizen/conductor/pkg/client"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestAuthenticatedRepositoryGraphAPIAndSharedClient(t *testing.T) {
	f := collectionFixture(t)
	collection := requestCollection(t, f, "author", "team", "application", "graph-source")
	receipt := completeCollectionFixture(t, f, collection)
	c, err := client.NewAuthenticated(f.server.URL, f.tokens["author"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	input := domain.GraphInput{Sources: []domain.GraphSource{{RepositoryID: "application", CollectionID: collection.ID, Digest: receipt.Digest}}}
	graph, err := c.CreateRepositoryGraph(f.ctx, "graph-client", input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetRepositoryGraph(f.ctx, graph.ID)
	if err != nil || got.Digest != graph.Digest {
		t.Fatalf("shared graph: %+v %v", got, err)
	}
	page, err := c.ListRepositoryGraphs(f.ctx, "", 1)
	if err != nil || len(page.Graphs) != 1 {
		t.Fatalf("graph page: %+v %v", page, err)
	}
	query, err := c.QueryRepositoryGraph(f.ctx, graph.ID, domain.GraphQuery{Search: "README", Depth: 1, Limit: 10})
	if err != nil || len(query.Nodes) == 0 || len(query.Gaps) == 0 {
		t.Fatalf("graph query: %+v %v", query, err)
	}
	for _, endpoint := range []struct {
		method, path string
		body         any
	}{{"POST", "/api/v1/repository-graphs", input}, {"GET", "/api/v1/repository-graphs", nil}, {"GET", "/api/v1/repository-graphs/" + graph.ID, nil}, {"GET", "/api/v1/repository-graphs/" + graph.ID + "/query?limit=10", nil}} {
		f.raw(f.server.URL, "forged.token", "team", "application", endpoint.method, endpoint.path, endpoint.body, http.Header{"Idempotency-Key": {"reject"}}, 401, nil)
		f.raw(f.server.URL, f.tokens["author"], "team", "application", endpoint.method, endpoint.path, endpoint.body, http.Header{"Idempotency-Key": {"reject"}, "X-Conductor-Actor": {"fake"}}, 401, nil)
		f.localRequest("author", endpoint.method, endpoint.path, endpoint.body, 503, nil)
		f.raw(f.server.URL, f.tokens["author"], "team", "", endpoint.method, endpoint.path, endpoint.body, http.Header{"Idempotency-Key": {"reject"}}, 400, nil)
	}
	for _, q := range []string{"limit=101", "depth=6", "search=a&search=b", "nodeId=" + strings.Repeat("a", 64) + "&search=a", "unexpected=true"} {
		f.request("author", "team", "application", "GET", "/api/v1/repository-graphs/"+graph.ID+"/query?"+q, nil, 400, nil)
	}
	f.request("author", "team", "private", "GET", "/api/v1/repository-graphs/"+graph.ID, nil, 404, nil)
	f.raw(f.server.URL, f.tokens["reader"], "team", "application", "POST", "/api/v1/repository-graphs", input, http.Header{"Idempotency-Key": {"reader"}}, 403, nil)
}

// This uses the real, pinned native Rust parser in its network-disabled container
// and the actual activity/store command. Provider text is a controlled fixture;
// separate Temporal acceptance establishes workflow process recovery.
func TestCodeGraphActivityReceiptAndSharedGraph(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_CODEGRAPH") != "1" {
		t.Skip("set CONDUCTOR_TEST_CODEGRAPH=1 for actual native graph activity acceptance")
	}
	f := collectionFixture(t)
	source := "package fixture\nfunc Called(){}\nfunc Caller(){Called()}\n"
	digest := sha256.Sum256([]byte(source))
	artifacts := []domain.ContextArtifact{{Path: "fixture.go", State: "collected", Text: &source, Digest: hex.EncodeToString(digest[:]), BlobOID: strings.Repeat("b", 40)}}
	var collection domain.Collection
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", domain.CollectionInput{Commit: strings.Repeat("a", 40), Paths: []string{"fixture.go"}}, http.Header{"Idempotency-Key": {"real-codegraph"}}, 202, &collection)
	var binding string
	if err := f.sql.QueryRow(f.ctx, `SELECT binding FROM context_collections WHERE id=$1`, collection.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := codegraph.New(docker, os.Getenv("CONDUCTOR_CODEGRAPH_IMAGE"))
	if err != nil {
		t.Fatal(err)
	}
	activity, err := collectionworker.NewActivity(f.db, func(context.Context, domain.ContextIntegration) (string, error) {
		return "synthetic-provider-token", nil
	}, func(remote.Config) (collectionworker.Collector, error) {
		return graphFixtureCollector{artifacts: artifacts}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	activity = activity.WithCodeGraph(adapter)
	result, err := activity.Collect(f.ctx, contextworkflow.Reference{ID: collection.ID, Binding: binding})
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewAuthenticated(f.server.URL, f.tokens["author"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	g, err := c.CreateRepositoryGraph(f.ctx, "native-graph", domain.GraphInput{Sources: []domain.GraphSource{{RepositoryID: "application", CollectionID: collection.ID, Digest: result.Digest}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(g.Snapshot.Indexer, domain.CodeGraphIndexer) {
		t.Fatalf("upstream provenance missing: %+v", g)
	}
	found := false
	for _, edge := range g.Snapshot.Edges {
		if edge.Kind == "codegraph:calls" {
			found = true
		}
	}
	if !found {
		t.Fatal("native parsed call relation missing from shared graph")
	}
	// Revocation cannot erase a previously committed receipt or permit public read.
	if err = f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-author", CanRead: false}}}); err != nil {
		t.Fatal(err)
	}
	again, err := activity.Collect(f.ctx, contextworkflow.Reference{ID: collection.ID, Binding: binding})
	if err != nil || again != result {
		t.Fatalf("receipt recovery: %+v %v", again, err)
	}
	f.request("author", "team", "application", "GET", "/api/v1/repository-graphs/"+g.ID, nil, 404, nil)
}

type graphFixtureCollector struct{ artifacts []domain.ContextArtifact }

func (c graphFixtureCollector) Collect(ctx context.Context, _ string, _ []string, check func(context.Context) error) ([]domain.ContextArtifact, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	return c.artifacts, nil
}
