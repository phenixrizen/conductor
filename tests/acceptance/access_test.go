package acceptance_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/authn"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
)

// These workflows use signed access tokens, discovery/JWKS HTTP endpoints, the
// production verifier and service, and a migrated PostgreSQL schema. Token roles
// deliberately disagree with database grants so a passing test cannot depend on
// trusting a client-selected role or local development actor.
func TestAuthenticatedSharedReview(t *testing.T) {
	f := newAccessFixture(t)
	var session domain.Session
	f.request("reviewer", "", "", http.MethodGet, "/api/v1/session", nil, http.StatusOK, &session)
	if session.Principal.ID != "person-reviewer" || session.Principal.Kind != "human" || len(session.Workspaces) != 1 || session.Workspaces[0].ID != "team" || session.Truncated {
		t.Fatalf("session did not resolve durable identity and membership: %+v", session)
	}
	f.request("agent", "", "", http.MethodGet, "/api/v1/session", nil, http.StatusOK, &session)
	if session.Principal.ID != "person-agent" || session.Principal.Kind != "agent" {
		t.Fatalf("signed human role claim overrode the service identity: %+v", session)
	}
	var repositories domain.RepositoryPage
	f.request("agent", "team", "", http.MethodGet, "/api/v1/repositories", nil, http.StatusOK, &repositories)
	if len(repositories.Repositories) != 1 || repositories.Truncated {
		t.Fatalf("agent repository discovery: %+v", repositories)
	}
	repository := repositories.Repositories[0]
	if repository.ID != "application" || repository.Provider != "github" || repository.Host != "github.com" || repository.ProviderID != "101" || !repository.CanRead || !repository.CanAuthor || repository.CanApprove {
		t.Fatalf("repository identity or effective agent capabilities: %+v", repository)
	}

	created := f.create("author", "team", "application", domain.Content{
		"intent":    "A shared synthetic design",
		"extension": map[string]any{"retain": true},
	})
	if created.Revision.Author != "person-author" || created.Approved || created.WorkspaceID != "team" || created.RepositoryID != "application" {
		t.Fatalf("creation did not use the server's durable principal: %+v", created)
	}
	var discovered domain.ChangePage
	f.request("reviewer", "team", "", http.MethodGet, "/api/v1/changes?limit=1", nil, http.StatusOK, &discovered)
	if len(discovered.Changes) != 1 || discovered.Changes[0].ID != created.ID || discovered.Changes[0].Author != "person-author" || discovered.Changes[0].WorkspaceID != "team" || discovered.Changes[0].RepositoryID != "application" {
		t.Fatalf("independent human did not discover shared work: %+v", discovered)
	}
	path := "/api/v1/changes/" + url.PathEscape(created.ID)
	f.request("author", "team", "application", http.MethodPost, path+"/review-requests", map[string]any{"revision": 1}, http.StatusOK, nil)
	var inspected domain.Package
	f.request("reviewer", "team", "application", http.MethodGet, path, nil, http.StatusOK, &inspected)
	if inspected.Revision.SubmittedAt == nil || !reflect.DeepEqual(inspected.Revision.Content, created.Revision.Content) {
		t.Fatalf("reviewer cannot inspect exact submitted content: %+v", inspected)
	}
	approval := map[string]any{"revision": inspected.Revision.Number, "digest": inspected.Revision.Digest}
	f.request("author", "team", "application", http.MethodPost, path+"/approvals", approval, http.StatusUnprocessableEntity, nil)
	f.request("agent", "team", "application", http.MethodPost, path+"/approvals", approval, http.StatusForbidden, nil)
	var approved domain.Package
	f.request("reviewer", "team", "application", http.MethodPost, path+"/approvals", approval, http.StatusCreated, &approved)
	if !approved.Approved || approved.Approval == nil || approved.Approval.Reviewer != "person-reviewer" || approved.Approval.Digest != inspected.Revision.Digest {
		t.Fatalf("approval did not preserve the inspected content and human identity: %+v", approved)
	}

	// An independently scoped agent may author a new revision, but the previous
	// human approval is historical and the old browser request must conflict.
	revisedContent := domain.Content{"intent": "An agent proposes a revised design", "extension": map[string]any{"retain": true}}
	var revised domain.Package
	f.request("agent", "team", "application", http.MethodPost, path+"/revisions", map[string]any{"expectedRevision": 1, "content": revisedContent}, http.StatusCreated, &revised)
	if revised.Revision.Number != 2 || revised.Revision.Author != "person-agent" || revised.Approved || revised.Approval != nil || revised.Revision.SubmittedAt != nil || !reflect.DeepEqual(revised.Revision.Content, revisedContent) {
		t.Fatalf("agent edit lost attribution, extensions, or approval invalidation: %+v", revised)
	}
	f.request("reviewer", "team", "application", http.MethodPost, path+"/approvals", approval, http.StatusConflict, nil)
	f.request("author", "team", "application", http.MethodPost, path+"/revisions", map[string]any{"expectedRevision": 1, "content": domain.Content{"intent": "Stale author edit"}}, http.StatusConflict, nil)
	var history domain.HistoryPage
	f.request("reviewer", "team", "application", http.MethodGet, path+"/history", nil, http.StatusOK, &history)
	if len(history.Revisions) != 2 || history.Revisions[0].Number != 2 || history.Revisions[0].ApprovalCount != 0 || history.Revisions[1].ApprovalCount != 1 {
		t.Fatalf("authenticated history lost historical approval: %+v", history)
	}
	var historical domain.RevisionRecord
	f.request("reviewer", "team", "application", http.MethodGet, path+"/revisions/1", nil, http.StatusOK, &historical)
	if !reflect.DeepEqual(historical.Revision, inspected.Revision) || len(historical.Approvals) != 1 || historical.Approvals[0].Reviewer != "person-reviewer" || historical.ApprovalsTruncated {
		t.Fatalf("historical content or approval changed: %+v", historical)
	}
	var audit domain.AuditPage
	f.request("reviewer", "team", "application", http.MethodGet, path+"/events", nil, http.StatusOK, &audit)
	if len(audit.Events) != 4 || audit.Events[0].Actor != "person-author" || audit.Events[2].Actor != "person-reviewer" || audit.Events[3].Actor != "person-agent" {
		t.Fatalf("authenticated commands lost attribution or failed commands wrote audit: %+v", audit)
	}

	// Reopening connections and the HTTP service proves metadata survives a new
	// application instance. The separate opt-in suite tests actual DB restarts.
	f.reopenAPIAndStore()
	var recovered domain.Package
	f.request("reviewer", "team", "application", http.MethodGet, path, nil, http.StatusOK, &recovered)
	if !reflect.DeepEqual(recovered, revised) {
		t.Fatalf("new service instance did not recover shared package: %+v", recovered)
	}
	f.request("reviewer", "", "", http.MethodGet, "/api/v1/session", nil, http.StatusOK, &session)
	if session.Principal.ID != "person-reviewer" || len(session.Workspaces) != 1 {
		t.Fatalf("new service instance did not recover access metadata: %+v", session)
	}
}

