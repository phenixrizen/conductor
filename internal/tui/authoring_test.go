package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

func TestGuidedChangeRequiresIntentPreviewsThenSavesWithoutSubmitting(t *testing.T) {
	var sent []request
	m := newModel(Options{Actor: "author"}, func(req request) (tea.Cmd, context.CancelFunc) {
		sent = append(sent, req)
		return func() tea.Msg { return nil }, func() {}
	})
	m, _ = key(m, "c")
	if m.editor == nil || !strings.Contains(m.View(), "New Change") {
		t.Fatal("new change did not open form")
	}
	m, _ = key(m, "ctrl+s")
	if m.draft != nil || !strings.Contains(m.status, "Change title and Intended outcome") {
		t.Fatal("missing intent accepted")
	}
	for _, input := range []string{"enter", "Synthetic useful change", "tab", "enter", "First line", "enter", "Second line", "ctrl+s"} {
		m, _ = key(m, input)
	}
	if len(sent) != 0 || m.editor != nil || m.draft["title"] != "Synthetic useful change" || m.draft["intent"] != "First line\nSecond line" {
		t.Fatalf("wrong preview: %#v, requests=%v", m.draft, sent)
	}
	if len(m.draft) != 2 {
		t.Fatal("empty optional fields synthesized")
	}
	m, _ = key(m, "u")
	if len(sent) != 0 || m.prompt != "" {
		t.Fatal("unsaved draft submitted")
	}
	m, cmd := confirmAction(t, m, "create", "s")
	if cmd == nil || len(sent) != 1 || sent[0].op != "create" {
		t.Fatal("save did not send exact create")
	}
	p := fixture(t, 1)
	p.Revision.Content = sent[0].content
	p.Revision.Digest, _ = domain.Digest(p.Revision.Content)
	p.Revision.SubmittedAt = nil
	next, follow := m.Update(result{request: sent[0], pack: p})
	m = next.(model)
	if follow != nil || m.pack.Revision.SubmittedAt != nil {
		t.Fatal("save submitted implicitly")
	}
	m, cmd = key(m, "s")
	if cmd != nil || m.prompt != "" || len(sent) != 1 {
		t.Fatal("save key submitted")
	}
	m, cmd = confirmAction(t, m, "submit", "u")
	if cmd == nil || len(sent) != 2 || sent[1].op != "submit" || sent[1].revision != 1 {
		t.Fatal("explicit submit did not capture saved revision")
	}
}

func TestGuidedRevisionPreservesStructuredFieldsAndExactBase(t *testing.T) {
	p := fixture(t, 7)
	p.Revision.Content = domain.Content{"intent": map[string]any{"request": "legacy"}, "scope": nil,
		"tasks": []any{"original"}, "verification": true, "design": "before", "extension": json.Number("12345678901234567890")}
	p.Revision.Digest, _ = domain.Digest(p.Revision.Content)
	original, _ := json.Marshal(p.Revision.Content)
	var sent request
	m := newModel(Options{Actor: "author"}, func(req request) (tea.Cmd, context.CancelFunc) {
		sent = req
		return func() tea.Msg { return nil }, func() {}
	})
	m.pack = &p
	m, _ = key(m, "e")
	m, _ = key(m, "ctrl+s")
	if m.draft != nil || !strings.Contains(m.status, "No design changes") {
		t.Fatal("no-op edit created a revision")
	}
	for _, input := range []string{"tab", "enter", "tab", "enter", "tab", "enter", "ctrl+u", "after", "ctrl+s"} {
		m, _ = key(m, input)
	}
	if m.editor != nil || m.draft["design"] != "after" {
		t.Fatal("design edit missing")
	}
	for _, field := range []string{"intent", "scope", "tasks", "verification", "extension"} {
		if !reflect.DeepEqual(m.draft[field], p.Revision.Content[field]) {
			t.Fatalf("changed structured %s", field)
		}
	}
	if _, exists := m.draft["title"]; exists {
		t.Fatal("absent title was added")
	}
	after, _ := json.Marshal(p.Revision.Content)
	if string(original) != string(after) {
		t.Fatal("form mutated inspected content")
	}
	m, _ = key(m, "e")
	if m.editor == nil || m.editor.values[3] != "after" {
		t.Fatal("preview cannot be edited")
	}
	m, _ = key(m, "ctrl+s")
	m, cmd := confirmAction(t, m, "revise", "s")
	if cmd == nil || sent.revision != 7 || sent.digest != p.Revision.Digest || sent.id != p.ID || sent.content["design"] != "after" {
		t.Fatalf("lost exact capture: %#v", sent)
	}
}

