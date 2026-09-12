package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

func fixture(t *testing.T, revision int64) domain.Package {
	t.Helper()
	content := domain.Content{"intent": "synthetic terminal review", "future": map[string]any{"keep": true}}
	digest, err := domain.Digest(content)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	return domain.Package{ID: "synthetic-package", Revision: domain.Revision{
		ChangeID: "synthetic-package", Number: revision, SchemaVersion: 1, Digest: digest,
		Content: content, Author: "author", CreatedAt: now, SubmittedAt: &now,
	}}
}

func key(m model, k string) (model, tea.Cmd) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	switch k {
	case "enter":
		msg.Type = tea.KeyEnter
	case "esc":
		msg.Type = tea.KeyEsc
	case "ctrl+c":
		msg.Type = tea.KeyCtrlC
	case "end":
		msg.Type = tea.KeyEnd
	case "home":
		msg.Type = tea.KeyHome
	}
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func runCommand(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected command")
	}
	next, follow := m.Update(cmd())
	if follow != nil {
		t.Fatal("unexpected automatic follow-up request")
	}
	return next.(model)
}

func confirmAction(t *testing.T, m model, action, actionKey string) (model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	m, cmd = key(m, actionKey)
	if cmd != nil || m.prompt != action {
		t.Fatalf("action did not require confirmation: %q", m.prompt)
	}
	m, cmd = key(m, action)
	if cmd != nil {
		t.Fatal("typing confirmation sent command early")
	}
	return key(m, "enter")
}

