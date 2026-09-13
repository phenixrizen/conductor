package acceptance_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

// This browser suite exercises real signed login, HTTP commands and PostgreSQL.
// Immutable receipt fixtures do not claim to exercise providers or Temporal;
// their process and transport acceptance remains in the durable context suite.
func TestBrowserContextCollections(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for actual collection workbench acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	f := newConfiguredBrowserFixture(t, browserFixtureOptions{withUI: true, collections: true, wrap: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Conductor-Test-Response") == "denied-stall" {
				// A known denial must clear inspection even if its body never ends.
				// This hook belongs only to the owned test server, not the product.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			next.ServeHTTP(w, r)
		})
	}})
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections(), f.verifier))
	configureCollectionIntegration(t, f.accessFixture, "team", "application", true)
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "private", PrincipalID: "person-reviewer", CanRead: true, CanAuthor: true}}}); err != nil {
		t.Fatal(err)
	}
	// Completed fixtures exceed one page without consuming active admission slots.
	for i := 0; i < 21; i++ {
		c := requestCollection(t, f.accessFixture, "author", "team", "application", "browser-page-"+strings.Repeat("x", i+1))
		completeCollectionFixture(t, f.accessFixture, c)
	}
	selected := requestCollection(t, f.accessFixture, "author", "team", "application", "browser-inspected-receipt")
	receipt := completeCollectionFixture(t, f.accessFixture, selected)
	pkg := f.create("author", "team", "application", domain.Content{"intent": map[string]any{"title": "Shared context browser acceptance"}, "futureField": map[string]any{"retained": true}})
	path := "/api/v1/changes/" + pkg.ID
	f.request("author", "team", "application", http.MethodPost, path+"/review-requests", map[string]any{"revision": 1}, 200, nil)
	f.request("reviewer", "team", "application", http.MethodPost, path+"/approvals", map[string]any{"revision": 1, "digest": pkg.Revision.Digest}, 201, nil)
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/collections.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_CHANGE_ID="+pkg.ID,
		"CONDUCTOR_BROWSER_AUTHOR_TOKEN="+f.tokens["author"], "CONDUCTOR_BROWSER_COLLECTION_ID="+selected.ID,
		"CONDUCTOR_BROWSER_RECEIPT_DIGEST="+receipt.Digest, "CONDUCTOR_BROWSER_SCREENSHOT=/tmp/conductor-context-workbench.png")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser context acceptance failed: %v\n%s", err, output)
	}
	t.Log(string(output))
	var current domain.Package
	f.request("reader", "team", "application", http.MethodGet, path, nil, 200, &current)
	if current.Revision.Number != 3 || current.Approved || current.Revision.Author != "person-reviewer" || !reflect.DeepEqual(current.Revision.Content["futureField"], pkg.Revision.Content["futureField"]) {
		t.Fatal("browser attachment lost exact revision, independent review or package extensions")
	}
	var history domain.RevisionRecord
	f.request("reader", "team", "application", http.MethodGet, path+"/revisions/1", nil, 200, &history)
	if len(history.Approvals) != 1 || history.Approvals[0].Digest != pkg.Revision.Digest {
		t.Fatal("browser attachment discarded historical approval")
	}
	var count, cancelled int
	if err := f.sql.QueryRow(f.ctx, `SELECT count(*),count(cancel_requested_at) FROM context_collections WHERE requester_id='person-reviewer'`).Scan(&count, &cancelled); err != nil {
		t.Fatal(err)
	}
	if count != 1 || cancelled != 1 {
		t.Fatalf("uncertain browser creation or cancellation duplicated work: %d/%d", count, cancelled)
	}
}