func TestDesignInputIsBoundedAndPastedKeysRemainText(t *testing.T) {
	m := newModel(Options{Actor: "author"}, nil)
	m, _ = key(m, "c")
	m, _ = key(m, "enter")
	m, cmd := key(m, "q\nsubmit\x13\x1b]52;c;clipboard\a\u202e")
	if cmd != nil || m.editor == nil || m.draft != nil {
		t.Fatal("pasted input triggered a command")
	}
	if strings.ContainsAny(m.editor.values[0], "\x13\x1b\a\u202e") {
		t.Fatal("pasted terminal controls accepted")
	}
	before := m.editor.values[0]
	m, _ = key(m, strings.Repeat("x", maxDesignBytes))
	if m.editor.values[0] != before || !strings.Contains(m.status, "limited") {
		t.Fatal("oversized input not rejected")
	}
	m, _ = key(m, "ctrl+u")
	m, _ = key(m, strings.Repeat("\n", maxDesignBytes/2))
	if m.editor.values[0] != "" {
		t.Fatal("escaped JSON exceeded total content bound")
	}
	m.invalidateAccess()
	if m.editor != nil || m.draft != nil || m.lines != nil {
		t.Fatal("denial retained authoring text")
	}
}

func TestReadableDesignAndAdvancedJSONRetainCompleteInspection(t *testing.T) {
	p := fixture(t, 1)
	p.Revision.Content["title"] = "Useful change"
	p.Revision.Content["intent"] = "First line\nSecond line"
	p.Revision.Content["tasks"] = []any{"Structured task", "\x1b]52;c;clipboard\a"}
	m := newModel(Options{Actor: "reviewer"}, nil)
	m.pack, m.height = &p, 45
	m.rebuild()
	readable := strings.Join(m.lines, "\n")
	if !strings.Contains(readable, "Change title\nUseful change") || !strings.Contains(readable, "Intended outcome\nFirst line\nSecond line") || !strings.Contains(readable, "Structured task") {
		t.Fatal("design is not readable")
	}
	m, _ = key(m, "J")
	if !strings.Contains(strings.Join(m.lines, "\n"), `"schemaVersion"`) || !strings.Contains(m.View(), p.Revision.Digest) {
		t.Fatal("full JSON or inspected digest missing")
	}
	if strings.Contains(m.View(), "\x1b]52;") {
		t.Fatal("source controls became active")
	}
}

func TestDesignBoundIncludesCommandEnvelopeAndRevision(t *testing.T) {
	for _, revision := range []int64{0, 7, 9223372036854775807} {
		command := map[string]any{"content": domain.Content{"title": ""}}
		if revision > 0 {
			command["expectedRevision"] = revision
		}
		empty, _ := json.Marshal(command)
		content := domain.Content{"title": strings.Repeat("x", maxDesignBytes-len(empty)-1)}
		if err := boundedDesign(content, revision); err != nil {
			t.Fatalf("exact boundary rejected: %v", err)
		}
		content["title"] = content["title"].(string) + "x"
		if boundedDesign(content, revision) == nil {
			t.Fatal("oversized command accepted")
		}
	}
	m := newModel(Options{Actor: "author"}, nil)
	m, _ = key(m, "c")
	m, _ = key(m, "enter")
	for _, input := range []string{"Some", " ", "words"} {
		m, _ = key(m, input)
	}
	if m.editor.values[0] != "Some words" {
		t.Fatal("terminal space key lost")
	}
}

func TestEditingImportedCreatePreservesLegacyBasicsAndCancelRestoresPreview(t *testing.T) {
	m := newModel(Options{Actor: "author"}, nil)
	m.draftOp = "create"
	m.draft = domain.Content{"title": map[string]any{"legacy": "title"}, "intent": nil, "design": "before"}
	original, _ := json.Marshal(m.draft)
	m, _ = key(m, "e")
	if m.editor == nil || m.editor.requireBasics || m.editor.editable[0] || m.editor.editable[1] {
		t.Fatal("imported basics were made mandatory/editable")
	}
	for _, input := range []string{"tab", "tab", "tab", "enter", "ctrl+u", "after", "esc", "esc"} {
		m, _ = key(m, input)
	}
	afterCancel, _ := json.Marshal(m.draft)
	if m.editor != nil || string(afterCancel) != string(original) {
		t.Fatal("cancel changed imported preview")
	}
	m, _ = key(m, "e")
	for _, input := range []string{"tab", "tab", "tab", "enter", "ctrl+u", "after", "ctrl+s"} {
		m, _ = key(m, input)
	}
	if m.editor != nil || m.draft["design"] != "after" || m.draftGuided {
		t.Fatal("legacy imported preview could not be edited")
	}
	if !reflect.DeepEqual(m.draft["title"], map[string]any{"legacy": "title"}) || m.draft["intent"] != nil {
		t.Fatal("legacy basics changed")
	}
}

func TestDesignControlNamesAndBracketedPasteAreText(t *testing.T) {
	for _, paste := range []bool{false, true} {
		m := newModel(Options{Actor: "author"}, nil)
		m, _ = key(m, "c")
		m, _ = key(m, "enter")
		for _, text := range []string{"enter", "tab", "end", "ctrl+s", "ctrl+c", "esc", "q"} {
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(text), Paste: paste})
			m = next.(model)
			if cmd != nil || m.editor == nil || !m.editor.typing {
				t.Fatal("typed key name triggered a control")
			}
		}
		if m.editor.values[0] != "entertabendctrl+sctrl+cescq" {
			t.Fatal("key name text changed")
		}
	}
}

