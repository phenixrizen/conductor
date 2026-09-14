package tui

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

func assistanceFixture(t *testing.T, m model) domain.DesignAssistance {
	t.Helper()
	base := m.pack.Revision
	c := domain.DesignAssistance{ID: strings.Repeat("a", 32), WorkspaceID: m.access.workspaceID, RepositoryID: m.access.repositoryID, RequesterID: m.access.principal.ID, CreatedAt: time.Now().UTC(), Base: base,
		Input: domain.AssistanceInput{ChangeID: base.ChangeID, ExpectedRevision: base.Number, ExpectedDigest: base.Digest, Instruction: "Explain the intended outcome and design", Sections: []string{"intent", "design"}}}
	var err error
	c.Digest, err = domain.DesignAssistanceDigest(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Suggestion = &domain.AssistanceSuggestion{AgentID: "person-agent", CreatedAt: c.CreatedAt, Sections: map[string]string{"intent": "Suggested outcome", "design": "Suggested approach"}, Note: "Synthetic proposal; checks have not run."}
	c.Suggestion.Digest, err = domain.AssistanceSuggestionDigest(c.Digest, *c.Suggestion)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestAssistanceFormCapturesExactInputAndDoesNotWriteUntilConfirmed(t *testing.T) {
	var sent []request
	m := authenticatedModel(t, func(req request) (tea.Cmd, context.CancelFunc) {
		sent = append(sent, req)
		return func() tea.Msg { return nil }, func() {}
	})
	m.pack.Revision.Content["scope"] = map[string]any{"legacy": true}
	m.pack.Revision.Digest, _ = domain.Digest(m.pack.Revision.Content)
	m.assistanceMode = true
	m, _ = key(m, "c")
	if m.assistance.form == nil || m.assistance.form.editable[2] {
		t.Fatal("form absent or structured section editable")
	}
	for _, k := range []string{"2", "3", "4", "tab", "tab", "tab", "tab", "tab", "tab", "enter", "Help explain the approach", "ctrl+s"} {
		m, _ = key(m, k)
	}
	p := m.assistance.preview
	if p == nil || len(sent) != 0 || !reflect.DeepEqual(p.command.assistanceInput.Sections, []string{"intent", "design"}) || p.command.assistanceInput.ExpectedDigest != m.pack.Revision.Digest || p.command.assistanceInput.Instruction != "Help explain the approach" {
		t.Fatalf("incorrect captured preview: %#v", p)
	}
	if !strings.Contains(strings.Join(m.lines, "\n"), "legacy") || !domain.IsLowerHex(p.command.assistanceKey, 32) {
		t.Fatal("preview omitted complete content or key")
	}
	m, cmd := confirmAction(t, m, "assist-request", "s")
	if cmd == nil || len(sent) != 1 || !reflect.DeepEqual(sent[0].assistanceInput, p.command.assistanceInput) {
		t.Fatal("confirmation refreshed or changed the request")
	}
	c := assistanceFixture(t, m)
	c.Input = p.command.assistanceInput
	c.Suggestion = nil
	c.Digest, _ = domain.DesignAssistanceDigest(c)
	next, follow := m.Update(result{request: sent[0], assistance: c})
	m = next.(model)
	if follow != nil || m.pack.Revision.Number != 1 || !strings.Contains(strings.Join(m.lines, "\n"), "conductor_propose_design_sections") || m.assistance.record == nil {
		t.Fatal("save changed Design, hid handoff or started automatic read")
	}
}
func TestAssistanceSelectedApplicationPreviewsFullMergedContentAndExactPins(t *testing.T) {
	var sent []request
	m := authenticatedModel(t, func(req request) (tea.Cmd, context.CancelFunc) {
		sent = append(sent, req)
		return func() tea.Msg { return nil }, func() {}
	})
	c := assistanceFixture(t, m)
	m.assistanceMode = true
	next, _ := m.assistanceResult(result{request: request{op: "assist-get"}, assistance: c})
	m = next.(model)
	m, _ = key(m, "2") // Keep Design; deselect Intended outcome.
	m, _ = key(m, "a")
	p := m.assistance.preview
	if p == nil || p.merged["intent"] != c.Base.Content["intent"] || p.merged["design"] != "Suggested approach" || !reflect.DeepEqual(p.merged["future"], c.Base.Content["future"]) {
		t.Fatal("selected merge lost existing content")
	}
	preview := strings.Join(m.lines, "\n")
	for _, text := range []string{"Before:", "Proposed:", "COMPLETE MERGED DESIGN", "Suggested approach", "future"} {
		if !strings.Contains(preview, text) {
			t.Fatal("missing preview", text)
		}
	}
	m, cmd := confirmAction(t, m, "assist-apply", "s")
	if cmd == nil || len(sent) != 1 || sent[0].assistanceApply.RequestDigest != c.Digest || sent[0].assistanceApply.SuggestionDigest != c.Suggestion.Digest || !reflect.DeepEqual(sent[0].assistanceApply.Sections, []string{"design"}) {
		t.Fatal("application did not capture exact pins and section subset")
	}
	digest, _ := domain.Digest(p.merged)
	c.Application = &domain.AssistanceApplication{AppliedBy: m.access.principal.ID, CreatedAt: time.Now().UTC(), Revision: 2, RevisionDigest: digest, Sections: []string{"design"}}
	c.Application.Digest, _ = domain.AssistanceApplicationDigest(c.Digest, c.Suggestion.Digest, *c.Application)
	next, follow := m.Update(result{request: sent[0], assistance: c})
	m = next.(model)
	if follow != nil || m.pack != nil || !m.blocked || m.assistance.record.Application == nil || !strings.Contains(m.status, "r inspects") {
		t.Fatal("application falsely presented old Design as latest")
	}
	before := len(sent)
	m, _ = key(m, "a")
	m, _ = key(m, "s")
	if len(sent) != before || m.prompt != "" {
		t.Fatal("applied record enabled another write")
	}
}
func TestAssistanceLostResponseRequiresInspectionAndRetainsExactCommand(t *testing.T) {
	var sent []request
	m := authenticatedModel(t, func(req request) (tea.Cmd, context.CancelFunc) {
		sent = append(sent, req)
		return func() tea.Msg { return nil }, func() {}
	})
	c := assistanceFixture(t, m)
	m.assistanceMode = true
	next, _ := m.assistanceResult(result{request: request{op: "assist-get"}, assistance: c})
	m = next.(model)
	m, _ = key(m, "a")
	captured := m.assistance.preview
	m, _ = confirmAction(t, m, "assist-apply", "s")
	next, _ = m.Update(result{request: sent[0], err: errors.New("lost acknowledgement")})
	m = next.(model)
	if m.assistance.uncertain != captured || !m.blocked || m.assistance.preview != nil {
		t.Fatal("uncertain captured input was lost")
	}
	m, _ = key(m, "v")
	if m.assistance.preview != nil {
		t.Fatal("recovery bypassed explicit inspection")
	}
	m, _ = key(m, "r")
	if m.pack != nil || m.access.ready || m.assistance.uncertain != captured || sent[len(sent)-1].op != "access" {
		t.Fatal("recheck failed to clear private inspection and retain hidden recovery")
	}
	// Complete the same-principal access / Change / assistance inspection chain.
	session, repos, p := accessFixture(t)
	p.Revision.Number = 2
	p.Revision.Content["design"] = "Another later revision"
	p.Revision.Digest, _ = domain.Digest(p.Revision.Content)
	req := sent[len(sent)-1]
	next, cmd := m.Update(result{request: req, session: session, repositories: repos})
	m = next.(model)
	if cmd == nil {
		t.Fatal("access did not inspect Change")
	}
	req = sent[len(sent)-1]
	next, cmd = m.Update(result{request: req, pack: p})
	m = next.(model)
	if cmd == nil {
		t.Fatal("Change inspection did not inspect assistance")
	}
	req = sent[len(sent)-1]
	next, _ = m.Update(result{request: req, assistance: c})
	m = next.(model)
	m, _ = key(m, "v")
	if m.assistance.preview != captured {
		t.Fatal("retained preview was silently rebased")
	}
	since := len(sent)
	m, cmd = confirmAction(t, m, "assist-apply", "s")
	if cmd == nil || len(sent) != since+1 || sent[len(sent)-1].assistanceKey != captured.command.assistanceKey || !reflect.DeepEqual(sent[len(sent)-1].assistanceApply, captured.command.assistanceApply) {
		t.Fatal("retry changed key/input or performed a read")
	}
}
func TestAssistanceDenialFencesPrivateStateAndLateResponses(t *testing.T) {
	m := authenticatedModel(t, func(req request) (tea.Cmd, context.CancelFunc) { return func() tea.Msg { return nil }, func() {} })
	c := assistanceFixture(t, m)
	m.assistanceMode = true
	m.assistance.record = &c
	m.assistance.chosen[1] = true
	m, _ = key(m, "a")
	m.assistance.uncertain = m.assistance.preview
	m.rebuild()
	oldSerial := m.serial
	next, _ := m.Update(result{request: request{op: "assist-get", serial: oldSerial, id: c.Base.ChangeID, assistanceID: c.ID}, err: &client.APIError{StatusCode: http.StatusForbidden}})
	m = next.(model)
	if m.assistance.record != nil || m.assistance.uncertain != nil || m.assistance.preview != nil || m.assistance.form != nil || m.lines != nil || m.pack != nil || m.access.ready {
		t.Fatal("denial retained private assistance or capabilities")
	}
	next, _ = m.Update(result{request: request{op: "assist-get", serial: oldSerial, id: c.Base.ChangeID, assistanceID: c.ID}, assistance: c})
	m = next.(model)
	if m.assistance.record != nil || m.access.ready {
		t.Fatal("late response restored denied content")
	}
}
func TestAssistanceControlsRespectKindsOwnershipLegacyAndLiteralKeys(t *testing.T) {
	for _, kind := range []string{"local", "reader", "agent", "other-human"} {
		t.Run(kind, func(t *testing.T) {
			m := authenticatedModel(t, nil)
			c := assistanceFixture(t, m)
			m.assistanceMode = true
			m.assistance.record = &c
			m.assistance.chosen[1] = true
			switch kind {
			case "local":
				m.access.authenticated = false
			case "reader":
				m.access.repository.CanAuthor = false
			case "agent":
				m.access.principal.Kind = "agent"
			case "other-human":
				c.RequesterID = "different-human"
			}
			m, cmd := key(m, "a")
			if cmd != nil || m.assistance.preview != nil {
				t.Fatal("unavailable application exposed")
			}
			if kind != "other-human" {
				m, _ = key(m, "c")
				if m.assistance.form != nil {
					t.Fatal("unavailable request form exposed")
				}
			}
		})
	}
	m := authenticatedModel(t, nil)
	m.assistanceMode = true
	m, _ = key(m, "c")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ctrl+s")})
	m = next.(model)
	if cmd != nil || m.assistance.form == nil || m.assistance.preview != nil {
		t.Fatal("literal pasted text became command")
	}
	m.assistance.form.selected = 6
	m, _ = key(m, "enter")
	m, _ = key(m, "q\x1b]52;c;secret\a\u202e")
	if strings.ContainsAny(m.assistance.form.instruction, "\x1b\a\u202e") {
		t.Fatal("terminal control input retained")
	}
	m, _ = key(m, strings.Repeat("x", 4097))
	if len(m.assistance.form.instruction) > 4096 {
		t.Fatal("instruction bound exceeded")
	}
}
func TestAssistanceResponseRejectsTamperedBaseSuggestionAndApplication(t *testing.T) {
	m := authenticatedModel(t, nil)
	original := assistanceFixture(t, m)
	for _, tamper := range []string{"base", "request", "suggestion", "application"} {
		t.Run(tamper, func(t *testing.T) {
			c := original
			s := *original.Suggestion
			c.Suggestion = &s
			switch tamper {
			case "base":
				c.Base.Digest = strings.Repeat("0", 64)
			case "request":
				c.Digest = strings.Repeat("0", 64)
			case "suggestion":
				s.Digest = strings.Repeat("0", 64)
			case "application":
				c.Application = &domain.AssistanceApplication{AppliedBy: c.RequesterID, Revision: 2, RevisionDigest: strings.Repeat("0", 64)}
			}
			if m.validateAssistanceScope(result{request: request{op: "assist-get", id: c.Base.ChangeID, assistanceID: c.ID}, assistance: c}) == nil {
				t.Fatal("tampered response accepted")
			}
		})
	}
}
func TestSwitchHeaderIsASCIIResponsiveAndIndependentOfWorkflowState(t *testing.T) {
	m := authenticatedModel(t, nil)
	header := strings.Join(m.header(), "\n")
	if !strings.Contains(header, "/___/") || !strings.Contains(header, "Engineering intent, orchestrated.") {
		t.Fatal("Switch art missing")
	}
	for _, r := range header {
		if r > 127 {
			t.Fatal("brand requires non-ASCII terminal")
		}
	}
	m.height = 24
	if !strings.Contains(strings.Join(m.header(), "\n"), "//====") || strings.Contains(strings.Join(m.header(), "\n"), "/___/") {
		t.Fatal("normal terminal did not use compact ASCII Switch")
	}
	m.width = 50
	if strings.Contains(strings.Join(m.header(), "\n"), "//====") {
		t.Fatal("art consumed narrow terminal inspection")
	}
	m.width, m.height = 80, 23
	if strings.Contains(strings.Join(m.header(), "\n"), "//====") {
		t.Fatal("art consumed very short terminal inspection")
	}
}

func TestAssistanceKnownConflictRequiresFreshInspectionWithoutPermanentRecoveryLock(t *testing.T) {
	m := authenticatedModel(t, func(req request) (tea.Cmd, context.CancelFunc) { return func() tea.Msg { return nil }, func() {} })
	c := assistanceFixture(t, m)
	m.assistanceMode = true
	m.assistance.record = &c
	m.assistance.chosen[1] = true
	m, _ = key(m, "a")
	m, _ = confirmAction(t, m, "assist-apply", "s")
	next, _ := m.Update(result{request: request{op: "assist-apply", serial: m.serial}, err: &client.APIError{StatusCode: http.StatusConflict}})
	m = next.(model)
	if !m.blocked || m.assistance.preview != nil || m.assistance.uncertain != nil || !strings.Contains(m.status, "command rejected") {
		t.Fatal("known stale command permanently locked the human in uncertain recovery")
	}
}

func TestAssistanceScopedNotFoundClearsPrivateState(t *testing.T) {
	m := authenticatedModel(t, nil)
	c := assistanceFixture(t, m)
	m.assistanceMode = true
	m.assistance.record = &c
	m.rebuild()
	next, _ := m.Update(result{request: request{op: "assist-get", serial: m.serial, id: c.Base.ChangeID, assistanceID: c.ID}, err: &client.APIError{StatusCode: http.StatusNotFound}})
	m = next.(model)
	if m.access.ready || m.pack != nil || m.assistance.record != nil || m.lines != nil {
		t.Fatal("scoped 404 retained potentially revoked private content")
	}
}
func TestAssistanceCancelledWriteRetainsCapturedCommandAndFencesLateSuccess(t *testing.T) {
	var sent request
	m := authenticatedModel(t, func(req request) (tea.Cmd, context.CancelFunc) {
		sent = req
		return func() tea.Msg { return nil }, func() {}
	})
	c := assistanceFixture(t, m)
	m.assistanceMode = true
	m.assistance.record = &c
	m.assistance.chosen[1] = true
	m, _ = key(m, "a")
	captured := m.assistance.preview
	m, _ = confirmAction(t, m, "assist-apply", "s")
	m, _ = key(m, "esc")
	if m.assistance.uncertain != captured || !m.blocked || m.assistance.preview != nil {
		t.Fatal("cancelled write lost retained input")
	}
	next, follow := m.Update(result{request: sent, assistance: c})
	m = next.(model)
	if follow != nil || m.assistance.uncertain != captured || !m.blocked {
		t.Fatal("superseded response restored uncertain state")
	}
}
