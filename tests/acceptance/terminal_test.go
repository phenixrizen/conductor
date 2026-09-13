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

// This suite owns its compiled CLI, signed identity issuer, API, credential files,
// and isolated database schema. The Python proxy observes real requests; it never
// substitutes package responses or grants authority to the terminal.
func TestAuthenticatedTerminalWorkbench(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TERMINAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TERMINAL=1 to run actual authenticated PTY acceptance")
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
		t.Fatal("opted-in terminal acceptance requires a Python 3 executable")
	}
	directory := t.TempDir()
	binary := filepath.Join(directory, "conductor")
	buildCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/conductor")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real conductor CLI: %v\n%s", err, output)
	}
	f := newAccessFixture(t)
	credentials := make(map[string]string)
	for name, token := range f.tokens {
		path := filepath.Join(directory, name+".token")
		if err := os.WriteFile(path, []byte(token+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		credentials[name] = path
	}
	paths, err := json.Marshal(credentials)
	if err != nil {
		t.Fatal(err)
	}
	// This isolated fixture-only endpoint exposes fixed revocation actions. It is
	// not a Conductor administration API: all updates use the trusted store method
	// against this test's schema, and the server disappears with the test.
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.ContentLength != 0 {
			http.Error(w, "invalid fixture control request", http.StatusBadRequest)
			return
		}
		var config domain.AccessConfig
		switch r.URL.Path {
		case "/reviewer-read-only", "/reviewer-full-grant":
			full := r.URL.Path == "/reviewer-full-grant"
			config.Grants = []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-reviewer", CanRead: true, CanAuthor: full, CanApprove: full}}
		case "/reviewer-no-membership", "/reviewer-membership":
			config.Memberships = []domain.MembershipConfig{{WorkspaceID: "team", PrincipalID: "person-reviewer", Active: r.URL.Path == "/reviewer-membership"}}
		case "/reviewer-inactive":
			config.Principals = []domain.PrincipalConfig{{ID: "person-reviewer", Issuer: f.issuer.url, Subject: "subject-reviewer", Kind: "human", Active: false}}
		default:
			http.NotFound(w, r)
			return
		}
		if err := f.db.ApplyAccessConfig(r.Context(), "synthetic-terminal-operator", config); err != nil {
			t.Errorf("apply terminal fixture access update: %v", err)
			http.Error(w, "fixture update failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	}))
	t.Cleanup(control.Close)
	command := exec.CommandContext(f.ctx, python, "../terminal/authenticated.py")
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "CONDUCTOR_API_URL="+f.server.URL, "CONDUCTOR_BIN="+binary, "CONDUCTOR_TERMINAL_CREDENTIAL_FILES="+string(paths), "CONDUCTOR_TERMINAL_CONTROL_URL="+control.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("authenticated terminal acceptance failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
