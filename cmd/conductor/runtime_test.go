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
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/reviewinput"
	"github.com/phenixrizen/conductor/pkg/client"
)

func TestRuntimeCLIExactOfflinePreviewAndCapturedRetry(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	input := domain.RuntimeInput{DeliveryID: strings.Repeat("a", 32), DeliveryDigest: strings.Repeat("b", 64), ObservationSequence: 1, DeploymentID: "deployment", Commit: strings.Repeat("c", 40), Environment: "production", Start: now.Add(-time.Minute), End: now.Add(-30 * time.Second), Requirements: []domain.RuntimeRequirement{}}
	raw, _ := json.Marshal(map[string]any{"idempotencyKey": "runtime-key", "input": input})
	path := filepath.Join(t.TempDir(), "request.json")
	os.WriteFile(path, raw, 0600)
	calls := []string{}
	keys := []string{}
	bodies := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		var in json.RawMessage
		json.NewDecoder(r.Body).Decode(&in)
		bodies = append(bodies, string(in))
		if r.Method == "POST" {
			w.WriteHeader(503)
			w.Write([]byte(`{"code":"unavailable","message":"synthetic lost result"}`))
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c, err := client.NewAuthenticated(srv.URL, "synthetic-token", "team", "repo")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := runReleaseCommand(context.Background(), nil, "runtime-preview", nil, releaseOptions{file: path})
	if err != nil || len(calls) != 0 {
		t.Fatal(err)
	}
	d := preview.(reviewinput.Draft)
	if _, err = runReleaseCommand(context.Background(), c, "runtime-collect", nil, releaseOptions{file: path, digest: "wrong"}); err == nil || len(calls) != 0 {
		t.Fatal("wrong digest sent")
	}
	for i := 0; i < 2; i++ {
		if _, err = runReleaseCommand(context.Background(), c, "runtime-collect", nil, releaseOptions{file: path, digest: d.Digest}); err == nil {
			t.Fatal("unavailable became success")
		}
	}
	if len(calls) != 2 || calls[0] != "POST /api/v1/runtime-evidence" || calls[1] != calls[0] || keys[0] != "runtime-key" || keys[1] != keys[0] || bodies[0] != bodies[1] {
		t.Fatal("retry changed input or performed refresh")
	}
	if _, err = runReleaseCommand(context.Background(), c, "runtime-evidence", nil, releaseOptions{limit: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err = runReleaseCommand(context.Background(), c, "runtime", []string{strings.Repeat("a", 32)}, releaseOptions{limit: 1}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 || !strings.HasPrefix(calls[2], "GET /api/v1/runtime-evidence?") || calls[3] != "GET /api/v1/runtime-evidence/"+strings.Repeat("a", 32) {
		t.Fatal(calls)
	}
}
