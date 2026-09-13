package acceptance_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/pkg/client"
)

func TestAuthenticatedDeliveryAPIAndSharedClient(t *testing.T) {
	f := collectionFixture(t)
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries(), f.verifier))
	input, c := seedDeliveryInput(t, f, []byte("synthetic fixture: worker execution is tested separately"))

	agent, err := client.NewAuthenticated(f.server.URL, f.tokens["agent"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	d, err := agent.CreateDelivery(f.ctx, "publication", input)
	if err != nil {
		t.Fatal(err)
	}
	if d.ProposerID != "person-agent" || d.Authorization != nil || d.Receipt != nil {
		t.Fatal("proposal fabricated authority or provider evidence")
	}
	got, err := c.GetDelivery(f.ctx, d.ID)
	if err != nil || got.Digest != d.Digest {
		t.Fatal("shared publication missing")
	}
	inspected, err := c.GetDeliveryArtifact(f.ctx, d.ID)
	if err != nil || inspected.DeliveryDigest != d.Digest || inspected.ArtifactDigest != input.ArtifactDigest {
		t.Fatalf("exact artifact inspection: %v", err)
	}
	var implementation execution.Result
	if json.Unmarshal(inspected.Artifact, &implementation) != nil || string(implementation.Patches[0].Patch) != "synthetic fixture: worker execution is tested separately" || len(implementation.Checks) != 1 {
		t.Fatal("artifact evidence omitted")
	}
	page, err := c.ListDeliveries(f.ctx, "", 1)
	if err != nil || len(page.Deliveries) != 1 {
		t.Fatal("publication discovery missing")
	}
	base := "/api/v1/repository-deliveries"
	endpoints := []accessEndpoint{{"POST", base, input}, {"GET", base, nil}, {"GET", base + "/" + d.ID, nil}, {"GET", base + "/" + d.ID + "/artifact", nil}, {"POST", base + "/" + d.ID + "/authorizations", map[string]string{"digest": d.Digest}}, {"POST", base + "/" + d.ID + "/reconciliations", map[string]string{"digest": d.Digest}}}
	for _, e := range endpoints {
		headers := http.Header{"Idempotency-Key": {"isolation"}}
		f.raw(f.server.URL, "forged.token", "team", "application", e.method, e.path, e.body, headers, 401, nil)
		headers.Set("X-Conductor-Actor", "forged")
		f.raw(f.server.URL, f.tokens["reviewer"], "team", "application", e.method, e.path, e.body, headers, 401, nil)
		f.localRequest("reviewer", e.method, e.path, e.body, 503, nil)
	}
	f.request("agent", "team", "application", "POST", base+"/"+d.ID+"/authorizations", map[string]string{"digest": d.Digest}, 403, nil)
	f.request("reviewer", "team", "private", "GET", base+"/"+d.ID, nil, 404, nil)
	f.request("reviewer", "team", "application", "POST", base+"/"+d.ID+"/authorizations", map[string]string{"digest": d.Digest, "actor": "person-reviewer"}, 400, nil)
	if _, err = c.AuthorizeDelivery(f.ctx, d.ID, d.Digest); err != nil {
		t.Fatal(err)
	}
	if err = f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanRead: false}}}); err != nil {
		t.Fatal(err)
	}
	f.request("reviewer", "team", "application", "GET", base+"/"+d.ID, nil, 404, nil)
}

// seedDeliveryInput retains synthetic execution facts for API/UI transport tests.
// Actual worker and provider behavior is verified by separate Docker/HTTP suites.
func seedDeliveryInput(t *testing.T, f *accessFixture, patchBytes []byte) (domain.DeliveryInput, *client.Client) {
	t.Helper()
	return seedDeliveryInputWithContent(t, f, patchBytes, domain.Content{"intent": "Synthetic publication acceptance"})
}

