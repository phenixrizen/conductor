package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

func accessFixture(t *testing.T) (domain.Session, domain.RepositoryPage, domain.Package) {
	t.Helper()
	session := domain.Session{Principal: domain.Principal{ID: "person-reviewer", Kind: "human"},
		Workspaces: []domain.Workspace{{ID: "team", Name: "Synthetic team"}}}
	repositories := domain.RepositoryPage{Repositories: []domain.ManagedRepository{{ID: "application", WorkspaceID: "team",
		Provider: "github", Host: "github.com", ProviderID: "123", Name: "synthetic/application",
		CanRead: true, CanAuthor: true, CanApprove: true}}}
	p := fixture(t, 1)
	p.WorkspaceID, p.RepositoryID = "team", "application"
	return session, repositories, p
}

func authenticatedModel(t *testing.T, execute executor) model {
	t.Helper()
	session, repositories, p := accessFixture(t)
	m := newModel(Options{}, execute)
	m.access = accessState{authenticated: true, workspaceID: "team", repositoryID: "application"}
	var err error
	m.access, err = m.access.inspect(session, repositories)
	if err != nil {
		t.Fatal(err)
	}
	m.width, m.height = 120, 45
	m.pack, m.inspectID = &p, p.ID
	m.rebuild()
	return m
}