func TestAuthenticatedScopeIsolationAndLegacySeparation(t *testing.T) {
	f := newAccessFixture(t)
	visible := f.create("author", "team", "application", domain.Content{"intent": "Readable shared package"})
	private := f.create("author", "team", "private", domain.Content{"intent": "Private same-workspace package"})
	foreign := f.create("other", "other-team", "other-application", domain.Content{"intent": "Another workspace's package"})
	legacy := f.localCreate(domain.Content{"intent": "Unscoped local package"})

	for _, hidden := range []struct {
		name string
		pkg  domain.Package
	}{
		{"private repository", private}, {"other workspace", foreign}, {"legacy package", legacy},
	} {
		t.Run(hidden.name, func(t *testing.T) {
			sub := *f
			sub.t = t
			for _, endpoint := range accessPackageEndpoints(hidden.pkg) {
				sub.request("reviewer", "team", "", endpoint.method, endpoint.path, endpoint.body, http.StatusNotFound, nil)
			}
		})
	}
	// Local actor mode cannot inspect or mutate an authenticated package even
	// when the attacker supplies the known durable principal ID as its actor.
	for _, endpoint := range accessPackageEndpoints(visible) {
		f.localRequest("person-author", endpoint.method, endpoint.path, endpoint.body, http.StatusNotFound, nil)
	}
	var localList domain.ChangePage
	f.localRequest("person-author", http.MethodGet, "/api/v1/changes", nil, http.StatusOK, &localList)
	if len(localList.Changes) != 1 || localList.Changes[0].ID != legacy.ID {
		t.Fatalf("local discovery exposed authenticated records: %+v", localList)
	}

	// Workspace selection is a scope request, never a membership assignment.
	f.request("reviewer", "other-team", "other-application", http.MethodGet, "/api/v1/repositories", nil, http.StatusForbidden, nil)
	f.request("reviewer", "other-team", "other-application", http.MethodGet, "/api/v1/changes", nil, http.StatusForbidden, nil)
	f.request("reviewer", "other-team", "other-application", http.MethodPost, "/api/v1/changes", map[string]any{"content": domain.Content{"intent": "Forbidden creation"}}, http.StatusForbidden, nil)
	f.request("reviewer", "team", "private", http.MethodPost, "/api/v1/changes", map[string]any{"content": domain.Content{"intent": "Forbidden repository creation"}}, http.StatusNotFound, nil)
	f.request("reader", "team", "application", http.MethodPost, "/api/v1/changes", map[string]any{"content": domain.Content{"intent": "Read permission cannot create"}}, http.StatusForbidden, nil)
	f.request("reviewer", "team", "", http.MethodPost, "/api/v1/changes", map[string]any{"content": domain.Content{"intent": "Missing canonical repository"}}, http.StatusBadRequest, nil)

	var knownRepositories domain.RepositoryPage
	f.request("reviewer", "team", "", http.MethodGet, "/api/v1/repositories", nil, http.StatusOK, &knownRepositories)
	if len(knownRepositories.Repositories) != 1 || knownRepositories.Repositories[0].ID != "application" {
		t.Fatalf("repository discovery disclosed inaccessible repositories: %+v", knownRepositories)
	}
	for _, endpoint := range accessPackageEndpoints(visible) {
		if endpoint.method == http.MethodPost {
			f.request("reader", "team", "application", endpoint.method, endpoint.path, endpoint.body, http.StatusForbidden, nil)
		} else {
			f.request("reader", "team", "application", endpoint.method, endpoint.path, endpoint.body, http.StatusOK, nil)
		}
	}
	var current domain.Package
	f.request("author", "team", "application", http.MethodGet, "/api/v1/changes/"+visible.ID, nil, http.StatusOK, &current)
	if !reflect.DeepEqual(current, visible) {
		t.Fatalf("denied commands altered the package: %+v", current)
	}
}

