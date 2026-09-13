package acceptance_test

import (
	"net/http"
	"os"
	"os/exec"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
)

func TestBrowserWorkflowNavigation(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for signed browser navigation acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	f := newConfiguredBrowserFixture(t, browserFixtureOptions{withUI: true, collections: true, coordination: true, deliveries: true, runtime: true})
	if err := f.db.ApplyAccessConfig(f.ctx, "synthetic-navigation-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "private", PrincipalID: "person-reader", CanRead: true}}}); err != nil {
		t.Fatal(err)
	}
	pkg := f.create("author", "team", "application", domain.Content{"intent": "Synthetic private navigation inspection"})
	f.request("author", "team", "application", http.MethodPost, "/api/v1/changes/"+pkg.ID+"/review-requests", map[string]any{"revision": 1}, 200, nil)
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/navigation.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_CHANGE_ID="+pkg.ID)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("browser navigation: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
