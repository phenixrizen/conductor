package acceptance_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/authn"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/pkg/client"
)

// These tests use signed identity, actual HTTP/service commands, and isolated
// PostgreSQL schemas. Receipt setup uses the trusted store completion command;
// that fixture does not claim provider or Temporal execution.
func collectionFixture(t *testing.T) *accessFixture {
	t.Helper()
	return collectionFixtureWithTimeout(t, time.Minute)
}

func collectionFixtureWithTimeout(t *testing.T, timeout time.Duration) *accessFixture {
	t.Helper()
	f := newAccessFixtureWithIssuer(t, nil, nil, timeout)
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections(), f.verifier))
	configureCollectionIntegration(t, f, "team", "application", true)
	configureCollectionIntegration(t, f, "other-team", "other-application", true)
	return f
}

func configureCollectionIntegration(t *testing.T, f *accessFixture, workspace, repository string, enabled bool) {
	t.Helper()
	config := domain.AccessConfig{ContextIntegrations: []domain.ContextIntegrationConfig{{WorkspaceID: workspace, RepositoryID: repository, Profile: "github-rest/2026-03-10", Locator: "synthetic/application", CredentialID: "synthetic-read-credential", Enabled: enabled}}}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-context-operator", config); err != nil {
		t.Fatal(err)
	}
}

