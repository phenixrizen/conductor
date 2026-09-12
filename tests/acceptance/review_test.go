package acceptance_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/phenixrizen/conductor/internal/api"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/repositorycontext"
	"github.com/phenixrizen/conductor/internal/service"
	"github.com/phenixrizen/conductor/internal/store"
	"github.com/phenixrizen/conductor/pkg/client"
)

// This test crosses independent HTTP clients, the real API/service, and an
// isolated PostgreSQL schema. It never substitutes in-memory review behavior.
func TestSharedRepositoryReviewWorkflow(t *testing.T) {
	ctx, serverURL := reviewServer(t)
	author := reviewClient(t, serverURL, "synthetic-author")
	reviewer := reviewClient(t, serverURL, "synthetic-reviewer")
	const repository = "synthetic/team & service"
	snapshot := committedContext(t, ctx, repository)
	contextContent := jsonObject(t, snapshot)
	contextContent["futureAdapterField"] = map[string]any{"preserve": true}
	contextContent["artifacts"].([]any)[0].(map[string]any)["futureArtifactField"] = []any{"retained"}
	content := domain.Content{
		"intent":            map[string]any{"request": "Review the existing synthetic design"},
		"repositoryContext": contextContent,
		"futurePackageField": map[string]any{
			"enabled": true, "items": []any{"retain", map[string]any{"nested": "extension"}},
		},
	}
	created, err := author.Create(ctx, content)
	if err != nil {
		t.Fatalf("author creates package: %v", err)
	}
	if created.Revision.Number != 1 || created.Approved || !reflect.DeepEqual(created.Revision.Content, content) {
		t.Fatalf("creation lost package content or initial state: %+v", created)
	}
	otherContext := jsonObject(t, snapshot)
	otherContext["repository"] = "synthetic/other-repository"
	other, err := author.Create(ctx, domain.Content{"intent": "Unrelated package", "repositoryContext": otherContext})
	if err != nil {
		t.Fatalf("create unrelated repository package: %v", err)
	}

	// The reviewer starts with repository identity, without receiving the package
	// ID through shared client memory or a database read.
	discovered, err := reviewer.ListChanges(ctx, repository, "", 20)
	if err != nil || len(discovered.Changes) != 1 {
		t.Fatalf("reviewer discovers shared package: %+v err=%v", discovered, err)
	}
	summary := discovered.Changes[0]
	if summary.ID != created.ID || summary.Repository != repository || summary.Revision != 1 || summary.Digest != created.Revision.Digest || summary.Author != "synthetic-author" || summary.Approved {
		t.Fatalf("discovery did not describe the created package: %+v", summary)
	}
	inspected, err := reviewer.Get(ctx, summary.ID)
	if err != nil || !reflect.DeepEqual(inspected.Revision.Content, content) {
		t.Fatalf("reviewer cannot inspect exact shared content: %+v err=%v", inspected, err)
	}
	if err := domain.ValidateRepositoryContext(inspected.Revision.Content["repositoryContext"]); err != nil {
		t.Fatalf("persisted context lost valid provenance: %v", err)
	}
	if _, err := author.Submit(ctx, created.ID, created.Revision.Number); err != nil {
		t.Fatalf("author submits inspected revision: %v", err)
	}
	inspected, err = reviewer.Get(ctx, summary.ID)
	if err != nil || inspected.Revision.SubmittedAt == nil || inspected.Revision.Digest != created.Revision.Digest {
		t.Fatalf("reviewer cannot inspect submitted revision: %+v err=%v", inspected, err)
	}
	_, err = author.Approve(ctx, inspected.ID, inspected.Revision.Number, inspected.Revision.Digest)
	assertAPIError(t, err, http.StatusUnprocessableEntity, "approval_rejected")
	approved, err := reviewer.Approve(ctx, inspected.ID, inspected.Revision.Number, inspected.Revision.Digest)
	if err != nil || !approved.Approved || approved.Approval == nil {
		t.Fatalf("independent reviewer approves exact content: %+v err=%v", approved, err)
	}
	if approved.Approval.Reviewer != "synthetic-reviewer" || approved.Approval.Revision != inspected.Revision.Number || approved.Approval.Digest != inspected.Revision.Digest {
		t.Fatalf("approval lost actor or exact revision binding: %+v", approved.Approval)
	}

	editedContent := domain.Content(jsonObject(t, approved.Revision.Content))
	editedContent["intent"] = map[string]any{"request": "Revised synthetic design requires a new review"}
	revised, err := author.Revise(ctx, created.ID, approved.Revision.Number, editedContent)
	if err != nil || revised.Revision.Number != 2 || revised.Approved || revised.Approval != nil || revised.Revision.SubmittedAt != nil || revised.Revision.Digest == inspected.Revision.Digest {
		t.Fatalf("edit did not invalidate previous approval: %+v err=%v", revised, err)
	}
	if !reflect.DeepEqual(revised.Revision.Content, editedContent) || !reflect.DeepEqual(revised.Revision.Content["repositoryContext"], content["repositoryContext"]) {
		t.Fatal("edit lost unknown package/context/artifact extension fields")
	}
	_, err = reviewer.Approve(ctx, inspected.ID, inspected.Revision.Number, inspected.Revision.Digest)
	assertAPIError(t, err, http.StatusConflict, "revision_conflict")
	_, err = author.Revise(ctx, created.ID, 1, domain.Content{"intent": "Stale edit must never persist"})
	assertAPIError(t, err, http.StatusConflict, "revision_conflict")

	latestHistory, err := reviewer.History(ctx, created.ID, 0, 1)
	if err != nil || len(latestHistory.Revisions) != 1 || latestHistory.Revisions[0].Number != 2 || latestHistory.Revisions[0].ApprovalCount != 0 || latestHistory.NextBeforeRevision != 2 {
		t.Fatalf("latest history page: %+v err=%v", latestHistory, err)
	}
	olderHistory, err := reviewer.History(ctx, created.ID, latestHistory.NextBeforeRevision, 1)
	if err != nil || len(olderHistory.Revisions) != 1 || olderHistory.Revisions[0].Number != 1 || olderHistory.Revisions[0].ApprovalCount != 1 || olderHistory.NextBeforeRevision != 0 {
		t.Fatalf("historical approval was not retained: %+v err=%v", olderHistory, err)
	}
	historical, err := reviewer.Revision(ctx, created.ID, 1)
	if err != nil || !reflect.DeepEqual(historical.Revision, inspected.Revision) || len(historical.Approvals) != 1 || historical.ApprovalsTruncated {
		t.Fatalf("historical inspected content and approval are not recoverable: %+v err=%v", historical, err)
	}
	if historical.Approvals[0].Reviewer != "synthetic-reviewer" || historical.Approvals[0].Digest != inspected.Revision.Digest || historical.Approvals[0].Revision != 1 {
		t.Fatalf("historical approval changed: %+v", historical.Approvals[0])
	}
	var events []domain.AuditEvent
	var cursor int64
	for pageNumber := 0; ; pageNumber++ {
		if pageNumber == 10 {
			t.Fatal("audit pagination did not terminate")
		}
		page, err := reviewer.Events(ctx, created.ID, cursor, 2)
		if err != nil {
			t.Fatalf("read durable audit events: %v", err)
		}
		events = append(events, page.Events...)
		if page.NextAfterSequence == 0 {
			break
		}
		if page.NextAfterSequence <= cursor {
			t.Fatalf("audit pagination did not advance: %+v", page)
		}
		cursor = page.NextAfterSequence
	}
	wantKinds := []string{"package.created", "review.requested", "review.approved", "package.revised"}
	wantActors := []string{"synthetic-author", "synthetic-author", "synthetic-reviewer", "synthetic-author"}
	if len(events) != len(wantKinds) {
		t.Fatalf("failed commands changed audit history: %+v", events)
	}
	for i, event := range events {
		if event.ChangeID != created.ID || event.EventType != wantKinds[i] || event.Actor != wantActors[i] || (i > 0 && event.Sequence <= events[i-1].Sequence) {
			t.Fatalf("unexpected audit event %d: %+v", i, event)
		}
		if event.EventType == "review.approved" && (event.Revision != 1 || event.Data["digest"] != inspected.Revision.Digest) {
			t.Fatalf("audit approval lost exact inspected revision: %+v", event)
		}
	}
	listed, err := reviewer.ListChanges(ctx, repository, "", 20)
	if err != nil || len(listed.Changes) != 1 || listed.Changes[0].ID != created.ID || listed.Changes[0].Revision != 2 || listed.Changes[0].Digest != revised.Revision.Digest || listed.Changes[0].Approved || listed.Changes[0].Repository != repository {
		t.Fatalf("repository discovery did not show latest unapproved revision: %+v err=%v", listed, err)
	}
	otherListed, err := reviewer.ListChanges(ctx, "synthetic/other-repository", "", 20)
	if err != nil || len(otherListed.Changes) != 1 || otherListed.Changes[0].ID != other.ID {
		t.Fatalf("repository filter leaked unrelated packages: %+v err=%v", otherListed, err)
	}
	current, err := reviewer.Get(ctx, created.ID)
	if err != nil || !reflect.DeepEqual(current, revised) {
		t.Fatalf("failed commands or historical reads changed current state: %+v err=%v", current, err)
	}
}

