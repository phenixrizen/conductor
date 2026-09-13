package acceptance_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/phenixrizen/conductor/internal/codegraph"
	"github.com/phenixrizen/conductor/internal/collectionworker"
	"github.com/phenixrizen/conductor/internal/contextworkflow"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/repositorycontext/remote"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/pkg/client"
)

func bundleFixture(t *testing.T) domain.SourceBundleData {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("/usr/bin/git", append([]string{"-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Dir = root
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + root, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.invalid"}
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("synthetic Git: %v %s", err, data)
		}
		return strings.TrimSpace(string(data))
	}
	run("init", "--initial-branch=main")
	data := domain.SourceBundleData{Artifacts: []domain.ContextArtifact{}, FileCount: 2}
	for _, v := range [][2]string{{"README.md", "Synthetic full-repository fixture\n"}, {"fixture.go", "package fixture\nfunc Called(){}\nfunc Caller(){Called()}\n"}} {
		if err := os.WriteFile(filepath.Join(root, v[0]), []byte(v[1]), 0600); err != nil {
			t.Fatal(err)
		}
		text := v[1]
		sum := sha256.Sum256([]byte(text))
		blob := run("hash-object", v[0])
		data.Artifacts = append(data.Artifacts, domain.ContextArtifact{Path: v[0], State: "collected", Text: &text, BlobOID: blob, Digest: hex.EncodeToString(sum[:])})
	}
	run("add", ".")
	run("commit", "-m", "Synthetic source bundle")
	data.Commit = run("rev-parse", "HEAD")
	data.Tree = run("rev-parse", "HEAD^{tree}")
	bundle := filepath.Join(t.TempDir(), "source.bundle")
	run("bundle", "create", bundle, "--all")
	var err error
	data.Bundle, err = os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data.Bundle)
	data.Digest = hex.EncodeToString(sum[:])
	return data
}

type fullSourceFixtureCollector struct{ data domain.SourceBundleData }

func (c fullSourceFixtureCollector) Collect(ctx context.Context, _ string, _ []string, check func(context.Context) error) ([]domain.ContextArtifact, error) {
	if err := check(ctx); err != nil {
		return nil, err
	}
	return c.data.Artifacts[:1], nil
}
func (c fullSourceFixtureCollector) FullSource(ctx context.Context, _ string, check func(context.Context) error) (domain.SourceBundleData, error) {
	if err := check(ctx); err != nil {
		return domain.SourceBundleData{}, err
	}
	return c.data, nil
}

func TestFullSourceReceiptGraphAndTrustedBundleGetter(t *testing.T) {
	fullSourceAcceptance(t, false)
}

func TestFullSourceTemporalReceiptAndGraph(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TEMPORAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TEMPORAL=1 for owned full-source workflow acceptance")
	}
	fullSourceAcceptance(t, true)
}

