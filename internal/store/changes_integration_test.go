package store

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	content["title"] = "Review a shared change"
	created, err := first.Create(ctx, "developer-one", content)
	if err != nil {
		t.Fatal(err)
	}
	page, err := second.List(ctx, "example/shared", "", 20)
	if err != nil || len(page.Changes) != 1 || page.Changes[0].ID != created.ID || page.Changes[0].Revision != 1 || page.Changes[0].Approved || page.Changes[0].Repository != "example/shared" || page.Changes[0].Title != "Review a shared change" || page.Changes[0].Intent != "Shared review" {
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
	if err != nil || len(updated.Changes) != 1 || updated.Changes[0].Revision != 2 || updated.Changes[0].Digest != revised.Revision.Digest || updated.Changes[0].Approved || !updated.Changes[0].CreatedAt.Equal(created.Revision.CreatedAt) || updated.Changes[0].Title != "" || updated.Changes[0].Intent != "Updated shared context" {
		t.Fatalf("second developer saw stale revision/approval: %+v err=%v", updated, err)
	}
	history, err := second.Revision(ctx, created.ID, 1)
	if err != nil || len(history.Approvals) != 1 || history.Approvals[0].Reviewer != "developer-two" || history.Revision.Digest != created.Revision.Digest || !reflect.DeepEqual(history.Revision.Content, created.Revision.Content) {
		t.Fatalf("shared discovery lost historical approval: %+v err=%v", history, err)
	}
}

func TestPostgresChangeSummaryText(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	s := service.New(p)
	titleBoundary := strings.Repeat("界", 199) + "🧭"
	intentBoundary := strings.Repeat("é", 399) + "🧪"
	for _, tc := range []struct {
		name                            string
		content                         domain.Content
		title, intent                   string
		titleTruncated, intentTruncated bool
	}{
		{name: "absent", content: domain.Content{"future": true}},
		{name: "empty", content: domain.Content{"title": "", "intent": ""}},
		{name: "objects", content: domain.Content{"title": map[string]any{"text": "Keep structured title"}, "intent": []any{"Keep structured intent"}}},
		{name: "scalars", content: domain.Content{"title": false, "intent": 42}},
		{name: "null", content: domain.Content{"title": nil, "intent": nil}},
		{name: "case aliases", content: domain.Content{"Title": "Keep extension", "Intent": "Keep extension"}},
		{name: "exact text", content: domain.Content{"title": "  Synthetic <title>\n", "intent": "\tExplain\nthis change  "}, title: "  Synthetic <title>\n", intent: "\tExplain\nthis change  "},
		{name: "unicode boundary", content: domain.Content{"title": titleBoundary, "intent": intentBoundary}, title: titleBoundary, intent: intentBoundary},
		{name: "unicode truncated", content: domain.Content{"title": titleBoundary + "尾", "intent": intentBoundary + "end"}, title: titleBoundary, intent: intentBoundary, titleTruncated: true, intentTruncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created, err := s.Create(ctx, "author", tc.content)
			if err != nil {
				t.Fatal(err)
			}
			counts := integrationCounts(t, ctx, p)
			page, err := s.List(ctx, "", "", 20)
			if err != nil {
				t.Fatal(err)
			}
			var summary *domain.ChangeSummary
			for i := range page.Changes {
				if page.Changes[i].ID == created.ID {
					summary = &page.Changes[i]
					break
				}
			}
			if summary == nil || summary.Title != tc.title || summary.Intent != tc.intent || summary.TitleTruncated != tc.titleTruncated || summary.IntentTruncated != tc.intentTruncated {
				t.Fatalf("incorrect text projection: %+v", summary)
			}
			if !utf8.ValidString(summary.Title) || !utf8.ValidString(summary.Intent) || utf8.RuneCountInString(summary.Title) > domain.MaxChangeSummaryTitleRunes || utf8.RuneCountInString(summary.Intent) > domain.MaxChangeSummaryIntentRunes {
				t.Fatalf("summary text exceeds Unicode bounds: %+v", summary)
			}
			encoded, err := json.Marshal(summary)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err = json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			for field, present := range map[string]bool{"title": tc.title != "", "intent": tc.intent != "", "titleTruncated": tc.titleTruncated, "intentTruncated": tc.intentTruncated} {
				if _, ok := fields[field]; ok != present {
					t.Fatalf("incorrect optional field %s in %s", field, encoded)
				}
			}
			retained, err := s.Get(ctx, created.ID)
			if err != nil || retained.Revision.Digest != created.Revision.Digest || summary.Digest != created.Revision.Digest || !reflect.DeepEqual(retained.Revision.Content, created.Revision.Content) || integrationCounts(t, ctx, p) != counts {
				t.Fatalf("discovery modified stored revision or audit: %+v err=%v", retained, err)
			}
		})
	}
}

func TestPostgresChangeSummaryAccessBeforePagination(t *testing.T) {
	t.Parallel()
	ctx, p, _ := integrationStore(t)
	provisionAccess(t, ctx, p)
	secure := service.NewAuthenticated(p)
	older, err := secure.Create(accessContext(ctx, "author", "workspace-one", "repo-one"), "", domain.Content{"title": "Older visible change", "intent": "Older visible intent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = secure.Create(accessContext(ctx, "reviewer", "workspace-one", "repo-two"), "", domain.Content{"title": "Private change", "intent": "Private intent"}); err != nil {
		t.Fatal(err)
	}
	newer, err := secure.Create(accessContext(ctx, "author", "workspace-one", "repo-one"), "", domain.Content{"title": "Newer visible change", "intent": "Newer visible intent"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = secure.Create(accessContext(ctx, "reviewer", "workspace-two", "repo-other-workspace"), "", domain.Content{"title": "Other workspace", "intent": "Other workspace intent"}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.New(p).Create(ctx, "local-author", domain.Content{"title": "Local change", "intent": "Local intent"}); err != nil {
		t.Fatal(err)
	}
	for _, principal := range []struct{ subject, repository string }{{"author", ""}, {"author", "repo-one"}, {"reviewer", "repo-one"}} {
		reader := accessContext(ctx, principal.subject, "workspace-one", principal.repository)
		first, err := secure.List(reader, "", "", 1)
		if err != nil || len(first.Changes) != 1 || first.Changes[0].ID != newer.ID || first.Changes[0].Title != "Newer visible change" || first.Changes[0].Intent != "Newer visible intent" || first.NextBefore == "" {
			t.Fatalf("newer authorized summary for %+v: %+v err=%v", principal, first, err)
		}
		last, err := secure.List(reader, "", first.NextBefore, 1)
		if err != nil || len(last.Changes) != 1 || last.Changes[0].ID != older.ID || last.Changes[0].Title != "Older visible change" || last.Changes[0].Intent != "Older visible intent" || last.NextBefore != "" {
			t.Fatalf("older authorized summary for %+v: %+v err=%v", principal, last, err)
		}
	}
	if err = p.ApplyAccessConfig(ctx, "synthetic-operator", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-author"}}}); err != nil {
		t.Fatal(err)
	}
	page, err := secure.List(accessContext(ctx, "author", "workspace-one", ""), "", "", 1)
	if err != nil || len(page.Changes) != 0 || page.NextBefore != "" {
		t.Fatalf("revoked summary visible: %+v err=%v", page, err)
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