func TestAuthenticatedStartupApprovalAndRefreshUseFixedClient(t *testing.T) {
	session, repositories, p := accessFixture(t)
	var mu sync.Mutex
	var requests []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer synthetic.token" || len(r.Header.Values("X-Conductor-Actor")) != 0 ||
			r.Header.Get("X-Conductor-Workspace") != "team" || r.Header.Get("X-Conductor-Repository") != "application" {
			t.Error("terminal credentials or scope changed")
		}
		switch r.URL.Path {
		case "/api/v1/session":
			_ = json.NewEncoder(w).Encode(session)
		case "/api/v1/repositories":
			_ = json.NewEncoder(w).Encode(repositories)
		case "/api/v1/changes/" + p.ID:
			_ = json.NewEncoder(w).Encode(p)
		case "/api/v1/changes/" + p.ID + "/approvals":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body) != 2 || body["revision"] != float64(1) || body["digest"] != p.Revision.Digest {
				t.Error("approval did not send only the inspected tuple")
			}
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"revision_conflict","message":"inspect latest"}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, err := client.NewAuthenticated(server.URL, "synthetic.token", "team", "application")
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP = server.Client()
	m, err := sessionModel(context.Background(), c, Options{ID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	m.width, m.height = 120, 45
	// Public client fields and the caller-owned HTTP configuration are detached
	// at startup. The token and request scope are private immutable strings.
	c.Actor, c.BaseURL = "forged", "http://remote.invalid"
	c.HTTP.Timeout, c.HTTP.Transport = time.Nanosecond, nil
	next, cmd := m.Update(startMsg{})
	m = next.(model)
	if m.access.ready || m.pack != nil || cmd == nil {
		t.Fatal("startup skipped server discovery")
	}
	next, read := m.Update(cmd())
	m = next.(model)
	if !m.access.ready || m.pack != nil || read == nil {
		t.Fatal("discovery did not precede package inspection")
	}
	m = runCommand(t, m, read)
	if !strings.Contains(m.View(), "principal: person-reviewer (human)") || strings.Contains(m.View(), "synthetic.token") {
		t.Fatal("terminal did not display only server-derived identity")
	}
	m, cmd = confirmAction(t, m, "approve", "a")
	m = runCommand(t, m, cmd)
	if !m.blocked || !strings.Contains(m.status, "Stale inspection") {
		t.Fatal("conflict did not block further approval")
	}
	mu.Lock()
	want := []string{"GET /api/v1/session", "GET /api/v1/repositories", "GET /api/v1/changes/" + p.ID, "POST /api/v1/changes/" + p.ID + "/approvals"}
	if !reflect.DeepEqual(requests, want) {
		t.Errorf("approval inserted discovery or a refresh: %v", requests)
	}
	mu.Unlock()
	m, cmd = key(m, "r")
	if m.pack != nil || m.access.ready {
		t.Fatal("explicit access refresh retained previous inspection")
	}
	next, read = m.Update(cmd())
	m = runCommand(t, next.(model), read)
	mu.Lock()
	defer mu.Unlock()
	want = append(want, "GET /api/v1/session", "GET /api/v1/repositories", "GET /api/v1/changes/"+p.ID)
	if !reflect.DeepEqual(requests, want) || m.blocked || m.pack == nil {
		t.Fatalf("explicit refresh did not re-establish access and inspection: %v", requests)
	}
}

func TestAuthenticatedDiscoveryFailsClosedOnMissingAmbiguousOrChangedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		change        func(*domain.Session, *domain.RepositoryPage)
	}{
		{"invalid principal", "invalid principal", func(s *domain.Session, _ *domain.RepositoryPage) { s.Principal.Kind = "architect" }},
		{"changed principal", "principal changed", func(s *domain.Session, _ *domain.RepositoryPage) { s.Principal.ID = "another-human" }},
		{"changed kind", "principal changed", func(s *domain.Session, _ *domain.RepositoryPage) { s.Principal.Kind = "agent" }},
		{"duplicate workspace", "ambiguous workspace", func(s *domain.Session, _ *domain.RepositoryPage) {
			s.Workspaces = append(s.Workspaces, s.Workspaces[0])
		}},
		{"workspace missing", "active memberships", func(s *domain.Session, _ *domain.RepositoryPage) { s.Workspaces = nil }},
		{"workspace missing from truncated page", "truncated", func(s *domain.Session, _ *domain.RepositoryPage) { s.Workspaces = nil; s.Truncated = true }},
		{"oversized workspace discovery", "page bounds", func(s *domain.Session, _ *domain.RepositoryPage) { s.Workspaces = make([]domain.Workspace, 101) }},
		{"duplicate repository", "ambiguous repository", func(_ *domain.Session, r *domain.RepositoryPage) {
			r.Repositories = append(r.Repositories, r.Repositories[0])
		}},
		{"wrong repository workspace", "ambiguous repository", func(_ *domain.Session, r *domain.RepositoryPage) { r.Repositories[0].WorkspaceID = "other" }},
		{"unreadable repository", "ambiguous repository", func(_ *domain.Session, r *domain.RepositoryPage) { r.Repositories[0].CanRead = false }},
		{"repository missing", "readable repositories", func(_ *domain.Session, r *domain.RepositoryPage) { r.Repositories = nil }},
		{"repository missing from truncated page", "truncated", func(_ *domain.Session, r *domain.RepositoryPage) { r.Repositories = nil; r.Truncated = true }},
		{"oversized repository discovery", "page bounds", func(_ *domain.Session, r *domain.RepositoryPage) {
			r.Repositories = make([]domain.ManagedRepository, 101)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, r, _ := accessFixture(t)
			tc.change(&s, &r)
			m := authenticatedModel(t, nil)
			m.serial = 1
			m.draft, m.pending = domain.Content{"private": "preview"}, request{op: "approve"}
			next, cmd := m.Update(result{request: request{serial: 1, op: "access"}, session: s, repositories: r})
			m = next.(model)
			if cmd != nil || m.access.ready || m.pack != nil || m.draft != nil || m.pending.op != "" || !strings.Contains(m.status, tc.message) {
				t.Fatalf("invalid discovery retained authority: %s", m.status)
			}
			if strings.Contains(tc.name, "changed") {
				m, cmd = key(m, "r")
				if cmd != nil || !m.access.restartRequired {
					t.Fatal("principal switch did not require a new terminal session")
				}
			}
		})
	}
	s, r, _ := accessFixture(t)
	s.Truncated, r.Truncated = true, true
	m := authenticatedModel(t, nil)
	a, err := m.access.inspect(s, r)
	if err != nil || !a.ready || !a.truncated {
		t.Fatalf("selected scope should remain usable with explicit truncation: %v", err)
	}
	m.access = a
	if !strings.Contains(m.View(), "Discovery truncated") {
		t.Fatal("truncation warning was hidden")
	}
}

