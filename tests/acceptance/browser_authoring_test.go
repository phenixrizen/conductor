package acceptance_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

func TestBrowserChangeAuthoring(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for signed browser authoring acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	f := newBrowserFixture(t, true)
	// The same isolated schema also hosts the explicit local-development surface.
	dist, err := filepath.Abs("../../apps/web/dist")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/", api.New(service.New(f.db)))
	mux.Handle("/", http.FileServer(http.Dir(dist)))
	local := httptest.NewServer(mux)
	t.Cleanup(local.Close)
	legacy := f.create("author", "team", "application", domain.Content{
		"title": map[string]any{"legacy": "Structured title"}, "intent": map[string]any{"outcome": "Structured intent"},
		"scope": nil, "design": "", "tasks": []any{"legacy task"}, "verification": false,
		"extension": map[string]any{"keep": []any{1.0, nil, ""}}, "emptyExtension": "",
	})
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/change_authoring.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_LOCAL_URL="+local.URL, "CONDUCTOR_BROWSER_LEGACY_ID="+legacy.ID, "CONDUCTOR_BROWSER_AUTHOR_TOKEN="+f.tokens["author"])
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("browser authoring: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
