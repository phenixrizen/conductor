package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

func collectionModelFixture(t *testing.T, execute executor, receipt bool) model {
	t.Helper()
	m := authenticatedModel(t, execute)
	m.collectionMode = true
	c := domain.Collection{ID: strings.Repeat("a", 32), WorkspaceID: "team", RepositoryID: "application", RequesterID: "person-reviewer",
		Input: domain.CollectionInput{Commit: strings.Repeat("a", 40), Paths: []string{"README.md", "missing.md"}}, CreatedAt: time.Now().UTC(),
		Source: domain.ContextSource{WorkspaceID: "team", RepositoryID: "application", Provider: "github", Host: "github.com", ProviderID: "123", Locator: "synthetic/application", Profile: "github-rest/2026-03-10", IntegrationVersion: 1}}
	c.InputDigest, _ = domain.JSONDigest(c.Input)
	if receipt {
		text := "synthetic \x1b]52;c;CANARY\a text"
		snapshot := domain.RepositoryContext{SchemaVersion: 2, Collector: domain.RemoteContextCollector, Repository: "application", CollectionID: c.ID, Source: &c.Source, Commit: c.Input.Commit, RequestedRef: c.Input.Commit, CollectedAt: time.Now().UTC(), Artifacts: []domain.ContextArtifact{{Path: "README.md", State: "unavailable", Message: text}, {Path: "missing.md", State: "missing", Message: "not present"}}}
		digest, _ := domain.JSONDigest(snapshot)
		c.Receipt = &domain.CollectionReceipt{ID: c.ID, Digest: digest, Snapshot: snapshot, CreatedAt: time.Now().UTC()}
		// Artifact messages cannot contain controls; rendering is also independently
		// exercised below with unexpected server observation text.
		c.Receipt.Snapshot.Artifacts[0].Message = "unavailable synthetic source"
		c.Receipt.Digest, _ = domain.JSONDigest(c.Receipt.Snapshot)
		if err := domain.ValidateCollectionReceipt(*c.Receipt, c); err != nil {
			t.Fatal(err)
		}
	}
	m.collections.record = &c
	m.collections.inspectID = c.ID
	m.rebuild()
	return m
}

func TestCollectionAttachmentCapturesExactTupleWithoutReadAndBlocksConflicts(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "stale"}[conflict], func(t *testing.T) {
			m := collectionModelFixture(t, nil, true)
			original := *m.pack
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				want := map[string]any{"expectedRevision": float64(1), "collectionId": m.collections.record.ID, "digest": m.collections.record.Receipt.Digest}
				if !reflect.DeepEqual(body, want) {
					t.Errorf("attachment did not send inspected tuple: %#v", body)
				}
				if conflict {
					w.WriteHeader(409)
					_, _ = w.Write([]byte(`{"error":{"code":"revision_conflict","message":"inspect latest"}}`))
					return
				}
				p := original
				p.Revision.Number++
				p.Revision.Content = make(domain.Content, len(original.Revision.Content)+1)
				for key, value := range original.Revision.Content {
					p.Revision.Content[key] = value
				}
				p.Revision.Content["repositoryContext"] = m.collections.record.Receipt.Snapshot
				p.Revision.Digest, _ = domain.Digest(p.Revision.Content)
				p.Revision.SubmittedAt = nil
				p.Revision.Author = "person-reviewer"
				p.Approved = false
				_ = json.NewEncoder(w).Encode(p)
			}))
			defer server.Close()
			c, _ := client.NewAuthenticated(server.URL, "synthetic.token", "team", "application")
			m.execute = runner(context.Background(), c)
			m, cmd := key(m, "t")
			if cmd != nil || m.prompt != "attach" || !strings.Contains(m.View(), original.Revision.Digest) || !strings.Contains(m.View(), m.collections.record.Receipt.Digest) {
				t.Fatal("attachment hid its inspected identities")
			}
			m, _ = key(m, "attach")
			m, cmd = key(m, "enter")
			m = runCommand(t, m, cmd)
			if len(requests) != 1 || requests[0] != "POST /api/v1/changes/"+original.ID+"/context-attachments" {
				t.Fatalf("attachment refreshed: %v", requests)
			}
			if conflict {
				if !m.blocked || m.pack.Revision.Number != 1 {
					t.Fatal("stale attachment retained write eligibility")
				}
				m, cmd = key(m, "t")
				if cmd != nil || m.prompt != "" {
					t.Fatal("stale attachment allowed another command")
				}
			} else if m.collectionMode || m.pack.Revision.Number != 2 || m.pack.Approved || m.pack.Revision.Content["future"] == nil {
				t.Fatal("attachment did not show new unapproved package with extensions")
			}
		})
	}
}