func reviewServer(t *testing.T) (context.Context, string) {
	t.Helper()
	databaseURL := os.Getenv("CONDUCTOR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set CONDUCTOR_TEST_DATABASE_URL to run the shared PostgreSQL acceptance workflow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "conductor_acceptance_" + hex.EncodeToString(random[:])
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create isolated acceptance schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("drop acceptance schema: %v", err)
		}
	})
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	migrations, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(migrations) == 0 {
		t.Fatalf("find ordered migrations: files=%v err=%v", migrations, err)
	}
	for _, path := range migrations {
		sql, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", path, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	isolatedURL, err := url.Parse(databaseURL)
	if err != nil || (isolatedURL.Scheme != "postgres" && isolatedURL.Scheme != "postgresql") {
		t.Fatalf("CONDUCTOR_TEST_DATABASE_URL must be a PostgreSQL URL: %v", err)
	}
	query := isolatedURL.Query()
	query.Set("search_path", schema)
	isolatedURL.RawQuery = query.Encode()
	database, err := store.Open(ctx, isolatedURL.String())
	if err != nil {
		t.Fatalf("open isolated store: %v", err)
	}
	t.Cleanup(database.Close)
	server := httptest.NewServer(api.New(service.New(database)))
	t.Cleanup(server.Close)
	return ctx, server.URL
}

