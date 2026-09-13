package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Run the real CLI entry point in a subprocess so flag/environment selection and
// the JSON output path are exercised without os.Exit ending the parent test.
func TestCLIProcess(t *testing.T) {
	if os.Getenv("CONDUCTOR_TEST_CLI_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"conductor"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestAuthenticatedCLICommandsUseScopeAndPrintJSON(t *testing.T) {
	cleanCredentialEnvironment(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic.token" || len(r.Header.Values("X-Conductor-Actor")) != 0 {
			t.Error("CLI credential mode was not preserved")
		}
		switch r.URL.Path {
		case "/api/v1/session":
			if r.Header.Get("X-Conductor-Workspace") != "env-workspace" || r.Header.Get("X-Conductor-Repository") != "env-repo" {
				t.Error("environment scope missing")
			}
			_, _ = w.Write([]byte(`{"principal":{"id":"person-1","kind":"human"},"workspaces":[],"truncated":false}`))
		case "/api/v1/repositories":
			if r.Header.Get("X-Conductor-Workspace") != "flag-workspace" || r.Header.Get("X-Conductor-Repository") != "flag-repo" {
				t.Error("scope flag did not override environment")
			}
			_, _ = w.Write([]byte(`{"repositories":[],"truncated":false}`))
		default:
			t.Errorf("unexpected CLI route: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("synthetic.token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"session"}, {"repositories", "--workspace", "flag-workspace", "--repository-id", "flag-repo"}} {
		command := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)...)
		command.Env = append(os.Environ(), "CONDUCTOR_TEST_CLI_PROCESS=1", "CONDUCTOR_URL="+server.URL, "CONDUCTOR_TOKEN_FILE="+path, "CONDUCTOR_WORKSPACE=env-workspace", "CONDUCTOR_REPOSITORY_ID=env-repo")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI command failed: %v %s", err, output)
		}
		var decoded map[string]any
		if json.Unmarshal(output, &decoded) != nil || decoded["truncated"] != false {
			t.Fatalf("CLI did not print the API JSON: %s", output)
		}
		if strings.Contains(string(output), "synthetic.token") {
			t.Fatal("CLI printed the credential")
		}
	}
}

func TestContentFileBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		valid       bool
	}{
		{"unknown structured fields", `{"intent":"synthetic","future":{"items":[true,"retained"]}}`, true},
		{"multiple values", `{"intent":"synthetic"} {}`, false},
		{"null", `null`, false},
		{"array", `[]`, false},
		{"over limit with whitespace", `{"intent":"synthetic"}` + strings.Repeat(" ", 1<<20), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "package.json")
			if err := os.WriteFile(path, []byte(tc.input), 0600); err != nil {
				t.Fatal(err)
			}
			content, err := contentFrom(path, "")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, got content=%v, err=%v", tc.valid, content, err)
			}
			if tc.valid && content["future"] == nil {
				t.Fatal("unknown content was lost")
			}
		})
	}
}