func collectionInput() domain.CollectionInput {
	return domain.CollectionInput{Commit: strings.Repeat("a", 40), Paths: []string{"docs/missing.md", "README.md"}}
}
func requestCollection(t *testing.T, f *accessFixture, actor, workspace, repository, key string) domain.Collection {
	t.Helper()
	var value domain.Collection
	f.raw(f.server.URL, f.tokens[actor], workspace, repository, "POST", "/api/v1/context-collections", collectionInput(), http.Header{"Idempotency-Key": {key}}, 202, &value)
	return value
}
func completeCollectionFixture(t *testing.T, f *accessFixture, collection domain.Collection) domain.CollectionReceipt {
	t.Helper()
	var binding string
	if err := f.sql.QueryRow(f.ctx, "SELECT binding FROM context_collections WHERE id=$1", collection.ID).Scan(&binding); err != nil {
		t.Fatal(err)
	}
	text := "# Synthetic shared source\nThis fixture is collected text, not passing verification.\n"
	hash := sha256.Sum256([]byte(text))
	artifacts := []domain.ContextArtifact{
		{Path: "README.md", State: "collected", BlobOID: strings.Repeat("b", 40), Digest: hex.EncodeToString(hash[:]), Text: &text},
		{Path: "docs/missing.md", State: "missing", Message: "Not present at the selected commit"},
	}
	receipt, err := f.db.CompleteCollection(f.ctx, collection.ID, binding, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func collectionEndpoints(collectionID, changeID, digest string) []accessEndpoint {
	return []accessEndpoint{
		{"POST", "/api/v1/context-collections", collectionInput()},
		{"GET", "/api/v1/context-collections", nil},
		{"GET", "/api/v1/context-collections/" + collectionID, nil},
		{"POST", "/api/v1/context-collections/" + collectionID + "/cancellation", map[string]any{}},
		{"POST", "/api/v1/changes/" + changeID + "/context-attachments", map[string]any{"expectedRevision": 1, "collectionId": collectionID, "digest": digest}},
	}
}

func TestAuthenticatedCollectionsShareReceiptsAndAttachOnlyInspectedContent(t *testing.T) {
	f := collectionFixture(t)
	collected := requestCollection(t, f, "author", "team", "application", "author-request-1")
	if collected.RequesterID != "person-author" || collected.Source.WorkspaceID != "team" || collected.Source.RepositoryID != "application" || collected.Receipt != nil || collected.Execution != nil || !reflect.DeepEqual(collected.Input.Paths, []string{"README.md", "docs/missing.md"}) {
		t.Fatalf("request lost canonical identity, deterministic paths, or unknown execution: %+v", collected)
	}
	var replay domain.Collection
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", collected.Input, http.Header{"Idempotency-Key": {"author-request-1"}}, 202, &replay)
	if replay.ID != collected.ID || replay.InputDigest != collected.InputDigest {
		t.Fatal("equivalent path order created a second request")
	}
	changed := collectionInput()
	changed.Commit = strings.Repeat("b", 40)
	c, err := client.NewAuthenticated(f.server.URL, f.tokens["author"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateCollection(f.ctx, "author-request-1", changed)
	var conflict *client.APIError
	if !errors.As(err, &conflict) || conflict.StatusCode != 409 || conflict.Code != "idempotency_conflict" {
		t.Fatalf("changed retry input: %v", err)
	}
	f.raw(f.server.URL, f.tokens["reader"], "team", "application", "POST", "/api/v1/context-collections", collectionInput(), http.Header{"Idempotency-Key": {"reader-request"}}, 403, nil)
	agent := requestCollection(t, f, "agent", "team", "application", "author-request-1")
	if agent.ID == collected.ID || agent.RequesterID != "person-agent" {
		t.Fatal("requester-scoped idempotency or server-owned agent attribution was lost")
	}
	f.request("reviewer", "team", "application", "POST", "/api/v1/context-collections/"+collected.ID+"/cancellation", map[string]any{}, 403, nil)
	receipt := completeCollectionFixture(t, f, collected)
	var shared domain.Collection
	f.request("reader", "team", "application", "GET", "/api/v1/context-collections/"+collected.ID, nil, 200, &shared)
	if shared.Receipt == nil || shared.Receipt.Digest != receipt.Digest || shared.Receipt.Snapshot.Artifacts[1].State != "missing" || shared.Execution != nil {
		t.Fatalf("reader lost shared evidence: %+v", shared)
	}

	pkg := f.create("author", "team", "application", domain.Content{"intent": "Preserve reviewed design", "futureField": map[string]any{"retained": true}})
	path := "/api/v1/changes/" + pkg.ID
	f.request("author", "team", "application", "POST", path+"/review-requests", map[string]any{"revision": 1}, 200, nil)
	f.request("reviewer", "team", "application", "POST", path+"/approvals", map[string]any{"revision": 1, "digest": pkg.Revision.Digest}, 201, nil)
	attachment := map[string]any{"expectedRevision": 1, "collectionId": collected.ID, "digest": strings.Repeat("f", 64)}
	f.request("agent", "team", "application", "POST", path+"/context-attachments", attachment, 409, nil)
	attachment["digest"] = receipt.Digest
	var attached domain.Package
	f.request("agent", "team", "application", "POST", path+"/context-attachments", attachment, 201, &attached)
	if attached.Revision.Number != 2 || attached.Revision.Author != "person-agent" || attached.Approved || attached.Approval != nil || attached.Revision.SubmittedAt != nil || !reflect.DeepEqual(attached.Revision.Content["futureField"], pkg.Revision.Content["futureField"]) {
		t.Fatalf("attachment changed approval, author, or unknown fields: %+v", attached)
	}
	f.request("agent", "team", "application", "POST", path+"/context-attachments", attachment, 409, nil)
	f.request("agent", "team", "application", "POST", path+"/review-requests", map[string]any{"revision": 2}, 200, nil)
	f.request("agent", "team", "application", "POST", path+"/approvals", map[string]any{"revision": 2, "digest": attached.Revision.Digest}, 403, nil)
	var history domain.RevisionRecord
	f.request("reviewer", "team", "application", "GET", path+"/revisions/1", nil, 200, &history)
	if len(history.Approvals) != 1 || history.Approvals[0].Digest != pkg.Revision.Digest {
		t.Fatal("attachment discarded historical approval")
	}
	configureCollectionIntegration(t, f, "team", "application", false)
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", collectionInput(), http.Header{"Idempotency-Key": {"new-disabled-request"}}, 503, nil)
	f.request("reader", "team", "application", "GET", "/api/v1/context-collections/"+collected.ID, nil, 200, &shared)
	if shared.Receipt == nil || shared.Receipt.Digest != receipt.Digest {
		t.Fatal("operator disablement erased historical source")
	}
}

func TestAuthenticatedCollectionScopeAndIdentityProtectEveryEndpoint(t *testing.T) {
	f := collectionFixture(t)
	collection := requestCollection(t, f, "author", "team", "application", "scoped-request")
	pkg := f.create("author", "team", "application", domain.Content{"intent": "Scoped attachment target"})
	for _, endpoint := range collectionEndpoints(collection.ID, pkg.ID, strings.Repeat("a", 64)) {
		for _, scope := range [][2]string{{"", "application"}, {"team", ""}, {"", ""}} {
			f.raw(f.server.URL, f.tokens["author"], scope[0], scope[1], endpoint.method, endpoint.path, endpoint.body, http.Header{"Idempotency-Key": {"scope-check"}}, 400, nil)
		}
		f.raw(f.server.URL, f.tokens["reviewer"], "other-team", "other-application", endpoint.method, endpoint.path, endpoint.body, http.Header{"Idempotency-Key": {"scope-check"}}, 403, nil)
		f.raw(f.server.URL, "forged.token", "team", "application", endpoint.method, endpoint.path, endpoint.body, http.Header{"Idempotency-Key": {"scope-check"}}, 401, nil)
		f.raw(f.server.URL, f.tokens["author"], "team", "application", endpoint.method, endpoint.path, endpoint.body, http.Header{"Idempotency-Key": {"scope-check"}, "X-Conductor-Actor": {"person-reviewer"}}, 401, nil)
		f.localRequest("person-author", endpoint.method, endpoint.path, endpoint.body, 503, nil)
	}
	foreign := requestCollection(t, f, "other", "other-team", "other-application", "foreign-request")
	for _, endpoint := range collectionEndpoints(foreign.ID, pkg.ID, strings.Repeat("b", 64))[2:] {
		f.request("author", "team", "application", endpoint.method, endpoint.path, endpoint.body, 404, nil)
	}
	f.request("author", "team", "private", "GET", "/api/v1/context-collections/"+collection.ID, nil, 404, nil)
	var page domain.CollectionPage
	f.request("reviewer", "team", "application", "GET", "/api/v1/context-collections", nil, 200, &page)
	if len(page.Collections) != 1 || page.Collections[0].ID != collection.ID {
		t.Fatalf("discovery crossed canonical scope: %+v", page)
	}
}

func TestAuthenticatedCollectionReceiptLinksCannotBeForgedOrMovedAcrossWorkspaces(t *testing.T) {
	f := collectionFixture(t)
	collection := requestCollection(t, f, "author", "team", "application", "trusted-receipt")
	receipt := completeCollectionFixture(t, f, collection)
	pkg := f.create("author", "team", "application", domain.Content{"intent": "Current scoped package"})
	foreignPackage := f.create("other", "other-team", "other-application", domain.Content{"intent": "Same external repository in another workspace"})
	f.request("other", "other-team", "other-application", "POST", "/api/v1/changes/"+foreignPackage.ID+"/context-attachments", map[string]any{"expectedRevision": 1, "collectionId": collection.ID, "digest": receipt.Digest}, 404, nil)
	for _, mode := range []string{"unknown receipt", "tampered metadata", "extra extension", "foreign scope"} {
		t.Run(mode, func(t *testing.T) {
			sub := *f
			sub.t = t
			contextValue := jsonObject(t, receipt.Snapshot)
			status := 400
			switch mode {
			case "unknown receipt":
				contextValue["collectionId"], status = strings.Repeat("f", 32), 404
			case "tampered metadata":
				contextValue["repository"] = "synthetic/forged-label"
			case "extra extension":
				contextValue["trustedBy"] = "self-appointed-operator"
			case "foreign scope":
				source := contextValue["source"].(map[string]any)
				source["workspaceId"], source["repositoryId"], status = "other-team", "other-application", 403
			}
			content := domain.Content{"intent": "Forged provenance must not persist", "repositoryContext": contextValue}
			sub.request("author", "team", "application", "POST", "/api/v1/changes", map[string]any{"content": content}, status, nil)
			sub.request("author", "team", "application", "POST", "/api/v1/changes/"+pkg.ID+"/revisions", map[string]any{"expectedRevision": 1, "content": content}, status, nil)
		})
	}
	var unchanged domain.Package
	f.request("author", "team", "application", "GET", "/api/v1/changes/"+pkg.ID, nil, 200, &unchanged)
	if !reflect.DeepEqual(unchanged, pkg) {
		t.Fatal("denied provenance write altered the package")
	}
}

func TestAuthenticatedCollectionPaginationRevocationAndCancellationFacts(t *testing.T) {
	f := collectionFixture(t)
	wanted := make(map[string]bool)
	var first domain.Collection
	for i := 0; i < 3; i++ {
		c := requestCollection(t, f, "author", "team", "application", fmt.Sprintf("page-%d", i))
		wanted[c.ID] = true
		if i == 0 {
			first = c
		}
		requestCollection(t, f, "other", "other-team", "other-application", fmt.Sprintf("foreign-%d", i))
	}
	seen, before := make(map[string]bool), ""
	for pages := 0; ; pages++ {
		if pages == 4 {
			t.Fatal("collection cursor did not terminate")
		}
		var page domain.CollectionPage
		f.request("reader", "team", "application", "GET", "/api/v1/context-collections?limit=1&before="+url.QueryEscape(before), nil, 200, &page)
		if len(page.Collections) != 1 || !wanted[page.Collections[0].ID] || seen[page.Collections[0].ID] {
			t.Fatalf("pagination leaked or repeated a collection: %+v", page)
		}
		seen[page.Collections[0].ID] = true
		if page.NextBefore == "" {
			break
		}
		before = page.NextBefore
	}
	if !reflect.DeepEqual(seen, wanted) {
		t.Fatal("pagination omitted a readable collection")
	}
	var cancellation domain.Collection
	path := "/api/v1/context-collections/" + first.ID
	f.request("author", "team", "application", "POST", path+"/cancellation", map[string]any{}, 202, &cancellation)
	if cancellation.CancelRequestedAt == nil || cancellation.Execution != nil || cancellation.Receipt != nil {
		t.Fatal("cancellation intent pretended execution had stopped")
	}
	var repeated domain.Collection
	f.request("author", "team", "application", "POST", path+"/cancellation", map[string]any{}, 202, &repeated)
	if !reflect.DeepEqual(cancellation.CancelRequestedAt, repeated.CancelRequestedAt) {
		t.Fatal("repeated cancellation changed its historical timestamp")
	}
	var audit, outbox int
	if err := f.sql.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM context_audit_events WHERE collection_id=$1 AND event_type='collection.cancellation_requested'),(SELECT count(*) FROM context_outbox WHERE collection_id=$1 AND operation='cancel')", first.ID).Scan(&audit, &outbox); err != nil || audit != 1 || outbox != 1 {
		t.Fatalf("cancellation audit=%d outbox=%d err=%v", audit, outbox, err)
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-reader"}}}); err != nil {
		t.Fatal(err)
	}
	f.request("reader", "team", "application", "GET", path, nil, 404, nil)
	var revokedPage domain.CollectionPage
	f.request("reader", "team", "application", "GET", "/api/v1/context-collections", nil, 200, &revokedPage)
	if len(revokedPage.Collections) != 0 {
		t.Fatal("revoked discovery exposed summaries")
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Memberships: []domain.MembershipConfig{{WorkspaceID: "team", PrincipalID: "person-author", Active: false}}}); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range collectionEndpoints(first.ID, "CHG-scoped", strings.Repeat("a", 64)) {
		f.raw(f.server.URL, f.tokens["author"], "team", "application", endpoint.method, endpoint.path, endpoint.body, http.Header{"Idempotency-Key": {"revoked-request"}}, 403, nil)
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Principals: []domain.PrincipalConfig{{ID: "person-reviewer", Issuer: f.issuer.url, Subject: "subject-reviewer", Kind: "human", Active: false}}}); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range collectionEndpoints(first.ID, "CHG-scoped", strings.Repeat("a", 64)) {
		f.raw(f.server.URL, f.tokens["reviewer"], "team", "application", endpoint.method, endpoint.path, endpoint.body, http.Header{"Idempotency-Key": {"inactive-request"}}, 401, nil)
	}
}

