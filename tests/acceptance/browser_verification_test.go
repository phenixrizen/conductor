package acceptance_test

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

func TestBrowserVerificationCriterionSupport(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for criterion inspection acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("browser opt-in requires a test database")
	}
	f := newConfiguredBrowserFixture(t, browserFixtureOptions{withUI: true, collections: true, coordination: true, deliveries: true})
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries(), f.verifier))
	configureCollectionIntegration(t, f.accessFixture, "team", "application", true)
	input, c := seedDeliveryInputWithContent(t, f.accessFixture, []byte("Synthetic artifact; actual producer is qualified separately"), domain.Content{"intent": "Synthetic linked criterion review", "verificationCriteria": domain.VerificationCriteria{SchemaVersion: 1, Criteria: []domain.VerificationCriterion{{ID: "synthetic-output", Description: "Synthetic explicit criterion <script>window.criterionInjected=true</script>"}, {ID: "unlinked", Description: "Another criterion needs its own evidence."}}}}, true)
	run, err := c.GetCoordination(f.ctx, input.RunID)
	if err != nil {
		t.Fatal(err)
	}
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/verification.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_CRITERIA_RUN="+run.ID)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser criterion inspection: %v\n%s", err, output)
	}
	t.Log(string(output))
}