func TestAuthenticatedDiscoveryFiltersBeforePaginationAndKeepsOwnership(t *testing.T) {
	f := newAccessFixture(t)
	// The signed-in author is permitted in both repositories. Choosing misleading
	// content must not move a package or make it disappear from its true scope.
	snapshot := committedContext(t, f.ctx, "private")
	content := domain.Content{"intent": "Misleading repository label", "repositoryContext": jsonObject(t, snapshot), "workspaceId": "other-team", "repositoryId": "private"}
	want := make(map[string]bool)
	var first domain.Package
	for i := 0; i < 3; i++ {
		created := f.create("author", "team", "application", content)
		want[created.ID] = true
		if i == 0 {
			first = created
		}
		f.create("author", "team", "private", domain.Content{"intent": fmt.Sprintf("Hidden package %d", i)})
		f.create("other", "other-team", "other-application", domain.Content{"intent": fmt.Sprintf("Foreign package %d", i)})
	}
	seen := make(map[string]bool)
	var before string
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber >= 4 {
			t.Fatal("authorized discovery pagination did not terminate")
		}
		var page domain.ChangePage
		f.request("reviewer", "team", "", http.MethodGet, "/api/v1/changes?limit=1&before="+url.QueryEscape(before), nil, http.StatusOK, &page)
		if len(page.Changes) != 1 || !want[page.Changes[0].ID] || seen[page.Changes[0].ID] || page.Changes[0].WorkspaceID != "team" || page.Changes[0].RepositoryID != "application" {
			t.Fatalf("page leaked, skipped, or repeated records before applying permissions: %+v", page)
		}
		seen[page.Changes[0].ID] = true
		if page.NextBefore == "" {
			break
		}
		if page.NextBefore == before {
			t.Fatal("discovery cursor did not advance")
		}
		before = page.NextBefore
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("discovery lost authorized packages: got %v want %v", seen, want)
	}
	var labeled domain.ChangePage
	f.request("reviewer", "team", "application", http.MethodGet, "/api/v1/changes?repository=private", nil, http.StatusOK, &labeled)
	if len(labeled.Changes) != len(want) {
		t.Fatalf("author-supplied label did not preserve bounded, authorized discovery: %+v", labeled)
	}
	for _, change := range labeled.Changes {
		if !want[change.ID] || change.Repository != "private" || change.RepositoryID != "application" {
			t.Fatalf("content grouping label became an authorization boundary: %+v", change)
		}
	}
	// Selecting a repository must constrain direct access too; a guessed ID is
	// not an escape hatch from the current workspace/repository scope.
	for _, endpoint := range accessPackageEndpoints(first) {
		f.request("author", "team", "private", endpoint.method, endpoint.path, endpoint.body, http.StatusNotFound, nil)
	}
	var revised domain.Package
	f.request("author", "team", "application", http.MethodPost, "/api/v1/changes/"+first.ID+"/revisions", map[string]any{
		"expectedRevision": 1,
		"content":          domain.Content{"intent": "Revised text still cannot transfer ownership", "workspaceId": "other-team", "repositoryId": "other-application"},
	}, http.StatusCreated, &revised)
	var workspace, repository string
	if err := f.sql.QueryRow(f.ctx, `SELECT workspace_id,repository_id FROM changes WHERE id=$1`, first.ID).Scan(&workspace, &repository); err != nil || workspace != "team" || repository != "application" {
		t.Fatalf("revision rewrote canonical ownership: %s/%s err=%v", workspace, repository, err)
	}
	if _, err := f.sql.Exec(f.ctx, `UPDATE changes SET workspace_id='other-team',repository_id='other-application' WHERE id=$1`, first.ID); err == nil {
		t.Fatal("database permitted moving an existing package between scopes")
	}
	f.request("reviewer", "team", "application", http.MethodGet, "/api/v1/changes/"+first.ID, nil, http.StatusOK, nil)
	f.request("other", "other-team", "other-application", http.MethodGet, "/api/v1/changes/"+first.ID, nil, http.StatusNotFound, nil)
}