func TestCollectionBrowserCommandsUseSignedLoginAndSessionCSRF(t *testing.T) {
	app := httptest.NewUnstartedServer(nil)
	t.Cleanup(app.Close)
	issuer := newBrowserIssuer(t, "https://"+app.Listener.Addr().String()+"/api/v1/auth/callback")
	f := newAccessFixtureWithIssuer(t, issuer.accessIssuer, issuer.client, time.Minute)
	configureCollectionIntegration(t, f, "team", "application", true)
	provider, err := authn.NewBrowser(f.ctx, authn.BrowserConfig{Issuer: issuer.url, ClientID: browserClientID, ClientSecret: browserClientSecret, RedirectURL: issuer.callback, HTTPClient: issuer.client})
	if err != nil {
		t.Fatal(err)
	}
	origin := "https://" + app.Listener.Addr().String()
	handler, err := api.NewBrowserAuthenticated(service.NewAuthenticated(f.db).WithCollections(), f.verifier, provider, f.db, api.BrowserConfig{Origin: origin, Issuer: issuer.url})
	if err != nil {
		t.Fatal(err)
	}
	app.Config.Handler = handler
	app.StartTLS()
	browserFixture := &browserFixture{accessFixture: f, app: app, provider: issuer}
	browser := browserFixture.browserClient()
	csrf := browserFixture.login(browser, "author")
	headers := http.Header{"Origin": {origin}, "X-Conductor-Workspace": {"team"}, "X-Conductor-Repository": {"application"}, "Idempotency-Key": {"browser-request"}}
	browserFixture.call(browser, "POST", "/api/v1/context-collections", collectionInput(), headers, 403, nil)
	headers.Set("X-Conductor-CSRF", csrf)
	var collection domain.Collection
	browserFixture.call(browser, "POST", "/api/v1/context-collections", collectionInput(), headers, 202, &collection)
	receipt := completeCollectionFixture(t, f, collection)
	pkg := f.create("author", "team", "application", domain.Content{"intent": "Browser attachment"})
	for _, endpoint := range collectionEndpoints(collection.ID, pkg.ID, receipt.Digest)[3:] {
		wrong := headers.Clone()
		wrong.Set("Origin", "https://other.example.test")
		browserFixture.call(browser, endpoint.method, endpoint.path, endpoint.body, wrong, 403, nil)
		missing := headers.Clone()
		missing.Del("X-Conductor-CSRF")
		browserFixture.call(browser, endpoint.method, endpoint.path, endpoint.body, missing, 403, nil)
	}
	var attached domain.Package
	browserFixture.call(browser, "POST", "/api/v1/changes/"+pkg.ID+"/context-attachments", map[string]any{"expectedRevision": 1, "collectionId": collection.ID, "digest": receipt.Digest}, headers, 201, &attached)
	if attached.Revision.Number != 2 || attached.Revision.Author != "person-author" || attached.Approved {
		t.Fatal("cookie attachment lost identity or approval isolation")
	}
	browserFixture.login(browser, "reviewer")
	browserFixture.call(browser, "GET", "/api/v1/context-collections/"+collection.ID, nil, headers, 403, nil)
}

