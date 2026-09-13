package acceptance_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
	"github.com/phenixrizen/conductor/internal/mcpserver"
	"github.com/phenixrizen/conductor/pkg/client"
)

func TestAuthenticatedFailedTaskArtifactsThroughAPIAndMCP(t *testing.T) {
	f, setup := releaseTerminalFixture(t)
	run := setup["reportRun"].(domain.CoordinationRun)
	receipt := run.Receipts[0]
	q := domain.CoordinationArtifactQuery{RunDigest: run.Digest, TaskID: receipt.TaskID, ArtifactDigest: receipt.ArtifactDigest}
	agent, err := client.NewAuthenticated(f.server.URL, f.tokens["agent"], "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := agent.GetCoordinationArtifact(f.ctx, run.ID, q)
	if err != nil {
		t.Fatal(err)
	}
	var result execution.Result
	if json.Unmarshal(artifact.Artifact, &result) != nil || result.Producer.State != "failed" || len(result.Patches) != 0 || artifact.ArtifactDigest != receipt.ArtifactDigest {
		t.Fatal("failed report became publishing evidence")
	}
	var deliveries int
	if err = f.sql.QueryRow(f.ctx, `SELECT count(*) FROM repository_deliveries WHERE input->>'runId'=$1`, run.ID).Scan(&deliveries); err != nil || deliveries != 0 {
		t.Fatal("report required publication proposal")
	}
	values := url.Values{"runDigest": {q.RunDigest}, "taskId": {q.TaskID}, "artifactDigest": {q.ArtifactDigest}}
	path := "/api/v1/coordination-runs/" + run.ID + "/artifact?" + values.Encode()
	for _, bad := range []string{path + "&actor=reviewer", path + "&runDigest=" + q.RunDigest, strings.Replace(path, "runDigest=", "RunDigest=", 1), strings.Replace(path, "taskId="+q.TaskID, "taskId=bad", 1)} {
		f.request("agent", "team", "application", http.MethodGet, bad, nil, 400, nil)
	}
	f.request("agent", "team", "private", http.MethodGet, path, nil, 404, nil)
	f.raw(f.server.URL, "forged.token", "team", "application", http.MethodGet, path, nil, nil, 401, nil)
	f.raw(f.server.URL, f.tokens["agent"], "team", "application", http.MethodGet, path, nil, http.Header{"X-Conductor-Actor": {"reviewer"}}, 401, nil)
	bridge, err := mcpserver.New(agent)
	if err != nil {
		t.Fatal(err)
	}
	left, right := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- bridge.Run(f.ctx, left) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "task-report-acceptance", Version: "1"}, nil).Connect(f.ctx, right, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close(); <-done }()
	args := map[string]any{"id": run.ID, "runDigest": q.RunDigest, "taskId": q.TaskID, "artifactDigest": q.ArtifactDigest}
	call, err := session.CallTool(f.ctx, &mcp.CallToolParams{Name: "conductor_get_task_artifact", Arguments: args})
	if err != nil || call.IsError {
		t.Fatalf("failed report MCP: %v %+v", err, call)
	}
	raw, _ := json.Marshal(call.StructuredContent)
	var envelope struct {
		Data domain.CoordinationArtifact `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Data.ArtifactDigest != q.ArtifactDigest {
		t.Fatal("MCP lost exact receipt")
	}
	args["actor"] = "reviewer"
	call, err = session.CallTool(f.ctx, &mcp.CallToolParams{Name: "conductor_get_task_artifact", Arguments: args})
	if err != nil || !call.IsError {
		t.Fatal("MCP accepted forged actor")
	}
	delete(args, "actor")
	// Complete large artifacts remain available through the dedicated API. MCP
	// returns an explicit error rather than a seemingly complete truncated report.
	largeRun, e := agent.CreateCoordination(f.ctx, "large-report", run.Plan)
	if e != nil {
		t.Fatal(e)
	}
	var largeTask string
	if e = f.sql.QueryRow(f.ctx, `SELECT id FROM coordination_tasks WHERE run_id=$1`, largeRun.ID).Scan(&largeTask); e != nil {
		t.Fatal(e)
	}
	large := result
	large.Checks = []execution.Evidence{}
	for _, id := range []string{"one", "two", "three"} {
		text := strings.Repeat("x", 1<<20)
		large.Checks = append(large.Checks, execution.Evidence{ID: id, State: "failed", Output: text, OutputDigest: execution.Sum([]byte(text))})
	}
	largeDigest, _ := domain.JSONDigest(large)
	if _, e = f.sql.Exec(f.ctx, `INSERT INTO coordination_task_receipts(task_id,run_id,digest,outcome,artifact_digest,artifact) VALUES($1,$2,$3,'failed',$3,$4)`, largeTask, largeRun.ID, largeDigest, large); e != nil {
		t.Fatal(e)
	}
	largeQ := domain.CoordinationArtifactQuery{RunDigest: largeRun.Digest, TaskID: largeTask, ArtifactDigest: largeDigest}
	complete, e := agent.GetCoordinationArtifact(f.ctx, largeRun.ID, largeQ)
	if e != nil || len(complete.Artifact) < 3<<20 {
		t.Fatal("large API artifact silently truncated", e)
	}
	tooLarge, e := session.CallTool(f.ctx, &mcp.CallToolParams{Name: "conductor_get_task_artifact", Arguments: map[string]any{"id": largeRun.ID, "runDigest": largeQ.RunDigest, "taskId": largeTask, "artifactDigest": largeDigest}})
	if e != nil || !tooLarge.IsError || len(tooLarge.Content) == 0 {
		t.Fatal("oversized MCP artifact looked complete")
	}
	text, ok := tooLarge.Content[0].(*mcp.TextContent)
	if !ok || !strings.Contains(text.Text, "response_too_large") {
		t.Fatal("missing explicit MCP size error")
	}
	if err = f.db.ApplyAccessConfig(f.ctx, "synthetic-revocation", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "application", PrincipalID: "person-agent"}}}); err != nil {
		t.Fatal(err)
	}
	call, err = session.CallTool(f.ctx, &mcp.CallToolParams{Name: "conductor_get_task_artifact", Arguments: args})
	if err != nil || !call.IsError {
		t.Fatal("MCP report survived read revocation")
	}
}