func TestAuthenticatedRevocationAndAccessAudit(t *testing.T) {
	f := newAccessFixture(t)
	created := f.create("author", "team", "application", domain.Content{"intent": "Review access may be revoked"})
	path := "/api/v1/changes/" + created.ID
	f.request("author", "team", "application", http.MethodPost, path+"/review-requests", map[string]any{"revision": 1}, http.StatusOK, nil)
	approval := map[string]any{"revision": 1, "digest": created.Revision.Digest}
	token := f.tokens["reviewer"]

	// Each change is observed using the same still-valid bearer token. No logout,
	// token refresh, server restart, or stale capability cache may be required.
	updates := []struct {
		name   string
		config domain.AccessConfig
		status int
	}{
		{"approval grant", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanRead: true, CanAuthor: true}}}, http.StatusForbidden},
		{"membership", domain.AccessConfig{Memberships: []domain.MembershipConfig{{WorkspaceID: "team", PrincipalID: "person-reviewer", Active: false}}}, http.StatusForbidden},
		{"principal", domain.AccessConfig{Principals: []domain.PrincipalConfig{{ID: "person-reviewer", Issuer: f.issuer.url, Subject: "subject-reviewer", Kind: "human", Active: false}}}, http.StatusUnauthorized},
	}
	for _, update := range updates {
		t.Run(update.name, func(t *testing.T) {
			sub := *f
			sub.t = t
			if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", update.config); err != nil {
				t.Fatalf("revoke %s: %v", update.name, err)
			}
			sub.request("reviewer", "team", "application", http.MethodPost, path+"/approvals", approval, update.status, nil)
			switch update.name {
			case "membership":
				var session domain.Session
				sub.request("reviewer", "", "", http.MethodGet, "/api/v1/session", nil, http.StatusOK, &session)
				if len(session.Workspaces) != 0 {
					t.Fatalf("session still advertised revoked membership: %+v", session)
				}
			case "principal":
				sub.request("reviewer", "", "", http.MethodGet, "/api/v1/session", nil, http.StatusUnauthorized, nil)
			}
			if f.tokens["reviewer"] != token {
				t.Fatal("revocation test accidentally replaced the access token")
			}
			if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", f.config); err != nil {
				t.Fatalf("restore access fixture: %v", err)
			}
		})
	}
	// Read revocation must cover history and audit as well as latest content.
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer"}}}); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range accessPackageEndpoints(created) {
		f.request("reviewer", "team", "application", endpoint.method, endpoint.path, endpoint.body, http.StatusNotFound, nil)
	}
	var empty domain.ChangePage
	f.request("reviewer", "team", "", http.MethodGet, "/api/v1/changes", nil, http.StatusOK, &empty)
	if len(empty.Changes) != 0 || empty.NextBefore != "" {
		t.Fatalf("revoked repository still appears in discovery: %+v", empty)
	}
	var auditCount int
	if err := f.sql.QueryRow(f.ctx, `SELECT count(*) FROM access_audit_events WHERE operator='synthetic-operator'`).Scan(&auditCount); err != nil || auditCount < 8 {
		t.Fatalf("permission changes were not durably audited: count=%d err=%v", auditCount, err)
	}
	// A failed identity rebind must roll back the whole administrative command,
	// including otherwise valid edits and its audit entry.
	bad := domain.AccessConfig{
		Principals: []domain.PrincipalConfig{
			{ID: "person-reader", Issuer: f.issuer.url, Subject: "subject-reader", Kind: "human", Active: false},
			{ID: "person-author", Issuer: f.issuer.url, Subject: "different-subject", Kind: "human", Active: true},
		},
	}
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", bad); err == nil {
		t.Fatal("administrative update rebound an existing principal")
	}
	var afterCount int
	f.request("reader", "team", "application", http.MethodGet, path, nil, http.StatusOK, nil)
	if err := f.sql.QueryRow(f.ctx, `SELECT count(*) FROM access_audit_events WHERE operator='synthetic-operator'`).Scan(&afterCount); err != nil || afterCount != auditCount {
		t.Fatalf("failed administrative command wrote audit: %d -> %d err=%v", auditCount, afterCount, err)
	}
	f.reopenAPIAndStore()
	f.request("reviewer", "team", "application", http.MethodGet, path, nil, http.StatusNotFound, nil)
	var recovered domain.Package
	f.request("author", "team", "application", http.MethodGet, path, nil, http.StatusOK, &recovered)
	if recovered.Approved || recovered.Revision.Number != 1 {
		t.Fatalf("revoked approval or administrative operation changed governance: %+v", recovered)
	}
}