func TestAuthenticatedCapabilityGuardsAtConfirmationAndDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, kind      string
		author, approve bool
	}{
		{"human author", "human", true, false},
		{"human reviewer", "human", false, true},
		{"read only", "human", false, false},
		{"agent with misleading approve grant", "agent", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := authenticatedModel(t, func(request) (tea.Cmd, context.CancelFunc) { return func() tea.Msg { return nil }, func() {} })
			m.access.principal.Kind = tc.kind
			m.access.repository.CanAuthor, m.access.repository.CanApprove = tc.author, tc.approve
			for _, action := range []struct{ op, key string }{{"create", "c"}, {"revise", "e"}, {"submit", "u"}, {"approve", "a"}} {
				copy := m
				p := *m.pack
				copy.pack = &p
				if action.op == "submit" {
					copy.pack.Revision.SubmittedAt = nil
				}
				allowed := tc.author
				if action.op == "approve" {
					allowed = tc.approve && tc.kind == "human"
				}
				copy, cmd := key(copy, action.key)
				if cmd != nil || (copy.prompt != "" || copy.editor != nil) != allowed {
					t.Fatalf("wrong %s confirmation availability: %q, %s", action.op, copy.prompt, copy.status)
				}
				// A retained confirmation cannot bypass a newly invalidated grant.
				copy.access.repository.CanAuthor, copy.access.repository.CanApprove = false, false
				next, cmd := copy.start(request{op: action.op, accessGeneration: copy.access.generation})
				if cmd != nil || next.(model).prompt != "" {
					t.Fatalf("dispatch bypassed %s capability", action.op)
				}
			}
		})
	}
	m := authenticatedModel(t, nil)
	m.pack.Revision.Author = m.access.principal.ID
	m, cmd := key(m, "a")
	if cmd != nil || m.prompt != "" || !strings.Contains(m.status, "own revision") {
		t.Fatal("server principal's self approval was enabled")
	}
}

func TestEveryAuthenticationDenialClearsInspectionAndRequiresExplicitAccessRead(t *testing.T) {
	for _, status := range []int{401, 403} {
		for _, op := range []string{"access", "list", "open", "create", "revise", "submit", "approve"} {
			t.Run(http.StatusText(status)+"/"+op, func(t *testing.T) {
				var sent []request
				execute := func(r request) (tea.Cmd, context.CancelFunc) {
					sent = append(sent, r)
					return func() tea.Msg { return nil }, func() {}
				}
				m := authenticatedModel(t, execute)
				old := *m.pack
				m.serial, m.busy = 7, op
				m.draft = domain.Content{"private": "discard"}
				m.prompt, m.input, m.pending = "approve", "approve", request{op: "approve", digest: old.Revision.Digest}
				m.page.Changes = []domain.ChangeSummary{{ID: "secret"}}
				next, cmd := m.Update(result{request: request{serial: 7, op: op, id: old.ID}, err: &client.APIError{StatusCode: status, Code: "denied"}})
				m = next.(model)
				if cmd != nil || m.access.ready || m.access.repository.CanRead || m.pack != nil || m.draft != nil || len(m.page.Changes) != 0 || len(m.lines) != 0 || m.prompt != "" || m.pending.op != "" {
					t.Fatal("denial retained protected review state")
				}
				for _, k := range []string{"a", "c", "e", "s", "o", "b", "n", "enter"} {
					m, cmd = key(m, k)
					if cmd != nil || m.prompt != "" {
						t.Fatalf("denial allowed %s before access reinspection", k)
					}
				}
				next, cmd = m.Update(result{request: request{serial: 7, op: "open", id: old.ID}, pack: old})
				m = next.(model)
				if cmd != nil || m.pack != nil || m.access.ready {
					t.Fatal("late response restored denied inspection")
				}
				m, cmd = key(m, "r")
				if cmd == nil || len(sent) != 1 || sent[0].op != "access" {
					t.Fatal("recovery did not start with explicit access discovery")
				}
			})
		}
	}
}