func TestCollectionUnknownCreateRetainsImmutableKeyAndNeverRetriesAutomatically(t *testing.T) {
	var sent []request
	m := collectionModelFixture(t, func(req request) (tea.Cmd, context.CancelFunc) {
		sent = append(sent, req)
		return func() tea.Msg { return result{request: req, err: errors.New("lost response")} }, func() {}
	}, false)
	d := collectionDraft{Commit: strings.Repeat("b", 40), Paths: []string{"README.md"}, IdempotencyKey: "synthetic-stable-key"}
	m.collections.draft = &d
	m.collections.record = nil
	m.rebuild()
	m, cmd := confirmAction(t, m, "collect", "s")
	m = runCommand(t, m, cmd)
	if len(sent) != 1 || m.collections.draft == nil || !m.collections.uncertainCreate || !strings.Contains(m.status, "Same request and key retained") {
		t.Fatal("uncertain creation lost request or retried")
	}
	m, cmd = key(m, "c")
	if cmd != nil || m.prompt != "" {
		t.Fatal("uncertainty allowed replacing request without explicit discard")
	}
	m, cmd = confirmAction(t, m, "collect", "s")
	if len(sent) != 2 || !reflect.DeepEqual(sent[0].collectionDraft, sent[1].collectionDraft) {
		t.Fatal("explicit retry changed identity or input")
	}
	// Esc cancels the in-flight command and fences its eventual late result.
	serial := m.serial
	m, _ = key(m, "esc")
	next, follow := m.Update(result{request: request{serial: serial, op: "collect"}, collection: *collectionModelFixture(t, nil, false).collections.record})
	m = next.(model)
	if follow != nil || m.collections.draft == nil || !m.collections.uncertainCreate || m.collections.record != nil {
		t.Fatal("late creation response replaced cancelled state")
	}
}

func TestCollectionCapabilitiesAndModeIsolation(t *testing.T) {
	for _, kind := range []string{"human", "agent"} {
		m := collectionModelFixture(t, nil, true)
		m.access.principal.Kind = kind
		for _, k := range []string{"a", "s", "e"} {
			copy, cmd := key(m, k)
			if cmd != nil || copy.prompt != "" {
				t.Fatalf("collection key %s fell through to package command", k)
			}
		}
		m.collections.record.RequesterID = "someone-else"
		if m.collectionActionAllowed("attach") != nil {
			t.Fatal("author cannot attach another requester's shared receipt")
		}
		if m.collectionActionAllowed("cancel-collection") == nil {
			t.Fatal("another requester can cancel collection")
		}
		m.access.repository.CanAuthor = false
		for _, k := range []string{"c", "s", "t", "x"} {
			copy, cmd := key(m, k)
			if cmd != nil || copy.prompt != "" {
				t.Fatalf("reader received %s write control", k)
			}
		}
	}
	m := newModel(Options{Actor: "author"}, nil)
	m, cmd := key(m, "g")
	if cmd != nil || m.collectionMode || !strings.Contains(m.status, "requires authenticated") {
		t.Fatal("local mode entered collection workflow")
	}
}

