package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/execution"
)

func TestTaskArtifactsPreserveFailedAndReadOnlyEvidenceWithAllSourceAccess(t *testing.T) {
	ctx, p, s, plan := coordinationFixture(t)
	viewer := accessContext(ctx, "worker", "workspace-one", "repo-one")
	run, err := s.CreateCoordination(viewer, "inspect-reports", plan)
	if err != nil {
		t.Fatal(err)
	}
	// These persisted report fixtures test read authorization/integrity. They do
	// not assert that a synthetic worker executed or that checks passed.
	var taskID string
	if err = p.pool.QueryRow(ctx, `SELECT id FROM coordination_tasks WHERE run_id=$1 AND task_key='service'`, run.ID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	report := execution.Result{InputDigest: strings.Repeat("a", 64), ProfileDigest: plan.Tasks[0].ProfileDigest, Image: plan.Tasks[0].Image, Adapter: "command/v1", AdapterVersion: "1", Producer: execution.Evidence{State: "failed", Output: "Synthetic design report and failed verification\x1b]52;c;data\a"}, Patches: []execution.Patch{}, Checks: []execution.Evidence{{ID: "design", State: "failed", Output: "Synthetic failed design check"}}}
	digest, _ := domain.JSONDigest(report)
	if _, err = p.pool.Exec(ctx, `INSERT INTO coordination_task_receipts(task_id,run_id,digest,outcome,artifact_digest,artifact) VALUES($1,$2,$3,'failed',$3,$4)`, taskID, run.ID, digest, report); err != nil {
		t.Fatal(err)
	}
	q := domain.CoordinationArtifactQuery{RunDigest: run.Digest, TaskID: taskID, ArtifactDigest: digest}
	got, err := s.GetCoordinationArtifact(viewer, run.ID, q)
	if err != nil || got.RunID != run.ID || got.TaskID != taskID || got.ArtifactDigest != digest {
		t.Fatalf("retained failed report unavailable: %+v %v", got, err)
	}
	var decoded execution.Result
	if json.Unmarshal(got.Artifact, &decoded) != nil || decoded.Producer.State != "failed" || len(decoded.Patches) != 0 || decoded.Producer.Output != report.Producer.Output {
		t.Fatal("failed/read-only evidence altered")
	}
	for _, changed := range []domain.CoordinationArtifactQuery{{RunDigest: strings.Repeat("f", 64), TaskID: taskID, ArtifactDigest: digest}, {RunDigest: run.Digest, TaskID: taskID, ArtifactDigest: strings.Repeat("f", 64)}} {
		if _, err = s.GetCoordinationArtifact(viewer, run.ID, changed); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("uninspected pin accepted: %v", err)
		}
	}
	missing := q
	missing.TaskID = strings.Repeat("f", 32)
	if _, err = s.GetCoordinationArtifact(viewer, run.ID, missing); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("foreign task accepted")
	}
	for _, scope := range [][2]string{{"workspace-two", "repo-other"}, {"workspace-one", "repo-three"}} {
		if _, err = s.GetCoordinationArtifact(accessContext(ctx, "worker", scope[0], scope[1]), run.ID, q); err == nil {
			t.Fatal("cross-scope report exposed")
		}
	}
	// Historical reports remain readable after execution authority/profile removal.
	if _, err = p.pool.Exec(ctx, `UPDATE execution_grants SET can_execute=false; UPDATE execution_profiles SET enabled=false`); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetCoordinationArtifact(viewer, run.ID, q)
	if err != nil || !bytes.Equal(again.Artifact, got.Artifact) {
		t.Fatal("execution revocation erased review evidence")
	}
	// An already admitted read holds related-repository grants through commit.
	var identity domain.AccessIdentity
	if err = p.pool.QueryRow(ctx, `SELECT issuer,subject FROM access_principals WHERE id='agent-worker'`).Scan(&identity.Issuer, &identity.Subject); err != nil {
		t.Fatal(err)
	}
	access, err := p.BeginAccess(ctx, domain.AccessRequest{Identity: identity, WorkspaceID: "workspace-one", RepositoryID: "repo-one"})
	if err != nil {
		t.Fatal(err)
	}
	gate := access.(*Postgres)
	defer gate.Rollback(ctx)
	if _, err = gate.InspectCoordinationArtifact(ctx, run.ID, q); err != nil {
		t.Fatal(err)
	}
	revoked := make(chan error, 1)
	go func() {
		_, err := p.pool.Exec(ctx, `UPDATE repository_grants SET can_read=false,can_author=false,can_approve=false WHERE repository_id='repo-two' AND principal_id='agent-worker'`)
		revoked <- err
	}()
	select {
	case err := <-revoked:
		t.Fatalf("related grant not held through read: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	if err = gate.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-revoked; err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetCoordinationArtifact(viewer, run.ID, q); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoked related source visible: %v", err)
	}
}
func TestTaskArtifactRejectsCorruptTypedDocument(t *testing.T) {
	for _, mode := range []string{"digest", "unknown_field"} {
		t.Run(mode, func(t *testing.T) {
			ctx, p, s, plan := coordinationFixture(t)
			viewer := accessContext(ctx, "worker", "workspace-one", "repo-one")
			run, err := s.CreateCoordination(viewer, "corrupt-report", plan)
			if err != nil {
				t.Fatal(err)
			}
			result := execution.Result{Producer: execution.Evidence{State: "failed"}}
			digest, _ := domain.JSONDigest(result)
			raw, _ := json.Marshal(result)
			if mode == "digest" {
				digest = strings.Repeat("f", 64)
			} else {
				raw = append(raw[:len(raw)-1], []byte(`,"unknownReport":"must not disappear"}`)...)
			}
			var taskID string
			if err = p.pool.QueryRow(ctx, `SELECT id FROM coordination_tasks WHERE run_id=$1 LIMIT 1`, run.ID).Scan(&taskID); err != nil {
				t.Fatal(err)
			}
			if _, err = p.pool.Exec(ctx, `INSERT INTO coordination_task_receipts(task_id,run_id,digest,outcome,artifact_digest,artifact) VALUES($1,$2,$3,'failed',$3,$4)`, taskID, run.ID, digest, json.RawMessage(raw)); err != nil {
				t.Fatal(err)
			}
			if _, err = s.GetCoordinationArtifact(viewer, run.ID, domain.CoordinationArtifactQuery{RunDigest: run.Digest, TaskID: taskID, ArtifactDigest: digest}); !errors.Is(err, domain.ErrUnavailable) {
				t.Fatalf("corrupt evidence exposed: %v", err)
			}
		})
	}
}