func TestAuthenticatedAPIRejectsImpersonation(t *testing.T) {
	f := newAccessFixture(t)
	created := f.create("author", "team", "application", domain.Content{"intent": "Authentication precedes shared data"})
	path := "/api/v1/changes/" + created.ID
	for _, endpoint := range append(accessPackageEndpoints(created), accessEndpoint{method: http.MethodGet, path: "/api/v1/session"}, accessEndpoint{method: http.MethodGet, path: "/api/v1/repositories"}, accessEndpoint{method: http.MethodGet, path: "/api/v1/changes"}, accessEndpoint{method: http.MethodPost, path: "/api/v1/changes", body: map[string]any{"content": domain.Content{"intent": "Cannot create anonymously"}}}) {
		f.raw(f.server.URL, "", "team", "application", endpoint.method, endpoint.path, endpoint.body, nil, http.StatusUnauthorized, nil)
	}
	for _, invalid := range []string{
		"not-a-token",
		f.issuer.token(t, "subject-reviewer", map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}),
		f.issuer.token(t, "subject-reviewer", map[string]any{"aud": "a-different-service"}),
		f.issuer.token(t, "subject-unknown", nil),
	} {
		f.raw(f.server.URL, invalid, "team", "application", http.MethodGet, path, nil, nil, http.StatusUnauthorized, nil)
	}
	f.raw(f.server.URL, f.tokens["agent"], "team", "application", http.MethodGet, path, nil, http.Header{"X-Conductor-Actor": []string{"person-reviewer"}}, http.StatusUnauthorized, nil)
	f.raw(f.server.URL, f.tokens["reviewer"], "team", "application", http.MethodGet, path, nil, http.Header{"X-Conductor-Actor": []string{""}}, http.StatusUnauthorized, nil)
	f.raw(f.server.URL, f.tokens["reviewer"], "team", "application", http.MethodGet, path, nil, http.Header{"Authorization": []string{"Bearer " + f.tokens["reviewer"], "Bearer " + f.tokens["author"]}}, http.StatusUnauthorized, nil)
	f.raw(f.server.URL, f.tokens["reviewer"], "team", "application", http.MethodGet, path, nil, http.Header{"X-Conductor-Workspace": []string{"team", "other-team"}}, http.StatusBadRequest, nil)
	f.request("author", "team", "application", http.MethodPost, path+"/review-requests", map[string]any{"revision": 1}, http.StatusOK, nil)
	approval := map[string]any{"revision": 1, "digest": created.Revision.Digest}
	// Neither ordinary HTTP role headers nor signed but unauthorized role claims
	// turn a service principal into an architect with approval authority.
	f.raw(f.server.URL, f.tokens["agent"], "team", "application", http.MethodPost, path+"/approvals", approval, http.Header{"X-Conductor-Role": []string{"architect"}, "X-Conductor-Principal": []string{"person-reviewer"}}, http.StatusForbidden, nil)
	f.request("reviewer", "team", "application", http.MethodPost, path+"/approvals", map[string]any{"revision": 1, "digest": created.Revision.Digest, "actor": "person-author"}, http.StatusBadRequest, nil)
	var final domain.Package
	f.request("reviewer", "team", "application", http.MethodGet, path, nil, http.StatusOK, &final)
	if final.Approved || final.Revision.Number != 1 || final.Revision.Author != "person-author" {
		t.Fatalf("impersonation attempt changed package: %+v", final)
	}
}

