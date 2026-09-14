package acceptance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

// The real terminal uses signed identities and PostgreSQL. Synthetic proposal
// text proves shared section review, not paid model inference or model quality.
func TestAuthenticatedTerminalAssistance(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TERMINAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TERMINAL=1 for real assistance PTY acceptance")
	}
	if os.Getenv("CONDUCTOR_TEST_DATABASE_URL") == "" {
		t.Fatal("opted-in terminal acceptance requires CONDUCTOR_TEST_DATABASE_URL")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("opted-in terminal acceptance requires Linux PTYs")
	}
	python := os.Getenv("CONDUCTOR_TERMINAL_PYTHON")
	if python == "" {
		python = "python3"
	}
	if _, err := exec.LookPath(python); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "conductor")
	buildCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/conductor")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real CLI: %v\n%s", err, output)
	}
	f := newAccessFixtureWithIssuer(t, nil, nil, 4*time.Minute)
	files := map[string]string{}
	for name, token := range f.tokens {
		path := filepath.Join(directory, name+".token")
		if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		files[name] = path
	}
	encodedFiles, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	p := f.create("author", "team", "application", domain.Content{"title": "Synthetic assisted Change", "intent": "Original outcome", "scope": map[string]any{"legacy": "retain"}, "futureExtension": map[string]any{"retain": []any{true, "unknown"}}})
	setup, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.RawQuery != "" || r.ContentLength != 0 || (r.URL.Path != "/revoke" && r.URL.Path != "/restore") {
			http.Error(w, "invalid fixture command", 400)
			return
		}
		config := domain.AccessConfig{Memberships: []domain.MembershipConfig{{WorkspaceID: "team", PrincipalID: "person-author", Active: r.URL.Path == "/restore"}}}
		if err := f.db.ApplyAccessConfig(r.Context(), "synthetic-terminal-operator", config); err != nil {
			t.Errorf("fixture revocation: %v", err)
			http.Error(w, "fixture failed", 500)
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer control.Close()
	command := exec.CommandContext(f.ctx, python, "../terminal/assistance_review.py")
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "CONDUCTOR_BIN="+binary, "CONDUCTOR_API_URL="+f.server.URL, "CONDUCTOR_TERMINAL_CREDENTIAL_FILES="+string(encodedFiles), "CONDUCTOR_TERMINAL_ASSISTANCE_SETUP="+string(setup), "CONDUCTOR_TERMINAL_CONTROL_URL="+control.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("assistance PTY acceptance: %v\n%s", err, output)
	}
	t.Log(string(output))
}
