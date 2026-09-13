package acceptance_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// Actual PTYs drive the compiled CLI and signed-issuer API against an isolated
// PostgreSQL schema. Receipt fixtures use the trusted store completion command;
// this interface acceptance does not claim provider or Temporal execution.
func TestAuthenticatedTerminalCollections(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_TERMINAL") != "1" {
		t.Skip("set CONDUCTOR_TEST_TERMINAL=1 to run actual authenticated collection PTY acceptance")
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
		t.Fatal("opted-in terminal acceptance requires Python 3")
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
	f := collectionFixture(t)
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
	pkg := f.create("author", "team", "application", domain.Content{"intent": "Synthetic terminal collection attachment", "futureExtension": map[string]any{"retain": []any{true, "unknown"}}})
	collection := requestCollection(t, f, "author", "team", "application", "terminal-shared-receipt")
	receipt := completeCollectionFixture(t, f, collection)
	// More than one page proves bounded navigation through actual shared rows.
	for i := 0; i < 21; i++ {
		c := requestCollection(t, f, "author", "team", "application", fmt.Sprintf("terminal-page-%d", i))
		completeCollectionFixture(t, f, c)
	}
	setup, err := json.Marshal(map[string]any{"package": pkg, "collection": collection, "receipt": receipt})
	if err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.ContentLength != 0 {
			http.Error(w, "invalid fixture control", 400)
			return
		}
		if r.URL.Path != "/revoke" && r.URL.Path != "/restore" {
			http.NotFound(w, r)
			return
		}
		config := domain.AccessConfig{Memberships: []domain.MembershipConfig{{WorkspaceID: "team", PrincipalID: "person-reviewer", Active: r.URL.Path == "/restore"}}}
		if err := f.db.ApplyAccessConfig(r.Context(), "synthetic-terminal-operator", config); err != nil {
			t.Errorf("apply fixture revocation: %v", err)
			http.Error(w, "fixture update failed", 500)
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer control.Close()
	command := exec.CommandContext(f.ctx, python, "../terminal/collection_review.py")
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "CONDUCTOR_API_URL="+f.server.URL, "CONDUCTOR_BIN="+binary, "CONDUCTOR_TERMINAL_CREDENTIAL_FILES="+string(paths), "CONDUCTOR_TERMINAL_COLLECTION_SETUP="+string(setup), "CONDUCTOR_TERMINAL_CONTROL_URL="+control.URL)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("authenticated collection PTY acceptance failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
