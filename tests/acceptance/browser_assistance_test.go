package acceptance_test

import (
	"net/http"
	"os"
	"os/exec"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
)

// Native text is synthetic; this tests signed browser/agent HTTP transport and
// shared persistence. The separate MCP suite qualifies actual stdio submission.
func TestBrowserDesignAssistance(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_BROWSER") != "1" {
		t.Skip("set CONDUCTOR_TEST_BROWSER=1 for signed browser assistance acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in browser acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	f := newBrowserFixture(t, true)
	pkg := f.create("author", "team", "application", domain.Content{
		"title": "Synthetic assisted Design", "intent": "Review selected native assistant suggestions",
		"scope": nil, "design": "Existing Design", "tasks": []any{"preserve structured work"},
		"extension": map[string]any{"future": []any{1.0, nil, ""}}, "emptyExtension": "",
	})
	path := "/api/v1/changes/" + pkg.ID
	f.request("author", "team", "application", http.MethodPost, path+"/review-requests", map[string]any{"revision": 1}, 200, nil)
	f.request("reviewer", "team", "application", http.MethodPost, path+"/approvals", map[string]any{"revision": 1, "digest": pkg.Revision.Digest}, 201, nil)
	python := os.Getenv("CONDUCTOR_BROWSER_PYTHON")
	if python == "" {
		python = "python3"
	}
	command := exec.CommandContext(f.ctx, python, "../browser/design_assistance.py")
	command.Env = append(os.Environ(), "CONDUCTOR_BROWSER_WEB_URL="+f.app.URL, "CONDUCTOR_BROWSER_CHANGE_ID="+pkg.ID,
		"CONDUCTOR_BROWSER_AUTHOR_TOKEN="+f.tokens["author"], "CONDUCTOR_BROWSER_AGENT_TOKEN="+f.tokens["agent"])
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("browser assistance: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