func TestCollectionDenialClearsAllStateAndRecoveryUsesCollectionIdentity(t *testing.T) {
	for _, op := range []string{"collection", "collections", "collect", "cancel-collection", "attach"} {
		t.Run(op, func(t *testing.T) {
			var sent []request
			m := collectionModelFixture(t, func(req request) (tea.Cmd, context.CancelFunc) {
				sent = append(sent, req)
				return func() tea.Msg { return nil }, func() {}
			}, true)
			id := m.collections.record.ID
			m.collections.draft = &collectionDraft{IdempotencyKey: "private-key"}
			m.collections.uncertainCreate = true
			m.prompt = "attach"
			m.pending = request{op: "attach"}
			m.serial = 3
			next, cmd := m.Update(result{request: request{serial: 3, op: op, id: id}, err: &client.APIError{StatusCode: 403, Code: "forbidden"}})
			m = next.(model)
			if cmd != nil || m.access.ready || m.pack != nil || m.collections.draft != nil || m.collections.record != nil || m.collections.uncertainCreate || m.prompt != "" || len(m.lines) != 0 {
				t.Fatal("denial retained collection or authority")
			}
			m, cmd = key(m, "r")
			if cmd == nil || len(sent) != 1 || sent[0].op != "access" || !sent[0].collectionView || sent[0].id != id {
				t.Fatal("collection recovery lost selection type")
			}
			session, repositories, _ := accessFixture(t)
			next, cmd = m.Update(result{request: sent[0], session: session, repositories: repositories})
			if cmd == nil || len(sent) != 2 || sent[1].op != "collection" || sent[1].id != id {
				t.Fatal("recovery read a package or skipped discovery")
			}
		})
	}
}

func TestCollectionResponseScopeAndReceiptValidationFailClosed(t *testing.T) {
	for _, mutate := range []func(*domain.Collection){
		func(c *domain.Collection) { c.WorkspaceID = "other" },
		func(c *domain.Collection) { c.Source.ProviderID = "456" },
		func(c *domain.Collection) { c.ID = strings.Repeat("b", 32) },
		func(c *domain.Collection) { c.Receipt.Digest = strings.Repeat("f", 64) },
		func(c *domain.Collection) { c.InputDigest = strings.Repeat("f", 64) },
	} {
		m := collectionModelFixture(t, nil, true)
		id := m.collections.record.ID
		c := *m.collections.record
		mutate(&c)
		m.serial = 1
		next, cmd := m.Update(result{request: request{serial: 1, op: "collection", id: id}, collection: c})
		m = next.(model)
		if cmd != nil || m.access.ready || m.collections.record != nil {
			t.Fatal("unscoped or forged receipt became inspected")
		}
	}
}

func TestCollectionPagingCancellationAndStaleEvidenceRendering(t *testing.T) {
	var sent request
	m := collectionModelFixture(t, func(req request) (tea.Cmd, context.CancelFunc) {
		sent = req
		return func() tea.Msg { return nil }, func() {}
	}, false)
	m.collections.record = nil
	m.collections.page = domain.CollectionPage{Collections: []domain.CollectionSummary{{ID: strings.Repeat("a", 32)}, {ID: strings.Repeat("b", 32)}}, NextBefore: "opaque.cursor"}
	m, _ = key(m, "down")
	if m.collections.selected != 1 || m.offset != 0 {
		t.Fatal("retained package captured collection navigation")
	}
	m, cmd := key(m, "n")
	if cmd == nil || sent.cursor != "opaque.cursor" || sent.op != "collections" || len(m.collections.page.Collections) != 0 {
		t.Fatal("collection page cursor not preserved independently")
	}
	next, _ := m.Update(result{request: sent, err: errors.New("unavailable")})
	m = next.(model)
	if len(m.collections.page.Collections) != 0 {
		t.Fatal("failed page retained old navigation")
	}
	if !strings.Contains(m.View(), "Collection data unavailable") {
		t.Fatal("failed page looked like confirmed empty discovery")
	}
	m = collectionModelFixture(t, nil, true)
	m.collections.record.Execution = &domain.CollectionExecution{State: "running", ObservedAt: time.Now().Add(-time.Minute), Current: true}
	now := time.Now()
	m.collections.record.CancelRequestedAt = &now
	m.rebuild()
	view := m.View()
	if !strings.Contains(view, "stale or unavailable") || !strings.Contains(view, "cancellation requested") || !strings.Contains(view, "receipt available") {
		t.Fatal("execution, receipt and cancellation facts were conflated")
	}
	for _, r := range view {
		if r != '\n' && (r < 32 || r > 126) {
			t.Fatalf("unsafe terminal rune %U", r)
		}
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "Coverage: missing.md | missing") {
		t.Fatal("receipt hid missing evidence")
	}
}

