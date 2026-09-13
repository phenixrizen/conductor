package acceptance_test

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/service"
)

func TestBrowserFailedTaskArtifact(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for task artifact browser acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	f := newConfiguredBrowserFixture(t, browserFixtureOptions{withUI: true, collections: true, coordination: true, deliveries: true})
	f.server.Close()
	f.server = httptest.NewServer(api.NewAuthenticated(service.NewAuthenticated(f.db).WithCollections().WithCoordination().WithDeliveries(), f.verifier))
	configureCollectionIntegration(t, f.accessFixture, "team", "application", true)
	input, c := seedDeliveryInput(t, f.accessFixture, []byte("Unrelated synthetic patch fixture"))
	original, err := c.GetCoordination(f.ctx, input.RunID)
	if err != nil {
		t.Fatal(err)
	}
	plan := original.Plan
	plan.Tasks[0].Scopes[0].WritablePaths = []string{}
	plan.Tasks[0].Checks = []domain.VerificationCommand{}
	run, err := c.CreateCoordination(f.ctx, "failed-browser-report", plan)
	if err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err = f.sql.QueryRow(f.ctx, `SELECT id FROM coordination_tasks WHERE run_id=$1`, run.ID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	output := "Synthetic failed design report <script>window.reportInjected=true</script> \x1b[2J \u202e"
	exit := 1
	artifact := execution.Result{CleanupConfirmed: false, ProfileDigest: plan.Tasks[0].ProfileDigest, Image: plan.Tasks[0].Image, Adapter: "command/v1", AdapterVersion: "1", InputDigest: strings.Repeat("d", 64), Producer: execution.Evidence{State: "failed", ExitCode: &exit, Output: output, OutputDigest: execution.Sum([]byte(output)), SourceDigest: strings.Repeat("d", 64), StartedAt: now, FinishedAt: now}, Patches: []execution.Patch{}, Checks: []execution.Evidence{}, StartedAt: now, FinishedAt: now}
	digest, err := domain.JSONDigest(artifact)
	if err != nil {
		t.Fatal(err)
	}
	// This retained failed receipt tests browser inspection. Actual isolated
	// producer execution and cleanup reconciliation have separate acceptance.
	if _, err = f.sql.Exec(f.ctx, `INSERT INTO coordination_task_receipts(task_id,run_id,digest,outcome,artifact_digest,artifact) VALUES($1,$2,$3,'failed',$3,$4)`, taskID, run.ID, digest, artifact); err != nil {
		t.Fatal(err)
	}
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/task_artifact.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_REPORT_RUN="+run.ID, "CONDUCTOR_BROWSER_REPORT_DIGEST="+run.Digest, "CONDUCTOR_BROWSER_REPORT_TASK="+taskID, "CONDUCTOR_BROWSER_REPORT_ARTIFACT="+digest)
	result, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("task artifact browser: %v\n%s", err, result)
	}
	t.Log(string(result))
	var deliveries int
	if err = f.sql.QueryRow(f.ctx, `SELECT count(*) FROM repository_deliveries WHERE input->>'runId'=$1`, run.ID).Scan(&deliveries); err != nil || deliveries != 0 {
		t.Fatal("task inspection created a delivery proposal")
	}
}