func TestCollectionAPIRequiresServerAndRepositoryEnablement(t *testing.T) {
	f := newAccessFixture(t)
	headers := http.Header{"Idempotency-Key": {"explicit-enablement"}}
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", collectionInput(), headers, 503, nil)
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections(), f.verifier))
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", collectionInput(), headers, 503, nil)
	configureCollectionIntegration(t, f, "team", "application", false)
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", collectionInput(), headers, 503, nil)
	var count int
	if err := f.sql.QueryRow(f.ctx, "SELECT count(*) FROM context_collections").Scan(&count); err != nil || count != 0 {
		t.Fatalf("disabled capability persisted work: count=%d error=%v", count, err)
	}
	configureCollectionIntegration(t, f, "team", "application", true)
	var collection domain.Collection
	f.raw(f.server.URL, f.tokens["author"], "team", "application", "POST", "/api/v1/context-collections", collectionInput(), headers, 202, &collection)
	if collection.ID == "" || collection.RequesterID != "person-author" {
		t.Fatal("explicitly enabled request lost server-owned identity")
	}
}

func TestCollectionExecutionObservationsRemainExplicitInSharedReads(t *testing.T) {
	f := collectionFixture(t)
	collection := requestCollection(t, f, "author", "team", "application", "observed-progress")
	// These fixture-only observations exercise the PostgreSQL read model. They
	// do not stand in for the separate actual Temporal process acceptance suite.
	if _, err := f.sql.Exec(f.ctx, "INSERT INTO context_runtime_bindings(collection_id,target,namespace) VALUES($1,$2,'synthetic-namespace')", collection.ID, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sql.Exec(f.ctx, "INSERT INTO context_execution_observations(collection_id,namespace,workflow_id,run_id,state) VALUES($1,'synthetic-namespace',$2,'fixture-run','unavailable')", collection.ID, "context/"+collection.ID); err != nil {
		t.Fatal(err)
	}
	for _, observation := range []struct {
		state, age string
		current    bool
	}{{"unavailable", "0 seconds", false}, {"running", "1 minute", false}, {"running", "0 seconds", true}} {
		if _, err := f.sql.Exec(f.ctx, "UPDATE context_execution_observations SET state=$2,observed_at=clock_timestamp()-$3::interval WHERE collection_id=$1", collection.ID, observation.state, observation.age); err != nil {
			t.Fatal(err)
		}
		var inspected domain.Collection
		var page domain.CollectionPage
		f.request("reader", "team", "application", "GET", "/api/v1/context-collections/"+collection.ID, nil, 200, &inspected)
		f.request("reader", "team", "application", "GET", "/api/v1/context-collections", nil, 200, &page)
		if inspected.Receipt != nil || len(page.Collections) != 1 || page.Collections[0].ReceiptDigest != "" {
			t.Fatal("execution observation was presented as a collected receipt")
		}
		for _, execution := range []*domain.CollectionExecution{inspected.Execution, page.Collections[0].Execution} {
			if execution == nil || execution.Namespace != "synthetic-namespace" || execution.RunID != "fixture-run" || execution.WorkflowID != "context/"+collection.ID || execution.State != observation.state || execution.Current != observation.current || execution.ObservedAt.IsZero() {
				t.Fatalf("stored execution identity/freshness lost in shared read: %+v", execution)
			}
		}
	}
}