type accessEndpoint struct {
	method, path string
	body         any
}

func accessPackageEndpoints(pkg domain.Package) []accessEndpoint {
	path := "/api/v1/changes/" + url.PathEscape(pkg.ID)
	return []accessEndpoint{
		{http.MethodGet, path, nil},
		{http.MethodGet, path + "/history", nil},
		{http.MethodGet, path + "/revisions/1", nil},
		{http.MethodGet, path + "/events", nil},
		{http.MethodPost, path + "/revisions", map[string]any{"expectedRevision": 1, "content": domain.Content{"intent": "Must never persist"}}},
		{http.MethodPost, path + "/review-requests", map[string]any{"revision": 1}},
		{http.MethodPost, path + "/approvals", map[string]any{"revision": 1, "digest": pkg.Revision.Digest}},
	}
}

type accessFixture struct {
	t             *testing.T
	ctx           context.Context
	databaseURL   string
	db            *store.Postgres
	sql           *pgxpool.Pool
	server, local *httptest.Server
	issuer        *accessIssuer
	verifier      *authn.Verifier
	tokens        map[string]string
	config        domain.AccessConfig
}

func newAccessFixture(t *testing.T) *accessFixture {
	return newAccessFixtureWithIssuer(t, nil, nil, 60*time.Second)
}

