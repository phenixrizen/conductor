package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/internal/service"
)

func assistanceFixture(t *testing.T) (context.Context, *Postgres, *service.AuthenticatedService, domain.Package, func() *Postgres) {
	t.Helper()
	ctx, p, reopen := integrationStore(t)
	provisionAccess(t, ctx, p)
	if err := p.ApplyAccessConfig(ctx, "fixture", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-author", CanRead: true, CanAuthor: true, CanApprove: true}}}); err != nil {
		t.Fatal(err)
	}
	s := service.NewAuthenticated(p)
	pkg, err := s.Create(accessContext(ctx, "author", "workspace-one", "repo-one"), "ignored", domain.Content{"title": "Original", "intent": "Synthetic intent", "design": "Original design", "extension": map[string]any{"keep": true}, "tasks": []any{"legacy structured task"}, "verification": nil})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, p, s, pkg, reopen
}
func assistanceInput(pkg domain.Package) domain.AssistanceInput {
	return domain.AssistanceInput{ChangeID: pkg.ID, ExpectedRevision: pkg.Revision.Number, ExpectedDigest: pkg.Revision.Digest, Instruction: "Improve the design and title", Sections: []string{"design", "title", "scope"}}
}
func applicationInput(value domain.DesignAssistance, sections ...string) domain.ApplySuggestionInput {
	return domain.ApplySuggestionInput{RequestDigest: value.Digest, SuggestionDigest: value.Suggestion.Digest, ExpectedRevision: value.Base.Number, ExpectedDigest: value.Base.Digest, Sections: sections}
}
func requestAndSuggest(t *testing.T, ctx context.Context, s *service.AuthenticatedService, pkg domain.Package, key string, sections map[string]string) domain.DesignAssistance {
	t.Helper()
	record, err := s.RequestDesignAssistance(accessContext(ctx, "author", "workspace-one", "repo-one"), key, assistanceInput(pkg))
	if err != nil {
		t.Fatal(err)
	}
	record, err = s.ProposeDesignSections(accessContext(ctx, "worker", "workspace-one", "repo-one"), record.ID, key, domain.SuggestionInput{RequestDigest: record.Digest, Sections: sections, Note: "Synthetic agent proposal; not verification"})
	if err != nil {
		t.Fatal(err)
	}
	return record
}
func TestDesignAssistanceSharedWorkflowAndReopen(t *testing.T) {
	t.Parallel()
	ctx, p, s, pkg, reopen := assistanceFixture(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	reviewer := accessContext(ctx, "reviewer", "workspace-one", "repo-one")
	agent := accessContext(ctx, "worker", "workspace-one", "repo-one")
	if _, err := s.Submit(author, pkg.ID, "ignored", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Approve(reviewer, pkg.ID, "ignored", 1, pkg.Revision.Digest); err != nil {
		t.Fatal(err)
	}
	in := assistanceInput(pkg)
	record, err := s.RequestDesignAssistance(author, "request-one", in)
	if err != nil {
		t.Fatal(err)
	}
	if record.RequesterID != "human-author" || record.Base.SubmittedAt == nil || record.Suggestion != nil || record.Application != nil {
		t.Fatalf("bad request fact: %+v", record)
	}
	if !reflect.DeepEqual(record.Input.Sections, in.Sections) {
		t.Fatal("section order changed")
	}
	proposal := domain.SuggestionInput{RequestDigest: record.Digest, Sections: map[string]string{"title": "Proposed title", "design": "Proposed design", "scope": ""}, Note: "Synthetic suggestion"}
	if _, err = s.RequestDesignAssistance(agent, "agent-request", in); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("agent request: %v", err)
	}
	if _, err = s.ProposeDesignSections(author, record.ID, "human-proposal", proposal); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("human proposal: %v", err)
	}
	record, err = s.ProposeDesignSections(agent, record.ID, "proposal-one", proposal)
	if err != nil {
		t.Fatal(err)
	}
	if record.Suggestion.AgentID != "agent-worker" {
		t.Fatal("agent attribution lost")
	}
	apply := applicationInput(record, "design", "scope")
	for _, other := range []context.Context{agent, reviewer} {
		if _, err = s.ApplyDesignSuggestion(other, record.ID, "not-requester", apply); !errors.Is(err, domain.ErrForbidden) {
			t.Fatalf("nonrequester applied: %v", err)
		}
	}
	applied, err := s.ApplyDesignSuggestion(author, record.ID, "apply-one", apply)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Application.AppliedBy != "human-author" || applied.Application.Revision != 2 {
		t.Fatalf("bad application: %+v", applied.Application)
	}
	current, err := s.Get(author, pkg.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Approved || current.Revision.Author != "human-author" || current.Revision.Content["title"] != "Original" || current.Revision.Content["design"] != "Proposed design" || current.Revision.Content["scope"] != "" || !reflect.DeepEqual(current.Revision.Content["extension"], pkg.Revision.Content["extension"]) || !reflect.DeepEqual(current.Revision.Content["tasks"], pkg.Revision.Content["tasks"]) {
		t.Fatalf("selected overlay or approval invalidation lost: %+v", current)
	}
	if value, present := current.Revision.Content["verification"]; !present || value != nil {
		t.Fatal("legacy null field lost")
	}
	if _, err = s.Submit(author, pkg.ID, "ignored", 2); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Approve(author, pkg.ID, "ignored", 2, current.Revision.Digest); !errors.Is(err, domain.ErrSelfApproval) {
		t.Fatalf("self approval: %v", err)
	}
	if _, err = s.Approve(reviewer, pkg.ID, "ignored", 2, current.Revision.Digest); err != nil {
		t.Fatal(err)
	}
	// A later ordinary revision cannot turn an uncertain application recovery into
	// a second edit, and the retained fact still names its original produced revision.
	current.Revision.Content["intent"] = "Later human edit"
	if _, err = s.Revise(author, pkg.ID, "ignored", 2, current.Revision.Content); err != nil {
		t.Fatal(err)
	}
	for _, command := range []func() (domain.DesignAssistance, error){func() (domain.DesignAssistance, error) { return s.RequestDesignAssistance(author, "request-one", in) }, func() (domain.DesignAssistance, error) {
		return s.ProposeDesignSections(agent, record.ID, "proposal-one", proposal)
	}, func() (domain.DesignAssistance, error) {
		return s.ApplyDesignSuggestion(author, record.ID, "apply-one", apply)
	}} {
		recovered, e := command()
		if e != nil || !reflect.DeepEqual(recovered, applied) {
			t.Fatalf("retained recovery drifted: %+v %v", recovered, e)
		}
	}
	var count int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE change_id=$1 AND event_type LIKE 'design_assistance.%'`, pkg.ID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("assistance audit duplicated: %d %v", count, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE change_id=$1 AND data::text LIKE '%Synthetic%'`, pkg.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("private text entered audit")
	}
	p.Close()
	opened := service.NewAuthenticated(reopen())
	retained, err := opened.GetDesignAssistance(reviewer, record.ID)
	if err != nil || !reflect.DeepEqual(retained, applied) {
		t.Fatalf("pool reopen lost facts: %+v %v", retained, err)
	}
}
func TestDesignAssistanceStaleNoopAndHistoricalRevert(t *testing.T) {
	t.Parallel()
	ctx, _, s, pkg, _ := assistanceFixture(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	unchanged := requestAndSuggest(t, ctx, s, pkg, "unchanged", map[string]string{"design": "Original design"})
	if _, err := s.ApplyDesignSuggestion(author, unchanged.ID, "unchanged", applicationInput(unchanged, "design")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("unchanged edit: %v", err)
	}
	stale := requestAndSuggest(t, ctx, s, pkg, "stale", map[string]string{"design": "Stale proposal"})
	pkg.Revision.Content["design"] = "Second design"
	second, err := s.Revise(author, pkg.ID, "ignored", 1, pkg.Revision.Content)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyDesignSuggestion(author, stale.ID, "stale", applicationInput(stale, "design")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stale edit: %v", err)
	}
	historical := requestAndSuggest(t, ctx, s, second, "historical", map[string]string{"design": "Original design"})
	if _, err = s.ApplyDesignSuggestion(author, historical.ID, "historical", applicationInput(historical, "design")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("historical content revert: %v", err)
	}
	current, err := s.Get(author, pkg.ID)
	if err != nil || current.Revision.Number != 2 {
		t.Fatalf("rejected edits wrote revision: %+v %v", current, err)
	}
	for _, field := range []string{"tasks", "verification"} {
		in := assistanceInput(second)
		in.Sections = []string{field}
		if _, err = s.RequestDesignAssistance(author, field, in); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("coerced legacy field %s: %v", field, err)
		}
	}
}
func TestDesignAssistanceConcurrentCommands(t *testing.T) {
	t.Parallel()
	ctx, p, s, pkg, _ := assistanceFixture(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	agent := accessContext(ctx, "worker", "workspace-one", "repo-one")
	const workers = 6
	results := make([]domain.DesignAssistance, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			results[i], errs[i] = s.RequestDesignAssistance(author, "shared-request", assistanceInput(pkg))
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil || results[i].ID != results[0].ID {
			t.Fatalf("request concurrency: %+v %v", results, errs)
		}
	}
	record := results[0]
	changed := record.Input
	changed.Instruction = "Different instruction"
	if _, err := s.RequestDesignAssistance(author, "shared-request", changed); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("changed request replay: %v", err)
	}
	for i := range workers {
		wg.Go(func() {
			results[i], errs[i] = s.ProposeDesignSections(agent, record.ID, string(rune('a'+i)), domain.SuggestionInput{RequestDigest: record.Digest, Sections: map[string]string{"design": "Concurrent suggestion"}})
		})
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("unexpected proposal error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent proposals: %d", successes)
	}
	record, err := s.GetDesignAssistance(author, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := applicationInput(record, "design")
	for i := range workers {
		wg.Go(func() { results[i], errs[i] = s.ApplyDesignSuggestion(author, record.ID, "shared-application", in) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil || results[i].Application.Revision != 2 {
			t.Fatalf("application concurrency: %+v %v", results, errs)
		}
	}
	in.ExpectedRevision = 2
	if _, err = s.ApplyDesignSuggestion(author, record.ID, "shared-application", in); !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Fatalf("changed application replay: %v", err)
	}
	var revisions int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM work_package_revisions WHERE change_id=$1`, pkg.ID).Scan(&revisions); err != nil || revisions != 2 {
		t.Fatalf("duplicate revisions: %d %v", revisions, err)
	}
}
func TestDesignAssistanceRevocationBeforeReplayAndRead(t *testing.T) {
	t.Parallel()
	ctx, p, s, pkg, _ := assistanceFixture(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	agent := accessContext(ctx, "worker", "workspace-one", "repo-one")
	record := requestAndSuggest(t, ctx, s, pkg, "request", map[string]string{"design": "Suggested"})
	in := applicationInput(record, "design")
	if _, err := s.ApplyDesignSuggestion(author, record.ID, "applied", in); err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyAccessConfig(ctx, "revoke-author", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "human-author", CanRead: true}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RequestDesignAssistance(author, "request", assistanceInput(pkg)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("request replay bypassed revocation: %v", err)
	}
	if _, err := s.ApplyDesignSuggestion(author, record.ID, "applied", in); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("apply replay bypassed revocation: %v", err)
	}
	if err := p.ApplyAccessConfig(ctx, "revoke-agent", domain.AccessConfig{Grants: []domain.GrantConfig{{RepositoryID: "repo-one", PrincipalID: "agent-worker"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDesignAssistance(agent, record.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("read bypassed revocation: %v", err)
	}
	if _, err := s.ProposeDesignSections(agent, record.ID, "request", domain.SuggestionInput{RequestDigest: record.Digest, Sections: record.Suggestion.Sections, Note: record.Suggestion.Note}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("proposal replay bypassed revocation: %v", err)
	}
	if _, err := s.ListDesignAssistance(agent, domain.AssistanceListOptions{Limit: 20}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("list bypassed revocation: %v", err)
	}
	if _, err := s.GetDesignAssistance(accessContext(ctx, "reviewer", "workspace-one", "repo-two"), record.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("wrong scope exposed request: %v", err)
	}
}
func TestDesignAssistanceAuditFailureRollsBackRevisionAndKey(t *testing.T) {
	t.Parallel()
	ctx, p, s, pkg, _ := assistanceFixture(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	record := requestAndSuggest(t, ctx, s, pkg, "request", map[string]string{"design": "Suggested"})
	in := applicationInput(record, "design")
	_, err := p.pool.Exec(ctx, `CREATE FUNCTION reject_assistance_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_type='design_assistance.applied' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_assistance_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_assistance_audit()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyDesignSuggestion(author, record.ID, "retry-key", in); err == nil {
		t.Fatal("audit failure accepted")
	}
	current, err := s.Get(author, pkg.ID)
	if err != nil || current.Revision.Number != 1 {
		t.Fatalf("revision escaped rollback: %+v %v", current, err)
	}
	unchanged, err := s.GetDesignAssistance(author, record.ID)
	if err != nil || unchanged.Application != nil {
		t.Fatalf("application escaped rollback: %+v %v", unchanged, err)
	}
	var count int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM design_assistance_commands WHERE operation='application'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("key escaped rollback: %d %v", count, err)
	}
	if _, err = p.pool.Exec(ctx, `DROP TRIGGER reject_assistance_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyDesignSuggestion(author, record.ID, "retry-key", in); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}
func TestDesignAssistancePaginationAndHeldAuthorization(t *testing.T) {
	t.Parallel()
	ctx, p, s, pkg, _ := assistanceFixture(t)
	author := accessContext(ctx, "author", "workspace-one", "repo-one")
	for _, key := range []string{"one", "two", "three"} {
		if _, err := s.RequestDesignAssistance(author, key, assistanceInput(pkg)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ListDesignAssistance(author, domain.AssistanceListOptions{ChangeID: pkg.ID, Limit: 2})
	if err != nil || len(first.Requests) != 2 || first.NextBefore == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	second, err := s.ListDesignAssistance(author, domain.AssistanceListOptions{ChangeID: pkg.ID, Before: first.NextBefore, Limit: 2})
	if err != nil || len(second.Requests) != 1 || second.NextBefore != "" || second.Requests[0].ID == first.Requests[1].ID {
		t.Fatalf("second page: %+v %v", second, err)
	}
	access, _ := domain.AccessFromContext(author)
	gate, err := p.BeginAccess(ctx, access)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	scoped := gate.(*Postgres)
	if _, err = scoped.GetDesignAssistance(ctx, first.Requests[0].ID); err != nil {
		t.Fatal(err)
	}
	// A separate transaction cannot revoke a grant while the assistance read owns it.
	revocation, err := p.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer revocation.Rollback(ctx)
	if _, err = revocation.Exec(ctx, `SET LOCAL lock_timeout='100ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = revocation.Exec(ctx, `UPDATE repository_grants SET can_author=false WHERE repository_id='repo-one' AND principal_id='human-author'`)
	if err == nil || !strings.Contains(err.Error(), "lock timeout") {
		t.Fatalf("read did not retain permission lock: %v", err)
	}
}