func reviewClient(t *testing.T, baseURL, actor string) *client.Client {
	t.Helper()
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	c := client.New(baseURL, actor)
	c.HTTP = &http.Client{Transport: transport, Timeout: 10 * time.Second}
	return c
}

func committedContext(t *testing.T, ctx context.Context, repository string) domain.RepositoryContext {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("Git is required for the configured acceptance workflow")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("prepare synthetic Git fixture: %v: %s", err, output)
		}
	}
	run("init", "--initial-branch=main")
	run("config", "user.name", "Synthetic Author")
	run("config", "user.email", "synthetic@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "design.md"), []byte("# Existing synthetic design\nPreserve the current contract.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", "design.md")
	run("commit", "-m", "Commit synthetic baseline")
	snapshot, err := repositorycontext.Collect(ctx, repo, repository, "main", []string{"design.md", "missing-evidence.md"})
	if err != nil {
		t.Fatalf("collect pinned repository context: %v", err)
	}
	return snapshot
}

func jsonObject(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func assertAPIError(t *testing.T, err error, status int, code string) {
	t.Helper()
	var apiError *client.APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != status || apiError.Code != code || apiError.CorrelationID == "" {
		t.Fatalf("expected typed HTTP %d %s with correlation ID, got %v", status, code, err)
	}
}