// Browser acceptance uses a TLS authorization-code issuer and an explicit longer
// budget for its real browser process. Existing API acceptance keeps its limit.
func newAccessFixtureWithIssuer(t *testing.T, issuer *accessIssuer, issuerClient *http.Client, timeout time.Duration) *accessFixture {
	t.Helper()
	databaseURL := os.Getenv("CONDUCTOR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set CONDUCTOR_TEST_DATABASE_URL to run authenticated PostgreSQL acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "conductor_access_acceptance_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop authenticated acceptance schema: %v", err)
		}
	})
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+quoted); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(paths) == 0 {
		t.Fatalf("find access migrations: %v %v", paths, err)
	}
	for _, path := range paths {
		migration, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("apply %s: %v", path, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	isolatedURL, err := url.Parse(databaseURL)
	if err != nil || (isolatedURL.Scheme != "postgres" && isolatedURL.Scheme != "postgresql") {
		t.Fatalf("CONDUCTOR_TEST_DATABASE_URL must be a PostgreSQL URL: %v", err)
	}
	query := isolatedURL.Query()
	query.Set("search_path", schema)
	isolatedURL.RawQuery = query.Encode()
	f := &accessFixture{t: t, ctx: ctx, databaseURL: isolatedURL.String(), tokens: make(map[string]string)}
	f.sql, err = pgxpool.New(ctx, f.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.sql.Close)
	f.db, err = store.Open(ctx, f.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Close() })
	f.issuer = issuer
	if f.issuer == nil {
		f.issuer = newAccessIssuer(t)
	}
	f.verifier, err = authn.New(ctx, authn.Config{Issuer: f.issuer.url, Audience: "conductor-acceptance", HTTPClient: issuerClient, AllowInsecureLoopback: issuer == nil})
	if err != nil {
		t.Fatalf("initialize real access-token verifier: %v", err)
	}
	f.config = accessConfiguration(f.issuer.url)
	if err := f.db.ApplyAccessConfig(ctx, "synthetic-operator", f.config); err != nil {
		t.Fatalf("provision synthetic workspaces and grants: %v", err)
	}
	// Real-provider qualifications do not possess or synthesize its signing key.
	if f.issuer.key != nil {
		for _, name := range []string{"author", "reviewer", "reader", "agent", "other"} {
			f.tokens[name] = f.issuer.token(t, "subject-"+name, nil)
		}
	}
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db), f.verifier))
	t.Cleanup(func() { f.server.Close() })
	f.local = httptest.NewServer(api.New(service.New(f.db)))
	t.Cleanup(func() { f.local.Close() })
	return f
}

func accessConfiguration(issuer string) domain.AccessConfig {
	config := domain.AccessConfig{
		Workspaces: []domain.WorkspaceConfig{{ID: "team", Name: "Synthetic engineering"}, {ID: "other-team", Name: "Synthetic other workspace"}},
		Repositories: []domain.RepositoryConfig{
			{ID: "application", WorkspaceID: "team", Provider: "github", Host: "github.com", ProviderID: "101", Name: "synthetic/application"},
			{ID: "private", WorkspaceID: "team", Provider: "gitlab", Host: "gitlab.example.invalid", ProviderID: "101", Name: "synthetic/application"},
			{ID: "other-application", WorkspaceID: "other-team", Provider: "github", Host: "github.com", ProviderID: "101", Name: "synthetic/application"},
		},
		Grants: []domain.GrantConfig{
			{RepositoryID: "application", PrincipalID: "person-author", CanRead: true, CanAuthor: true, CanApprove: true},
			{RepositoryID: "application", PrincipalID: "person-reviewer", CanRead: true, CanAuthor: true, CanApprove: true},
			{RepositoryID: "application", PrincipalID: "person-reader", CanRead: true},
			{RepositoryID: "application", PrincipalID: "person-agent", CanRead: true, CanAuthor: true, CanApprove: true},
			{RepositoryID: "private", PrincipalID: "person-author", CanRead: true, CanAuthor: true, CanApprove: true},
			{RepositoryID: "other-application", PrincipalID: "person-other", CanRead: true, CanAuthor: true, CanApprove: true},
		},
	}
	for _, name := range []string{"author", "reviewer", "reader", "agent", "other"} {
		kind, workspace := "human", "team"
		if name == "agent" {
			kind = "agent"
		}
		if name == "other" {
			workspace = "other-team"
		}
		config.Principals = append(config.Principals, domain.PrincipalConfig{ID: "person-" + name, Issuer: issuer, Subject: "subject-" + name, Kind: kind, Active: true})
		config.Memberships = append(config.Memberships, domain.MembershipConfig{WorkspaceID: workspace, PrincipalID: "person-" + name, Active: true})
	}
	return config
}

func (f *accessFixture) reopenAPIAndStore() {
	f.t.Helper()
	f.server.Close()
	f.local.Close()
	f.db.Close()
	var err error
	f.db, err = store.Open(f.ctx, f.databaseURL)
	if err != nil {
		f.t.Fatal(err)
	}
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db), f.verifier))
	f.local = httptest.NewServer(api.New(service.New(f.db)))
}

func (f *accessFixture) create(actor, workspace, repository string, content domain.Content) domain.Package {
	f.t.Helper()
	var pkg domain.Package
	f.request(actor, workspace, repository, http.MethodPost, "/api/v1/changes", map[string]any{"content": content}, http.StatusCreated, &pkg)
	return pkg
}

