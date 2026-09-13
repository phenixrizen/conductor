package client

import (
	"context"
	"encoding/json"
	"github.com/phenixrizen/conductor/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTaskArtifactUsesExactPinsAndDedicatedBound(t *testing.T) {
	q := domain.CoordinationArtifactQuery{RunDigest: strings.Repeat("a", 64), TaskID: strings.Repeat("b", 32), ArtifactDigest: strings.Repeat("c", 64)}
	id := strings.Repeat("d", 32)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/api/v1/coordination-runs/"+id+"/artifact" || r.URL.Query().Get("runDigest") != q.RunDigest || r.URL.Query().Get("taskId") != q.TaskID || r.URL.Query().Get("artifactDigest") != q.ArtifactDigest || r.Header.Get("X-Conductor-Repository") != "repo" {
			t.Error("artifact scope/pins changed")
		}
		raw, _ := json.Marshal(map[string]string{"report": strings.Repeat("x", 3<<20)})
		_ = json.NewEncoder(w).Encode(domain.CoordinationArtifact{RunID: id, RunDigest: q.RunDigest, TaskID: q.TaskID, ArtifactDigest: q.ArtifactDigest, Artifact: raw})
	}))
	defer server.Close()
	c, err := NewAuthenticated(server.URL, "synthetic-token", "team", "repo")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.GetCoordinationArtifact(context.Background(), id, q)
	if err != nil || len(got.Artifact) < 3<<20 || calls != 1 {
		t.Fatalf("complete artifact bound: %d %v", calls, err)
	}
	invalid := q
	invalid.ArtifactDigest = ""
	if _, err = c.GetCoordinationArtifact(context.Background(), id, invalid); err == nil || calls != 1 {
		t.Fatal("invalid pin reached server")
	}
}