func fullSourceAcceptance(t *testing.T, temporal bool) {
	if os.Getenv("CONDUCTOR_TEST_CODEGRAPH") != "1" {
		t.Skip("set CONDUCTOR_TEST_CODEGRAPH=1 for actual native full-source activity acceptance")
	}
	fixtureTimeout := time.Minute
	if temporal {
		// Include fixture and Temporal startup in the intended workflow budget;
		// runFullSourceTemporal cannot extend an earlier parent deadline.
		fixtureTimeout = 90 * time.Second
	}
	f := collectionFixtureWithTimeout(t, fixtureTimeout)
	data := bundleFixture(t)
	var collection domain.Collection
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", domain.CollectionInput{Commit: data.Commit, Paths: []string{"README.md"}, FullSource: true}, http.Header{"Idempotency-Key": {"full-source"}}, 202, &collection)
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
	activity, err := collectionworker.NewActivity(f.db, func(context.Context, domain.ContextIntegration) (string, error) { return "synthetic-token", nil }, func(remote.Config) (collectionworker.Collector, error) {
		return fullSourceFixtureCollector{data: data}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	activity = activity.WithCodeGraph(adapter)
	var result contextworkflow.Result
	if temporal {
		result = runFullSourceTemporal(t, f, activity, collection.ID)
	} else {
		result, err = activity.Collect(f.ctx, contextworkflow.Reference{ID: collection.ID, Binding: binding})
	}
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewAuthenticated(f.server.URL, f.tokens["author"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetCollection(f.ctx, collection.ID)
	if err != nil || got.FullSource == nil || got.FullSource.Digest != data.Digest || got.FullSource.Tree != data.Tree {
		t.Fatalf("full source summary: %+v %v", got, err)
	}
	if len(got.Receipt.Snapshot.Artifacts) != 1 || got.Receipt.Snapshot.SchemaVersion != 2 {
		t.Fatal("full source changed version 2 selected-path receipt semantics")
	}
	loaded, err := f.db.LoadSourceBundle(f.ctx, "team", "application", collection.ID, result.Digest, data.Commit)
	if err != nil || loaded.Digest != data.Digest || string(loaded.Bundle) != string(data.Bundle) {
		t.Fatalf("exact source getter: %v", err)
	}
	if _, err = f.db.LoadSourceBundle(f.ctx, "other-team", "application", collection.ID, result.Digest, data.Commit); err == nil {
		t.Fatal("source getter crossed canonical scope")
	}
	input := domain.GraphInput{Sources: []domain.GraphSource{{RepositoryID: "application", CollectionID: collection.ID, Digest: result.Digest, FullSourceDigest: data.Digest}}}
	graph, err := c.CreateRepositoryGraph(f.ctx, "whole-graph", input)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range graph.Snapshot.Nodes {
		if n.Name == "Caller" && n.Path == "fixture.go" {
			found = true
		}
	}
	if !found || graph.Snapshot.Sources[0].FullSourceDigest != data.Digest {
		t.Fatalf("whole repository graph/provenance absent: %+v", graph.Snapshot)
	}
	input.Sources[0].FullSourceDigest = strings.Repeat("f", 64)
	if _, err = c.CreateRepositoryGraph(f.ctx, "forged-full-source", input); err == nil {
		t.Fatal("forged source bundle digest accepted")
	}
	if _, err = f.sql.Exec(f.ctx, `DELETE FROM context_source_bundles`); err == nil {
		t.Fatal("mutable source bundle")
	}
	// Source mode is immutable retry input, not a request to upgrade an existing receipt.
	_, err = c.CreateCollection(f.ctx, "full-source", domain.CollectionInput{Commit: data.Commit, Paths: []string{"README.md"}})
	var conflict *client.APIError
	if !errors.As(err, &conflict) || conflict.StatusCode != 409 || conflict.Code != "idempotency_conflict" {
		t.Fatalf("changed full-source retry input: %v", err)
	}
	// Public graph visibility and trusted scoped bundle reads both honor revocation.
	if _, err = f.sql.Exec(f.ctx, `UPDATE repository_grants SET can_read=false,can_author=false,can_approve=false WHERE repository_id='application' AND principal_id='person-author'`); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginAccess(f.ctx, domain.AccessRequest{Identity: domain.AccessIdentity{Issuer: f.issuer.url, Subject: "subject-author"}, WorkspaceID: "team", RepositoryID: "application"})
	if err != nil {
		t.Fatal(err)
	}
	scoped := tx.(*store.Postgres)
	if _, err = scoped.LoadSourceBundle(f.ctx, "team", "application", collection.ID, result.Digest, data.Commit); err == nil {
		t.Fatal("revoked principal read private bundle")
	}
	if err = tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetRepositoryGraph(f.ctx, graph.ID); err == nil {
		t.Fatal("revoked principal read whole-source graph")
	}
	// A lost activity result still recovers the committed receipt after revocation.
	recovered, err := activity.Collect(f.ctx, contextworkflow.Reference{ID: collection.ID, Binding: binding})
	if err != nil || recovered != result {
		t.Fatalf("committed full-source retry: %+v %v", recovered, err)
	}
}

// This path uses the production dispatcher, bound runtime and protected activity
// against an owned Temporal process. Only provider acquisition is a local fixture;
// Git bundle verification, native CodeGraph and persistence are real.
func runFullSourceTemporal(t *testing.T, f *accessFixture, activity *collectionworker.Activity, id string) contextworkflow.Result {
	t.Helper()
	address := durableAddress(t)
	process := durableStartTemporal(t, t.TempDir(), address, "full-source-temporal")
	defer process.stop()
	ctx, cancel := context.WithTimeout(f.ctx, 90*time.Second)
	defer cancel()
	engine, err := durableEngine(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	resolve := func(ctx context.Context) (string, error) {
		return contextworkflow.RuntimeTarget(ctx, engine, durableNamespace, address, contextworkflow.TaskQueue)
	}
	target, err := resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contextworkflow.NewBoundRuntime(engine, durableNamespace, address, contextworkflow.TaskQueue, target)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := collectionworker.NewDispatcher(f.db, runtime, durableNamespace, target, resolve)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- contextworkflow.RunWorker(workerCtx, engine, contextworkflow.TaskQueue, activity.Collect)
	}()
	defer func() {
		stopWorker()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	if delivered, err := dispatcher.Step(ctx); err != nil || !delivered {
		t.Fatalf("dispatch: %v %v", delivered, err)
	}
	var result contextworkflow.Result
	if err := engine.GetWorkflow(ctx, contextworkflow.WorkflowName+"/"+id, "").Get(ctx, &result); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	iterator := engine.GetWorkflowHistory(ctx, contextworkflow.WorkflowName+"/"+id, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	events, total := 0, 0
	for iterator.HasNext() {
		event, err := iterator.Next()
		if err != nil {
			t.Fatal(err)
		}
		payload, err := protojson.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		events++
		total += len(payload)
		if events > 256 || total > 1<<20 {
			t.Fatal("full-source workflow history exceeded bound")
		}
		for _, forbidden := range []string{"package fixture", "Synthetic full-repository fixture", "synthetic-token", "bundle"} {
			if strings.Contains(string(payload), forbidden) {
				t.Fatal("private source or credentials entered Temporal history")
			}
		}
	}
	return result
}

// These transaction cases intentionally use a structural index fixture. Native
// parser execution is covered above; here failure after bundle insert must roll
// back every receipt, source and audit fact together.
func TestFullSourceReceiptBundleAndAuditAreAtomic(t *testing.T) {
	f := collectionFixture(t)
	data := bundleFixture(t)
	index := domain.CodeGraphIndex{Indexer: domain.CodeGraphIndexer, KernelVersion: "synthetic-fixture", Files: []domain.CodeGraphFile{}, Nodes: []domain.CodeGraphNode{}, Edges: []domain.CodeGraphEdge{}}
	for _, a := range data.Artifacts {
		index.Files = append(index.Files, domain.CodeGraphFile{Path: a.Path, State: "unsupported_or_deferred"})
	}
	var collection domain.Collection
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", domain.CollectionInput{Commit: data.Commit, Paths: []string{"README.md"}, FullSource: true}, http.Header{"Idempotency-Key": {"atomic-source"}}, 202, &collection)
	var binding string
	if err := f.sql.QueryRow(f.ctx, `SELECT binding FROM context_collections WHERE id=$1`, collection.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sql.Exec(f.ctx, `CREATE FUNCTION reject_source_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='collection.receipt_recorded' THEN RAISE EXCEPTION 'synthetic audit rejection'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_source_audit BEFORE INSERT ON context_audit_events FOR EACH ROW EXECUTE FUNCTION reject_source_audit()`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.CompleteCollectionWithSource(f.ctx, collection.ID, binding, data.Artifacts[:1], data, index); err == nil {
		t.Fatal("source completion ignored audit failure")
	}
	var receipts, bundles, audits int
	if err := f.sql.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM context_receipts),(SELECT count(*) FROM context_source_bundles),(SELECT count(*) FROM context_audit_events WHERE event_type='collection.receipt_recorded')`).Scan(&receipts, &bundles, &audits); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || bundles != 0 || audits != 0 {
		t.Fatalf("partial source facts survived rollback: %d %d %d", receipts, bundles, audits)
	}
	if _, err := f.sql.Exec(f.ctx, `DROP TRIGGER reject_source_audit ON context_audit_events`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sql.Exec(f.ctx, `UPDATE repository_grants SET can_author=false WHERE repository_id='application' AND principal_id='person-author'`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.CompleteCollectionWithSource(f.ctx, collection.ID, binding, data.Artifacts[:1], data, index); err == nil {
		t.Fatal("revoked author committed full source")
	}
	if err := f.sql.QueryRow(f.ctx, `SELECT count(*) FROM context_source_bundles`).Scan(&bundles); err != nil || bundles != 0 {
		t.Fatalf("revocation committed source: %d %v", bundles, err)
	}
}