func TestApprovalUsesInspectedTupleAndConflictRequiresExplicitRead(t *testing.T) {
	p := fixture(t, 1)
	var mu sync.Mutex
	var requests []string
	latest := p
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Header.Get("X-Conductor-Actor") != "reviewer" {
			t.Error("local actor header changed")
		}
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(latest)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 2 || body["revision"] != float64(1) || body["digest"] != p.Revision.Digest {
			t.Errorf("approval tuple or command fields changed: %#v", body)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"revision_conflict","message":"inspect latest","correlationId":"synthetic"}}`))
	}))
	defer server.Close()
	m := newModel(Options{Actor: "reviewer", ID: p.ID}, runner(context.Background(), client.New(server.URL, "reviewer")))
	next, cmd := m.Update(startMsg{})
	m = runCommand(t, next.(model), cmd)
	mu.Lock()
	latest.Revision.Number = 2
	mu.Unlock()
	m, cmd = confirmAction(t, m, "approve", "a")
	if !strings.Contains(m.View(), p.Revision.Digest) {
		t.Fatal("inspected digest not visible during confirmation")
	}
	m = runCommand(t, m, cmd)
	if !m.blocked || m.pack.Revision.Number != 1 || !strings.Contains(m.status, "Stale inspection") {
		t.Fatalf("conflict did not preserve blocked inspection: %+v", m)
	}
	m, cmd = key(m, "a")
	if cmd != nil || m.prompt != "" {
		t.Fatal("conflict allowed another approval")
	}
	mu.Lock()
	if len(requests) != 2 || requests[0] != "GET /api/v1/changes/"+p.ID || requests[1] != "POST /api/v1/changes/"+p.ID+"/approvals" {
		t.Fatalf("approval performed an implicit read: %v", requests)
	}
	mu.Unlock()
	m, cmd = key(m, "r")
	m = runCommand(t, m, cmd)
	if m.blocked || m.pack.Revision.Number != 2 {
		t.Fatal("explicit read did not renew inspection")
	}
}

func TestMutationErrorsAndRacingResponsesBlockFurtherWrites(t *testing.T) {
	p := fixture(t, 1)
	for _, tc := range []struct {
		name, op string
		err      error
		response domain.Package
	}{
		{"network uncertainty", "approve", errors.New("connection closed"), domain.Package{}},
		{"server error", "submit", &client.APIError{StatusCode: 503, Code: "unavailable"}, domain.Package{}},
		{"approval returns newer revision", "approve", nil, fixture(t, 2)},
		{"submit returns newer revision", "submit", nil, fixture(t, 2)},
		{"revise returns later revision", "revise", nil, fixture(t, 3)},
		{"create returns later revision", "create", nil, fixture(t, 2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(Options{Actor: "reviewer"}, nil)
			m.pack, m.serial, m.busy = &p, 1, tc.op
			next, cmd := m.Update(result{request: request{serial: 1, op: tc.op, id: p.ID, revision: 1, digest: p.Revision.Digest, content: p.Revision.Content}, err: tc.err, pack: tc.response})
			m = next.(model)
			if cmd != nil || !m.blocked || m.pack.Revision.Number != 1 {
				t.Fatal("uncertain result replaced inspection or allowed writes")
			}
			for _, action := range []string{"a", "s", "e", "c"} {
				m, cmd = key(m, action)
				if cmd != nil || m.prompt != "" {
					t.Fatalf("uncertain write allowed %s", action)
				}
			}
		})
	}
}

func TestApprovalSuccessAndSelfApprovalGuard(t *testing.T) {
	p := fixture(t, 1)
	m := newModel(Options{Actor: "author"}, nil)
	m.pack = &p
	m, cmd := key(m, "a")
	if cmd != nil || m.prompt != "" || !strings.Contains(m.status, "own revision") {
		t.Fatal("self approval was enabled")
	}
	m.options.Actor = "reviewer"
	m.serial, m.busy = 1, "approve"
	p.Approved, p.Approval = true, &domain.Approval{ChangeID: p.ID, Revision: 1, Digest: p.Revision.Digest, Reviewer: "reviewer"}
	next, cmd := m.Update(result{request: request{serial: 1, op: "approve", id: p.ID, revision: 1, digest: p.Revision.Digest}, pack: p})
	m = next.(model)
	if cmd != nil || m.blocked || !strings.Contains(m.status, "Design approval recorded") {
		t.Fatal("confirmed approval not shown")
	}
	if !strings.Contains(m.status, "verification, merge and deployment are separate") {
		t.Fatal("approval conflated delivery outcomes")
	}
}

func TestCancellationAndLateResponses(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	p := fixture(t, 1)
	m := newModel(Options{Actor: "reviewer"}, runner(context.Background(), client.New(server.URL, "reviewer")))
	m.pack = &p
	m, cmd := confirmAction(t, m, "approve", "a")
	response := make(chan tea.Msg, 1)
	go func() { response <- cmd() }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP operation never started")
	}
	m, _ = key(m, "esc")
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP context not cancelled")
	}
	var late tea.Msg
	select {
	case late = <-response:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled command never returned")
	}
	serial := m.serial
	next, follow := m.Update(late)
	m = next.(model)
	if follow != nil || m.serial != serial || !m.blocked || !strings.Contains(m.status, "outcome unknown") {
		t.Fatal("late response replaced cancelled state")
	}
	quitCancelled := false
	m.cancel = func() { quitCancelled = true }
	_, _ = key(m, "ctrl+c")
	if !quitCancelled {
		t.Fatal("quit did not cancel pending operation")
	}
}

func TestFilePreviewRequiresConfirmationAndPreservesExtensions(t *testing.T) {
	p := fixture(t, 7)
	p.Approved = true
	var sent request
	execute := func(req request) (tea.Cmd, context.CancelFunc) {
		sent = req
		return func() tea.Msg { return nil }, func() {}
	}
	m := newModel(Options{Actor: "author", File: "synthetic.json"}, execute)
	m.pack = &p
	m, cmd := key(m, "e")
	if cmd != nil || m.prompt != "file" || m.input != "synthetic.json" {
		t.Fatal("revise did not request an explicit file")
	}
	m, cmd = key(m, "enter")
	if cmd == nil || sent.op != "file" {
		t.Fatal("file import not requested")
	}
	content := domain.Content{"intent": "changed", "future": map[string]any{"opaque": []any{true, "keep"}}}
	next, follow := m.Update(result{request: sent, content: content})
	m = next.(model)
	if follow != nil || m.draft == nil || !strings.Contains(m.View(), "not saved") {
		t.Fatal("import did not stop at preview")
	}
	if !strings.Contains(m.View(), "Base state: design approved") || !strings.Contains(m.View(), "base author:") {
		t.Fatal("preview must distinguish the base approval from the unsaved replacement")
	}
	m, cmd = confirmAction(t, m, "revise", "s")
	if cmd == nil || sent.op != "revise" || sent.revision != 7 || sent.id != p.ID || sent.content["future"] == nil {
		t.Fatalf("revision discarded fields or inspected tuple: %+v", sent)
	}
}

func TestViewportSanitizationAndSmallTerminalActionGuards(t *testing.T) {
	p := fixture(t, 1)
	p.Revision.Content["large"] = strings.Repeat("synthetic ", 1000) + "FINAL_CONTENT_MARKER"
	p.Revision.Content["hostile"] = "\x1b]52;c;clipboard\a\u202e\r\n"
	m := newModel(Options{Actor: "reviewer"}, nil)
	m.pack = &p
	m.width, m.height = 50, 20
	m.rebuild()
	m, _ = key(m, "end")
	if m.offset == 0 || len(m.lines) < 100 {
		t.Fatal("large content did not scroll")
	}
	all := strings.Join(m.lines, "")
	if !strings.Contains(all, "FINAL_CONTENT_MARKER") || !strings.Contains(all, `"future"`) {
		t.Fatal("content was truncated or ASCII JSON escaped")
	}
	for _, r := range m.View() {
		if r != '\n' && (r < 0x20 || r > 0x7e) {
			t.Fatalf("unsafe terminal rune: %U", r)
		}
	}
	if rows := strings.Split(m.View(), "\n"); len(rows) > m.height {
		t.Fatalf("render exceeds height: %d", len(rows))
	}
	for _, row := range strings.Split(m.View(), "\n") {
		if len(row) > m.width {
			t.Fatal("render exceeds width")
		}
	}
	m.options.Actor = strings.Repeat("r", 128)
	m.width, m.height = 36, 14
	if !m.tooSmall() {
		t.Fatal("dynamic metadata did not need more height")
	}
	m, cmd := key(m, "a")
	if cmd != nil || m.prompt != "" {
		t.Fatal("approval enabled while metadata hidden")
	}
	m.prompt, m.input = "approve", "approve"
	m, cmd = key(m, "enter")
	if cmd != nil {
		t.Fatal("resizing hid metadata but confirmation still sent")
	}
}

func TestPromptInputIsBoundedAndPastedControlsCannotConfirm(t *testing.T) {
	m := newModel(Options{}, nil)
	m.prompt = "file"
	m, cmd := key(m, strings.Repeat("x", 10000)+"\x1b\r\n\u202e")
	if cmd != nil || len(m.input) != 4096 || strings.ContainsAny(m.input, "\x1b\r\n\u202e") {
		t.Fatal("prompt input not bounded or sanitized")
	}
	m.prompt, m.input = "approve", ""
	m, cmd = key(m, "approve\r")
	if cmd != nil || m.busy != "" {
		t.Fatal("pasted newline submitted approval")
	}
}