func seedDeliveryInputWithContent(t *testing.T, f *accessFixture, patchBytes []byte, content domain.Content) (domain.DeliveryInput, *client.Client) {
	t.Helper()
	profile := domain.ExecutionProfileConfig{WorkspaceID: "team", ID: "synthetic", Image: "sha256:" + strings.Repeat("a", 64), Enabled: true, Profile: domain.WorkerProfile{Adapter: "command/v1", Command: []string{"/bin/true"}}}
	_, profileDigest, err := store.ValidatedExecutionProfile(profile.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{ExecutionProfiles: []domain.ExecutionProfileConfig{profile}, Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanRead: true, CanAuthor: true, CanApprove: true}}, ExecutionGrants: []domain.ExecutionGrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanExecute: true, CanPublish: true}, {RepositoryID: "application", PrincipalID: "person-agent", CanExecute: true, CanPublish: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.ApplyDeliveryIntegrations(f.ctx, "synthetic-operator", []domain.DeliveryIntegrationConfig{{WorkspaceID: "team", RepositoryID: "application", Profile: domain.GitHubDeliveryProfile, Locator: "synthetic/application", CredentialID: "fixture-publication", BaseBranches: []string{"main"}, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	c, err := client.NewAuthenticated(f.server.URL, f.tokens["reviewer"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	data := bundleFixture(t)
	var collection domain.Collection
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", domain.CollectionInput{Commit: data.Commit, Paths: []string{"README.md"}, FullSource: true}, http.Header{"Idempotency-Key": {"delivery-source"}}, 202, &collection)
	var binding string
	if err = f.sql.QueryRow(f.ctx, `SELECT binding FROM context_collections WHERE id=$1`, collection.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	// This fixture uses real retained Git bytes but explicitly marks native indexing
	// unexecuted. Native CodeGraph and coding execution have separate acceptance.
	index := domain.CodeGraphIndex{Indexer: domain.CodeGraphIndexer, KernelVersion: "publication-unexecuted", Files: []domain.CodeGraphFile{}, Nodes: []domain.CodeGraphNode{}, Edges: []domain.CodeGraphEdge{}}
	for _, a := range data.Artifacts {
		index.Files = append(index.Files, domain.CodeGraphFile{Path: a.Path, State: "unsupported_or_deferred"})
	}
	receipt, err := f.db.CompleteCollectionWithSource(f.ctx, collection.ID, binding, data.Artifacts[:1], data, index)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := c.CreateRepositoryGraph(f.ctx, "delivery-graph", domain.GraphInput{Sources: []domain.GraphSource{{RepositoryID: "application", CollectionID: collection.ID, Digest: receipt.Digest, FullSourceDigest: data.Digest}}})
	if err != nil {
		t.Fatal(err)
	}
	author, err := client.NewAuthenticated(f.server.URL, f.tokens["author"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := author.Create(f.ctx, content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = author.Submit(f.ctx, pkg.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Approve(f.ctx, pkg.ID, 1, pkg.Revision.Digest); err != nil {
		t.Fatal(err)
	}
	check := domain.VerificationCommand{ID: "test", RepositoryID: "application", Argv: []string{"go", "test", "./..."}, TimeoutSeconds: 10}
	plan := domain.CoordinationPlan{SchemaVersion: 1, GraphID: graph.ID, GraphDigest: graph.Digest, Packages: []domain.PackagePin{{ChangeID: pkg.ID, RepositoryID: "application", Revision: 1, Digest: pkg.Revision.Digest}}, Repositories: []domain.CoordinationRepository{{RepositoryID: "application", Commit: collection.Input.Commit, CollectionID: collection.ID, ReceiptDigest: receipt.Digest, FullSourceDigest: data.Digest}}, Tasks: []domain.CoordinationTask{{ID: "code", Perspective: "developer", Profile: profile.ID, ProfileDigest: profileDigest, Image: profile.Image, Prompt: "Fixture", Scopes: []domain.TaskScope{{RepositoryID: "application", WritablePaths: []string{"src"}}}, Checks: []domain.VerificationCommand{check}, TimeoutSeconds: 30}}, MaxParallel: 1}
	run, err := c.CreateCoordination(f.ctx, "delivery-run", plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.AuthorizeCoordination(f.ctx, run.ID, run.Digest); err != nil {
		t.Fatal(err)
	}
	var task string
	if err = f.sql.QueryRow(f.ctx, `SELECT id FROM coordination_tasks WHERE run_id=$1`, run.ID).Scan(&task); err != nil {
		t.Fatal(err)
	}
	patches := []execution.Patch{{RepositoryID: "application", BaseCommit: collection.Input.Commit, BaseTree: data.Tree, ResultTree: strings.Repeat("c", 40), Patch: patchBytes, Digest: execution.Sum(patchBytes), Paths: []string{"src/file.go"}}}
	patchJSON, _ := json.Marshal(patches)
	zero := 0
	artifact := execution.Result{CleanupConfirmed: true, ProfileDigest: profileDigest, Image: profile.Image, Adapter: "command/v1", AdapterVersion: "1", InputDigest: strings.Repeat("d", 64), Patches: patches, Producer: execution.Evidence{State: "passed", ExitCode: &zero, SourceDigest: strings.Repeat("d", 64), OutputDigest: execution.Sum(nil)}, Checks: []execution.Evidence{{ID: check.ID, RepositoryID: check.RepositoryID, Argv: check.Argv, State: "passed", ExitCode: &zero, SourceDigest: execution.Sum(patchJSON), OutputDigest: execution.Sum(nil)}}}
	digest, _ := domain.JSONDigest(artifact)
	if _, err = f.sql.Exec(f.ctx, `INSERT INTO coordination_task_receipts(task_id,run_id,digest,outcome,artifact_digest,artifact) VALUES($1,$2,$3,'succeeded',$3,$4)`, task, run.ID, digest, artifact); err != nil {
		t.Fatal(err)
	}
	input := domain.DeliveryInput{RunID: run.ID, TaskID: task, ArtifactDigest: digest, BaseBranch: "main", Title: "Synthetic verified change", Description: "Fixture"}
	return input, c
}