func TestFailedDesignRetainsExactInputAcrossExplicitInspection(t *testing.T) {
	for _, failure := range []error{&client.APIError{StatusCode: 409}, errors.New("lost response")} {
		session, repositories, latest := accessFixture(t)
		latest.Revision.Number = 2
		latest.Revision.Content = domain.Content{"title": "Concurrent saved title", "intent": "Other engineer's work"}
		latest.Revision.Digest, _ = domain.Digest(latest.Revision.Content)
		var requests []request
		execute := func(req request) (tea.Cmd, context.CancelFunc) {
			requests = append(requests, req)
			return func() tea.Msg {
				res := result{request: req}
				switch req.op {
				case "access":
					res.session, res.repositories = session, repositories
				case "open":
					res.pack = latest
				default:
					res.err = failure
				}
				return res
			}, func() {}
		}
		m := authenticatedModel(t, execute)
		originalDigest := m.pack.Revision.Digest
		for _, input := range []string{"e", "enter", "Keep my typed title", "ctrl+s"} {
			m, _ = key(m, input)
		}
		retained, _ := json.Marshal(m.draft)
		m, cmd := confirmAction(t, m, "revise", "s")
		m = runCommand(t, m, cmd)
		if !m.blocked || m.recovery == nil {
			t.Fatal("failed revision lost its typed input")
		}
		m, cmd = key(m, "v")
		if cmd != nil || m.recoveryPreview {
			t.Fatal("recovery skipped current inspection")
		}
		m, cmd = key(m, "r")
		next, follow := m.Update(cmd())
		m = runCommand(t, next.(model), follow)
		if m.blocked || m.recovery == nil || m.draft != nil || m.pack.Revision.Number != 2 {
			t.Fatal("explicit inspection lost recovery or applied it silently")
		}
		m, cmd = key(m, "v")
		if cmd != nil || len(requests) != 3 || !m.recoveryPreview || !strings.Contains(m.View(), originalDigest) {
			t.Fatal("recovery did hidden I/O or hid original capture")
		}
		actual, _ := json.Marshal(m.draft)
		if string(actual) != string(retained) {
			t.Fatal("recovery merged or changed retained input")
		}
		comparison := strings.Join(m.lines, "\n")
		if !strings.Contains(comparison, "Concurrent saved title") || !strings.Contains(comparison, "Keep my typed title") {
			t.Fatal("complete saved/replacement comparison missing")
		}
		m, cmd = confirmAction(t, m, "revise", "s")
		if cmd == nil || requests[3].revision != 2 || requests[3].digest != latest.Revision.Digest {
			t.Fatal("fresh confirmation did not bind inspected current state")
		}
	}
}

func TestRetainedDesignClearsOnDeniedAccessAndRecognizesSavedContent(t *testing.T) {
	m := authenticatedModel(t, nil)
	m.recovery = &designRecovery{op: "revise", id: m.pack.ID, content: m.pack.Revision.Content}
	m, cmd := key(m, "v")
	if cmd != nil || m.draft != nil || m.recovery != nil || !strings.Contains(m.status, "matches") {
		t.Fatal("already saved content offered a duplicate revision")
	}
	m.recovery = &designRecovery{op: "revise", id: m.pack.ID, content: domain.Content{"intent": "private draft"}}
	id := m.pack.ID
	next, cmd := m.Update(result{request: request{serial: m.serial, op: "access"}, err: errors.New("temporary connection failure")})
	m = next.(model)
	if cmd != nil || m.recovery == nil || m.access.ready || strings.Contains(m.View(), "private draft") {
		t.Fatal("failed access transport discarded or exposed unverified recovery input")
	}
	next, cmd = m.Update(result{request: request{serial: m.serial, op: "open", id: id}, err: &client.APIError{StatusCode: http.StatusForbidden}})
	m = next.(model)
	if cmd != nil || m.recovery != nil || m.draft != nil || m.editor != nil || m.pack != nil {
		t.Fatal("source denial retained private draft")
	}
}

func TestCancelledCreateRetainsInputWithoutReplay(t *testing.T) {
	m := newModel(Options{Actor: "author"}, func(req request) (tea.Cmd, context.CancelFunc) {
		return func() tea.Msg { return result{request: req, page: domain.ChangePage{}} }, func() {}
	})
	for _, input := range []string{"c", "enter", "A new change", "tab", "enter", "My intended outcome", "ctrl+s"} {
		m, _ = key(m, input)
	}
	m, _ = confirmAction(t, m, "create", "s")
	m, _ = key(m, "esc")
	if m.recovery == nil || !m.blocked {
		t.Fatal("cancelled create input lost")
	}
	m, cmd := key(m, "r")
	m = runCommand(t, m, cmd)
	m, cmd = key(m, "v")
	if cmd != nil || m.draft["title"] != "A new change" || !strings.Contains(m.status, "duplicate") || !m.draftGuided {
		t.Fatal("unknown create was replayed or lost its input/origin")
	}
}
