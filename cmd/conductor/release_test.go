package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/reviewinput"
	"github.com/phenixrizen/conductor/pkg/client"
)

func TestReleaseCLIUsesExactPreviewAndInspectedCommands(t *testing.T) {
	var calls []string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, err := client.NewAuthenticated(srv.URL, "synthetic-token", "team", "repo")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "graph.json")
	raw := `{"idempotencyKey":"shared-input-1","input":{"sources":[{"repositoryId":"repo","collectionId":"` + strings.Repeat("a", 32) + `","digest":"` + strings.Repeat("b", 64) + `"}]}}`
	if err = os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	preview, err := runReleaseCommand(context.Background(), nil, "graph-preview", nil, releaseOptions{file: path})
	if err != nil {
		t.Fatal(err)
	}
	d := preview.(reviewinput.Draft)
	if len(calls) != 0 {
		t.Fatal("preview performed API I/O")
	}
	if _, err = runReleaseCommand(context.Background(), c, "graph-create", nil, releaseOptions{file: path, digest: "wrong"}); err == nil || len(calls) != 0 {
		t.Fatal("uninspected input sent")
	}
	if _, err = runReleaseCommand(context.Background(), c, "graph-create", nil, releaseOptions{file: path, digest: d.Digest}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "POST /api/v1/repository-graphs" || body["sources"] == nil {
		t.Fatal(calls, body)
	}
	calls = nil
	digest := strings.Repeat("f", 64)
	id := strings.Repeat("a", 32)
	if _, err = runReleaseCommand(context.Background(), c, "run-authorize", []string{id}, releaseOptions{limit: 20, digest: digest}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "POST /api/v1/coordination-runs/"+id+"/authorization" || body["digest"] != digest {
		t.Fatal("authorization refreshed or substituted input", calls, body)
	}
}
func TestReleaseCLIRequiresFixedScopeAndPreviewIsOffline(t *testing.T) {
	t.Setenv("CONDUCTOR_TOKEN", "synthetic-token")
	t.Setenv("CONDUCTOR_TOKEN_FILE", "unavailable")
	if c, err := clientForCommand("run-preview", "", false, "", ""); err != nil || c != nil {
		t.Fatal("offline preview read credential configuration", err)
	}
	_ = os.Unsetenv("CONDUCTOR_TOKEN_FILE")
	if _, err := clientForCommand("runs", "", false, "team", ""); err == nil {
		t.Fatal("missing selected repository accepted")
	}
	if _, err := clientForCommand("tracker-link", "person", true, "team", "repo"); err == nil {
		t.Fatal("local actor mixed with credential")
	}
}