func (f *accessFixture) localCreate(content domain.Content) domain.Package {
	f.t.Helper()
	var pkg domain.Package
	f.localRequest("synthetic-local-author", http.MethodPost, "/api/v1/changes", map[string]any{"content": content}, http.StatusCreated, &pkg)
	return pkg
}

func (f *accessFixture) request(actor, workspace, repository, method, path string, body any, want int, result any) {
	f.t.Helper()
	token, ok := f.tokens[actor]
	if !ok {
		f.t.Fatalf("unknown synthetic token fixture %q", actor)
	}
	f.raw(f.server.URL, token, workspace, repository, method, path, body, nil, want, result)
}

func (f *accessFixture) localRequest(actor, method, path string, body any, want int, result any) {
	f.t.Helper()
	f.raw(f.local.URL, "", "", "", method, path, body, http.Header{"X-Conductor-Actor": []string{actor}}, want, result)
}

func (f *accessFixture) raw(baseURL, token, workspace, repository, method, path string, body any, headers http.Header, want int, result any) {
	f.t.Helper()
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
	}
	req, err := http.NewRequestWithContext(f.ctx, method, baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		f.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if workspace != "" {
		req.Header.Set("X-Conductor-Workspace", workspace)
	}
	if repository != "" {
		req.Header.Set("X-Conductor-Repository", repository)
	}
	req.Header.Set("Content-Type", "application/json")
	for name, values := range headers {
		req.Header[name] = append([]string(nil), values...)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		f.t.Fatalf("read bounded response: bytes=%d err=%v", len(data), err)
	}
	if response.StatusCode != want {
		f.t.Fatalf("%s %s in %s/%s: HTTP %d, want %d: %s", method, path, workspace, repository, response.StatusCode, want, data)
	}
	if want >= 400 {
		var failure struct {
			Error struct{ Code, Message, CorrelationID string } `json:"error"`
		}
		if err := json.Unmarshal(data, &failure); err != nil || failure.Error.Code == "" || failure.Error.Message == "" || failure.Error.CorrelationID == "" || failure.Error.CorrelationID != response.Header.Get("X-Correlation-ID") {
			f.t.Fatalf("unbounded or uncorrelated API error: %s err=%v", data, err)
		}
		if (token != "" && strings.Contains(string(data), token)) || strings.Contains(string(data), "SELECT ") {
			f.t.Fatal("API error exposed a credential or persistence detail")
		}
		if code := map[int]string{http.StatusUnauthorized: "authentication_required", http.StatusForbidden: "permission_denied", http.StatusNotFound: "not_found", http.StatusConflict: "revision_conflict", http.StatusUnprocessableEntity: "approval_rejected"}[want]; code != "" && failure.Error.Code != code {
			f.t.Fatalf("wrong stable error code: got %q want %q", failure.Error.Code, code)
		}
	}
	if result != nil {
		if err := json.Unmarshal(data, result); err != nil {
			f.t.Fatalf("decode %s %s response: %v", method, path, err)
		}
	}
}

type accessIssuer struct {
	url string
	key *rsa.PrivateKey
}

func newAccessIssuer(t *testing.T) *accessIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	issuer := &accessIssuer{url: "http://" + server.Listener.Addr().String(), key: key}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": issuer.url, "jwks_uri": issuer.url + "/keys"})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
				"kty": "RSA", "kid": "synthetic-key", "alg": "RS256", "use": "sig",
				"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
			}}})
		default:
			http.NotFound(w, r)
		}
	})
	server.Start()
	t.Cleanup(server.Close)
	return issuer
}

func (issuer *accessIssuer) token(t *testing.T, subject string, override map[string]any) string {
	t.Helper()
	now := time.Now()
	claims := map[string]any{
		"iss": issuer.url, "sub": subject, "aud": "conductor-acceptance",
		"exp": now.Add(10 * time.Minute).Unix(), "iat": now.Add(-time.Second).Unix(),
		"client_id": "synthetic-client", "jti": "synthetic-" + subject,
		"roles": []string{"administrator", "architect"}, "kind": "human", "preferred_username": "person-reviewer",
	}
	for name, value := range override {
		claims[name] = value
	}
	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "at+jwt", "kid": "synthetic-key"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, issuer.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature)
}
