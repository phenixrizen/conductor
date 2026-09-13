package acceptance_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/pkg/client"
)

// Real signed login, API, retained PostgreSQL facts and Chromium; these seeded
// artifacts do not assert actual coding or live GitHub/GitLab publication.
func TestBrowserRepositoryDeliveries(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for delivery browser acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	var f *browserFixture
	wrap := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			recorded := httptest.NewRecorder()
			next.ServeHTTP(recorded, r)
			// Controlled provider observations exercise browser rendering and reconciliation.
			// The actual provider adapters have separate real HTTP protocol acceptance.
			if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/authorizations") && recorded.Code == 202 {
				var d domain.Delivery
				if err := json.Unmarshal(recorded.Body.Bytes(), &d); err != nil {
					t.Error(err)
					return
				}
				var binding, operation string
				if err := f.sql.QueryRow(f.ctx, `SELECT d.binding,o.binding FROM repository_deliveries d JOIN delivery_outbox o ON o.delivery_id=d.id WHERE d.id=$1 AND o.operation='publish'`, d.ID).Scan(&binding, &operation); err != nil {
					t.Error(err)
					return
				}
				observation := domain.DeliveryObservation{ProviderID: "synthetic-browser-pr", Number: 1, URL: "https://github.com/synthetic/application/pull/1", Commit: strings.Repeat("e", 40), Tree: d.ResultTree, State: "draft", Draft: true, Checks: []domain.ProviderCheck{}, ChecksState: "unknown", Deployment: "unavailable", Deployments: []domain.ProviderDeployment{}, ProductionOutcome: "not_observed", ObservedAt: time.Now().UTC().Add(-time.Hour)}
				if _, err := f.db.CompleteDeliveryOperation(f.ctx, operation, binding, observation); err != nil {
					t.Error(err)
					return
				}
			}
			for key, values := range recorded.Header() {
				w.Header()[key] = values
			}
			w.WriteHeader(recorded.Code)
			_, _ = w.Write(recorded.Body.Bytes())
		})
	}
	f = newConfiguredBrowserFixture(t, browserFixtureOptions{withUI: true, collections: true, coordination: true, deliveries: true, wrap: wrap})
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries(), f.verifier))
	configureCollectionIntegration(t, f.accessFixture, "team", "application", true)
	// The complete base64 artifact exceeds the ordinary response limit. Review
	// must consume all bytes and check their digest, never silently slice a patch.
	patch := []byte("<script>window.untrustedExecuted=true</script>\n\x1b[2J\n" + strings.Repeat("synthetic patch evidence\n", 180000))
	input, _ := seedDeliveryInput(t, f.accessFixture, patch)
	agent, err := client.NewAuthenticated(f.server.URL, f.tokens["agent"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	d, err := agent.CreateDelivery(f.ctx, "browser-agent-publication", input)
	if err != nil {
		t.Fatal(err)
	}
	input.Title = "Browser publication with exact retry"
	encoded, _ := json.Marshal(input)
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/delivery.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_DELIVERY_ID="+d.ID, "CONDUCTOR_BROWSER_DELIVERY_DIGEST="+d.Digest, "CONDUCTOR_BROWSER_DELIVERY_INPUT="+string(encoded), "CONDUCTOR_BROWSER_PATCH_BYTES="+stringMustJSON(len(patch)))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser delivery acceptance: %v\n%s", err, output)
	}
	t.Log(string(output))
	var count, authorized int
	if err = f.sql.QueryRow(f.ctx, `SELECT count(*),count(a.delivery_id) FROM repository_deliveries d LEFT JOIN delivery_authorizations a ON a.delivery_id=d.id WHERE d.proposer_id='person-reviewer'`).Scan(&count, &authorized); err != nil {
		t.Fatal(err)
	}
	if count != 1 || authorized != 1 {
		t.Fatalf("browser publication duplicate/lost decisions: %d/%d", count, authorized)
	}
}
func stringMustJSON(value any) string { b, _ := json.Marshal(value); return string(b) }