func TestCollectionRequestFileStrictBoundedAndCanonical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "request.json")
	valid := `{"commit":"` + strings.Repeat("a", 40) + `","paths":["z.md","README.md"],"idempotencyKey":"stable-key"}`
	for _, bad := range []string{
		`null`, valid + ` {}`, strings.Replace(valid, `"commit"`, `"Commit"`, 1), strings.Replace(valid, `"commit"`, `"unknown"`, 1),
		strings.Replace(valid, `"paths":["z.md","README.md"]`, `"paths":null`, 1), strings.Replace(valid, `"z.md"`, `"../secret"`, 1),
		strings.Replace(valid, `"z.md"`, `"README.md"`, 1), strings.Replace(valid, `"idempotencyKey":"stable-key"`, `"idempotencyKey":"stable-key","idempotencyKey":"stable-key"`, 1),
		strings.Replace(valid, `"idempotencyKey":"stable-key"`, `"idempotencyKey":"stable-key","\u0069dempotencyKey":"stable-key"`, 1),
		strings.Repeat(" ", 64<<10) + valid, string([]byte{0xff}),
	} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readCollectionDraft(context.Background(), path); err == nil {
			t.Fatalf("invalid request accepted: %.100s", bad)
		}
	}
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readCollectionDraft(context.Background(), path)
	if err != nil || !reflect.DeepEqual(got.Paths, []string{"README.md", "z.md"}) || got.IdempotencyKey != "stable-key" {
		t.Fatalf("canonical request: %+v %v", got, err)
	}
	full := strings.TrimSuffix(valid, "}") + `,"fullSource":true}`
	if err = os.WriteFile(path, []byte(full), 0600); err != nil {
		t.Fatal(err)
	}
	complete, err := readCollectionDraft(context.Background(), path)
	if err != nil || !complete.input().FullSource {
		t.Fatal("whole-source request omitted from preview/input", err)
	}
	for _, invalid := range []string{"-", dir, ""} {
		if _, err := readCollectionDraft(context.Background(), invalid); err == nil {
			t.Fatal("non-file input accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readCollectionDraft(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatal("file read ignored cancellation")
	}
}

func TestCollectionSuccessfulCreateValidatesCapturedInputAndRequester(t *testing.T) {
	m := collectionModelFixture(t, nil, false)
	c := *m.collections.record
	m.collections.record = nil
	draft := collectionDraft{Commit: c.Input.Commit, Paths: c.Input.Paths, IdempotencyKey: "explicit-key"}
	m.collections.draft = &draft
	m.serial = 1
	next, cmd := m.Update(result{request: request{serial: 1, op: "collect", collectionDraft: draft}, collection: c})
	m = next.(model)
	if cmd != nil || !m.access.ready || m.collections.draft != nil || m.collections.record == nil || m.collections.record.ID != c.ID || !strings.Contains(m.status, "request accepted") {
		t.Fatal("valid collection acknowledgment failed captured-input validation")
	}
}

func TestAttachmentResponseMustDescribeNewUnapprovedDraft(t *testing.T) {
	for _, mutate := range []func(*domain.Package){
		func(p *domain.Package) { p.Approved = true },
		func(p *domain.Package) { p.Approval = &domain.Approval{} },
		func(p *domain.Package) { now := time.Now(); p.Revision.SubmittedAt = &now },
		func(p *domain.Package) { p.Revision.Author = "unexpected-author" },
	} {
		m := collectionModelFixture(t, nil, true)
		next, _ := m.confirmCollection("attach")
		m = next.(model)
		req := m.pending
		req.serial = 1
		m.serial = 1
		m.prompt = ""
		p := *m.pack
		p.Revision.Number++
		p.Revision.Content = req.content
		p.Revision.Digest, _ = domain.Digest(req.content)
		p.Revision.Author = m.actor()
		p.Revision.SubmittedAt = nil
		mutate(&p)
		next, cmd := m.Update(result{request: req, pack: p})
		m = next.(model)
		if cmd != nil || !m.blocked || m.pack.Revision.Number != 1 || !strings.Contains(m.status, "Write not confirmed") {
			t.Fatal("attachment response invented approval or lost authenticated authorship")
		}
	}
}