func TestAuthenticatedResponsesCannotChangeRequestedPackageOrScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*result)
	}{
		{"wrong package ID", func(r *result) { r.pack.ID = "another" }},
		{"wrong revision owner", func(r *result) { r.pack.Revision.ChangeID = "another" }},
		{"wrong workspace", func(r *result) { r.pack.WorkspaceID = "another" }},
		{"wrong repository", func(r *result) { r.pack.RepositoryID = "another" }},
		{"wrong list scope", func(r *result) {
			r.op = "list"
			r.page.Changes = []domain.ChangeSummary{{ID: "secret", Revision: 1, WorkspaceID: "another"}}
		}},
		{"duplicate list IDs", func(r *result) {
			r.op = "list"
			c := domain.ChangeSummary{ID: "duplicate", Revision: 1, WorkspaceID: "team", RepositoryID: "application"}
			r.page.Changes = []domain.ChangeSummary{c, c}
		}},
		{"oversized cursor", func(r *result) { r.op = "list"; r.page.NextBefore = strings.Repeat("x", 1025) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := authenticatedModel(t, nil)
			m.serial = 1
			r := result{request: request{op: "open", id: m.pack.ID, serial: 1}, pack: *m.pack}
			tc.change(&r)
			next, cmd := m.Update(r)
			m = next.(model)
			if cmd != nil || m.pack != nil || m.access.ready || !strings.Contains(m.status, errResponseScope.Error()) {
				t.Fatal("invalid response became inspected work")
			}
		})
	}
}

func TestTerminalModeAndScopeValidatedBeforeIO(t *testing.T) {
	for _, tc := range []struct {
		actor, clientActor, workspace, repository string
		authenticated                             bool
	}{
		{authenticated: true}, {authenticated: true, workspace: "team"}, {authenticated: true, repository: "application"},
		{authenticated: true, workspace: "team", repository: "application", actor: "forged"},
		{authenticated: true, workspace: "team", repository: "application", clientActor: "forged"},
		{actor: "reviewer", clientActor: "another"}, {},
	} {
		var c *client.Client
		if tc.authenticated {
			c, _ = client.NewAuthenticated("https://api.invalid", "synthetic.token", tc.workspace, tc.repository)
		} else {
			c = client.New("http://127.0.0.1", tc.clientActor)
		}
		c.Actor = tc.clientActor
		if _, err := sessionModel(context.Background(), c, Options{Actor: tc.actor}); err == nil {
			t.Fatal("invalid terminal mode reached startup")
		}
	}
	if _, err := sessionModel(context.Background(), nil, Options{}); err == nil {
		t.Fatal("nil client accepted")
	}
}

func TestAuthenticationRefreshCancellationAndObsoleteConfirmation(t *testing.T) {
	var cancelled bool
	m := authenticatedModel(t, func(request) (tea.Cmd, context.CancelFunc) {
		return func() tea.Msg { return nil }, func() { cancelled = true }
	})
	m, _ = key(m, "a")
	pending := m.pending
	next, _ := m.recheckAccess(m.pack.ID)
	m = next.(model)
	serial := m.serial
	m, _ = key(m, "esc")
	if !cancelled || m.access.ready || m.pack != nil {
		t.Fatal("cancelled access refresh retained inspection")
	}
	s, r, _ := accessFixture(t)
	next, cmd := m.Update(result{request: request{serial: serial, op: "access"}, session: s, repositories: r})
	m = next.(model)
	if cmd != nil || m.access.ready {
		t.Fatal("late access discovery became current")
	}
	a, err := m.access.inspect(s, r)
	if err != nil {
		t.Fatal(err)
	}
	m.access, m.blocked = a, false
	next, cmd = m.start(pending)
	if cmd != nil || !strings.Contains(next.(model).status, "access changed") {
		t.Fatal("old confirmation survived access generation change")
	}
}
