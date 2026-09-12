package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

func sharedRepositoryContent(repository, intent string) domain.Content {
	return domain.Content{
		"intent": intent,
		"repositoryContext": map[string]any{
			"schemaVersion": 1, "repository": repository,
			"commit": strings.Repeat("a", 40), "requestedRef": "main",
			"collectedAt": "2026-09-12T12:00:00Z", "collector": "conductor-git/v1",
			"artifacts": []any{map[string]any{"path": "README.md", "state": "missing", "message": "Synthetic fixture omits this file"}},
		},
	}
}

func TestPostgresSharedChangeDiscovery(t *testing.T) {
	t.Parallel()
	ctx, firstStore, open := integrationStore(t)
	// Separate pools and service instances model independent developers connected
	// to the same central store, with no shared in-memory package cache.
	first, second := service.New(firstStore), service.New(open())
	content := sharedRepositoryContent("example/shared", "Shared review")
	created, err := first.Create(ctx, "developer-one", content)
	if err != nil {
		t.Fatal(err)
	}
	page, err := second.List(ctx, "example/shared", "", 20)
	if err != nil || len(page.Changes) != 1 || page.Changes[0].ID != created.ID || page.Changes[0].Revision != 1 || page.Changes[0].Approved || page.Changes[0].Repository != "example/shared" {
		t.Fatalf("second developer cannot discover package: %+v err=%v", page, err)
	}
	inspected, err := second.Revision(ctx, created.ID, page.Changes[0].Revision)
	if err != nil || !reflect.DeepEqual(inspected.Revision.Content, created.Revision.Content) {
		t.Fatalf("second developer cannot inspect shared context: %+v err=%v", inspected, err)
	}
	if _, err := first.Submit(ctx, created.ID, "developer-one", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Approve(ctx, created.ID, "developer-two", 1, inspected.Revision.Digest); err != nil {
		t.Fatal(err)
	}
	approved, err := first.List(ctx, "example/shared", "", 20)
	if err != nil || len(approved.Changes) != 1 || !approved.Changes[0].Approved || approved.Changes[0].Digest != inspected.Revision.Digest {
		t.Fatalf("first developer cannot see independent approval: %+v err=%v", approved, err)
	}
	revised, err := first.Revise(ctx, created.ID, "developer-one", 1, sharedRepositoryContent("example/moved", "Updated shared context"))
	if err != nil {
		t.Fatal(err)
	}
	oldRepository, err := second.List(ctx, "example/shared", "", 20)
	if err != nil || len(oldRepository.Changes) != 0 || oldRepository.Changes == nil {
		t.Fatalf("filter used historical repository metadata: %+v err=%v", oldRepository, err)
	}
	updated, err := second.List(ctx, "example/moved", "", 20)
	if err != nil || len(updated.Changes) != 1 || updated.Changes[0].Revision != 2 || updated.Changes[0].Digest != revised.Revision.Digest || updated.Changes[0].Approved || !updated.Changes[0].CreatedAt.Equal(created.Revision.CreatedAt) {
		t.Fatalf("second developer saw stale revision/approval: %+v err=%v", updated, err)
	}
	history, err := second.Revision(ctx, created.ID, 1)
	if err != nil || len(history.Approvals) != 1 || history.Approvals[0].Reviewer != "developer-two" {
		t.Fatalf("shared discovery lost historical approval: %+v err=%v", history, err)
	}
}

func TestPostgresChangeListKeysetAndRepositoryTypes(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	empty, err := p.List(ctx, "", "", 20)
	if err != nil || empty.Changes == nil || len(empty.Changes) != 0 || empty.NextBefore != "" {
		t.Fatalf("empty shared store: %+v err=%v", empty, err)
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	for _, entry := range []struct {
		id         string
		repository any
	}{
		{"CHG-a", "example/repo"}, {"CHG-b", "example/repo"},
		{"CHG-c", map[string]any{"not": "a repository string"}}, {"CHG-d", "example/other"},
	} {
		content := domain.Content{"intent": entry.id, "repositoryContext": map[string]any{"repository": entry.repository}}
		digest, err := domain.Digest(content)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Create(ctx, entry.id, "author", content, digest, now); err != nil {
			t.Fatal(err)
		}
	}
	first, err := p.List(ctx, "", "", 2)
	if err != nil || len(first.Changes) != 2 || first.Changes[0].ID != "CHG-d" || first.Changes[1].ID != "CHG-c" || first.Changes[1].Repository != "" || first.NextBefore == "" {
		t.Fatalf("tied timestamp first page/malformed metadata: %+v err=%v", first, err)
	}
	newContent := domain.Content{"intent": "Newly inserted shared package"}
	newDigest, err := domain.Digest(newContent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Create(ctx, "CHG-new", "author", newContent, newDigest, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	last, err := p.List(ctx, "", first.NextBefore, 2)
	if err != nil || len(last.Changes) != 2 || last.Changes[0].ID != "CHG-b" || last.Changes[1].ID != "CHG-a" || last.NextBefore != "" {
		t.Fatalf("new insertion shifted next page: %+v err=%v", last, err)
	}
	filtered, err := p.List(ctx, "example/repo", "", 1)
	if err != nil || len(filtered.Changes) != 1 || filtered.Changes[0].ID != "CHG-b" || filtered.NextBefore == "" {
		t.Fatalf("filtered first page: %+v err=%v", filtered, err)
	}
	filteredLast, err := p.List(ctx, "example/repo", filtered.NextBefore, 1)
	if err != nil || len(filteredLast.Changes) != 1 || filteredLast.Changes[0].ID != "CHG-a" || filteredLast.NextBefore != "" {
		t.Fatalf("filtered last page: %+v err=%v", filteredLast, err)
	}
	for _, repository := range []string{"example", "EXAMPLE/repo", `{"not": "a repository string"}`} {
		page, err := p.List(ctx, repository, "", 20)
		if err != nil || len(page.Changes) != 0 {
			t.Fatalf("repository filter was not exact string match: %q %+v err=%v", repository, page, err)
		}
	}
	for _, tc := range []struct {
		repository, cursor string
		limit              int
	}{{"", "invalid cursor", 20}, {"", strings.Repeat("a", 1025), 20}, {strings.Repeat("r", 2049), "", 20}, {"", "", 0}, {"", "", 101}} {
		if _, err := p.List(ctx, tc.repository, tc.cursor, tc.limit); !errors.Is(err, domain.ErrInvalidInput) {
			t.Errorf("invalid list query %+v: got %v", tc, err)
		}
	}
}
