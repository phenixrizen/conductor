package tui

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
	"github.com/phenixrizen/conductor/pkg/client"
)

type assistanceState struct {
	page                       domain.AssistancePage
	cursors                    []string
	pageIndex, selected        int
	record                     *domain.DesignAssistance
	form                       *assistanceForm
	preview, active, uncertain *assistancePreview
	chosen                     [6]bool
}
type assistanceForm struct {
	base             domain.Revision
	chosen, editable [6]bool
	instruction      string
	selected         int
	typing           bool
}
type assistancePreview struct {
	command request
	base    domain.Revision
	merged  domain.Content
	record  *domain.DesignAssistance
}

func isAssistanceWrite(op string) bool { return op == "assist-request" || op == "assist-apply" }
func isAssistanceOperation(op string) bool {
	return isAssistanceWrite(op) || op == "assist-list" || op == "assist-get"
}

// Immutable assistance records do not disappear normally. A scoped 404 can hide
// a revoked repository grant, so discard cached private state conservatively.
func assistanceNotFound(err error) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 404
}
func (m model) assistanceTyping() bool { return m.assistance.form != nil && m.assistance.form.typing }
func (m model) assistanceAuthor() error {
	if !m.access.authenticated || !m.access.ready {
		return errors.New("assistance requires authenticated workspace and repository access")
	}
	if m.access.principal.Kind != "human" {
		return errors.New("only a human author may request and apply assistance; agents propose through MCP")
	}
	r := m.access.repository
	return domain.ValidateRepositoryAction(m.access.principal, r.CanRead, r.CanAuthor, r.CanApprove, "author")
}
func (m model) assistanceActionAllowed(op string) error {
	if err := m.assistanceAuthor(); err != nil {
		return err
	}
	p := m.assistance.preview
	if p == nil || p.command.op != op {
		return errors.New("preview the exact assistance command first")
	}
	if m.blocked {
		return errors.New("press r to inspect shared state before recovering or writing")
	}
	if m.pack == nil || m.pack.ID != p.base.ChangeID {
		return errors.New("inspect the original Change first")
	}
	// Retained commands are replayed byte-for-byte, even when their committed result
	// has advanced the Change. The server authorizes before recovering its receipt.
	if m.assistance.uncertain == p {
		return nil
	}
	if m.pack.Revision.Number != p.base.Number || m.pack.Revision.Digest != p.base.Digest {
		return errors.New("the Change differs from the captured base; request fresh assistance")
	}
	if op == "assist-apply" && (p.record == nil || p.record.RequesterID != m.access.principal.ID || p.record.Application != nil) {
		return errors.New("only the requesting human can apply an unapplied suggestion")
	}
	return nil
}
func (m model) openAssistance() (tea.Model, tea.Cmd) {
	if !m.access.authenticated || !m.access.ready {
		m.status = "Assistance unavailable: authenticated workspace and repository access is required."
		return m, nil
	}
	if m.pack == nil || m.draft != nil || m.blocked {
		m.status = "Save and inspect a Change before requesting assistance."
		return m, nil
	}
	m.assistanceMode = true
	return m.listAssistance(0)
}
func (m model) listAssistance(index int) (tea.Model, tea.Cmd) {
	if m.pack == nil {
		m.status = "Inspect the Change with r first."
		return m, nil
	}
	if index == 0 {
		m.assistance.cursors = []string{""}
	}
	m.assistance.pageIndex = index
	m.assistance.page, m.assistance.record, m.assistance.form, m.assistance.preview = domain.AssistancePage{}, nil, nil, nil
	m.offset = 0
	m.rebuild()
	return m.start(request{op: "assist-list", id: m.pack.ID, cursor: m.assistance.cursors[index]})
}
func (m model) recheckAssistance() (tea.Model, tea.Cmd) {
	id, aid := m.inspectID, ""
	if m.assistance.record != nil {
		aid = m.assistance.record.ID
	}
	retained, designRetained := m.assistance.uncertain, m.recovery
	m.invalidateAccess()
	m.assistance.uncertain, m.recovery = retained, designRetained
	if m.access.restartRequired {
		m.assistance = assistanceState{}
		m.recovery = nil
		m.status = "Access unavailable: " + errPrincipalChanged.Error()
		return m, nil
	}
	return m.start(request{op: "access", id: id, assistanceID: aid, assistanceView: true})
}
func (m model) beginAssistance() (tea.Model, tea.Cmd) {
	if err := m.assistanceAuthor(); err != nil {
		m.status = "Action blocked: " + err.Error()
		return m, nil
	}
	if m.pack == nil || m.blocked {
		m.status = "Inspect the saved Change with r first."
		return m, nil
	}
	if m.assistance.uncertain != nil {
		m.status = "A prior command is uncertain. r inspects shared state; v recovers its exact input/key."
		return m, nil
	}
	f := &assistanceForm{base: m.pack.Revision}
	for i, field := range designFields {
		value, present := f.base.Content[field.key]
		_, text := value.(string)
		f.editable[i] = !present || text
	}
	m.assistance.form, m.assistance.preview = f, nil
	m.offset = 0
	m.status = "Choose sections with 1-6, Tab to Instruction, Enter to type; Ctrl+S previews."
	m.rebuild()
	return m, nil
}
func assistanceKeyID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func chosenAssistanceFields(chosen [6]bool) []string {
	var fields []string
	for i, f := range designFields {
		if chosen[i] {
			fields = append(fields, f.key)
		}
	}
	return fields
}
func (m model) previewAssistanceRequest() (tea.Model, tea.Cmd) {
	f := m.assistance.form
	if strings.TrimSpace(f.instruction) == "" || len(f.instruction) > 4096 || !utf8.ValidString(f.instruction) {
		m.status = "Add an instruction of 1-4096 UTF-8 bytes."
		return m, nil
	}
	fields := chosenAssistanceFields(f.chosen)
	if len(fields) == 0 {
		m.status = "Select at least one text section."
		return m, nil
	}
	key, err := assistanceKeyID()
	if err != nil {
		m.status = "Cannot create a request key."
		return m, nil
	}
	input := domain.AssistanceInput{ChangeID: f.base.ChangeID, ExpectedRevision: f.base.Number, ExpectedDigest: f.base.Digest, Instruction: f.instruction, Sections: fields}
	m.assistance.preview = &assistancePreview{base: f.base, command: request{op: "assist-request", id: f.base.ChangeID, assistanceKey: key, assistanceInput: input}}
	m.assistance.form = nil
	m.offset = 0
	m.status = "Request preview ready. s confirms saving the request; no model is started."
	m.rebuild()
	return m, nil
}
func (m model) previewAssistanceApply() (tea.Model, tea.Cmd) {
	if err := m.assistanceAuthor(); err != nil {
		m.status = "Action blocked: " + err.Error()
		return m, nil
	}
	c := m.assistance.record
	if c == nil || c.Suggestion == nil || c.Application != nil || c.RequesterID != m.access.principal.ID {
		m.status = "Only the requesting human may apply an unapplied suggestion."
		return m, nil
	}
	if m.assistance.uncertain != nil {
		m.status = "Recover the retained command with r then v first."
		return m, nil
	}
	if m.pack == nil || m.blocked || m.pack.Revision.Number != c.Input.ExpectedRevision || m.pack.Revision.Digest != c.Input.ExpectedDigest {
		m.status = "Historical or stale base: inspect with r, then create a fresh request."
		return m, nil
	}
	fields := chosenAssistanceFields(m.assistance.chosen)
	if len(fields) == 0 {
		m.status = "Select at least one suggested section with 1-6."
		return m, nil
	}
	merged := make(domain.Content, len(c.Base.Content)+len(fields))
	for k, v := range c.Base.Content {
		merged[k] = v
	}
	for _, k := range fields {
		merged[k] = c.Suggestion.Sections[k]
	}
	digest, err := domain.Digest(merged)
	if err != nil || digest == c.Base.Digest {
		m.status = "The selected sections make no Design changes."
		return m, nil
	}
	if err := boundedDesign(merged, c.Base.Number); err != nil {
		m.status = "Cannot preview: " + err.Error()
		return m, nil
	}
	key, err := assistanceKeyID()
	if err != nil {
		m.status = "Cannot create an application key."
		return m, nil
	}
	input := domain.ApplySuggestionInput{RequestDigest: c.Digest, SuggestionDigest: c.Suggestion.Digest, ExpectedRevision: c.Input.ExpectedRevision, ExpectedDigest: c.Input.ExpectedDigest, Sections: fields}
	m.assistance.preview = &assistancePreview{base: c.Base, merged: merged, record: c, command: request{op: "assist-apply", id: c.Base.ChangeID, assistanceID: c.ID, assistanceKey: key, assistanceApply: input}}
	m.offset = 0
	m.status = "Application preview ready. Inspect the complete merged Design; s confirms selected sections."
	m.rebuild()
	return m, nil
}
func (m model) confirmAssistance() (tea.Model, tea.Cmd) {
	p := m.assistance.preview
	if p == nil {
		m.status = "Preview a request or selected suggestion sections first."
		return m, nil
	}
	if err := m.assistanceActionAllowed(p.command.op); err != nil {
		m.status = "Action blocked: " + err.Error()
		return m, nil
	}
	m.pending = p.command
	m.pending.accessGeneration = m.access.generation
	m.prompt, m.input = p.command.op, ""
	return m, nil
}
func (m model) assistanceKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.assistance.form != nil {
		return m.assistanceFormKey(key)
	}
	switch key.String() {
	case "h":
		m.assistanceMode = false
		m.assistance.preview = nil
		m.assistance.form = nil
		m.offset = 0
		m.rebuild()
		m.status = "Returned to Change. Uncertain assistance input remains available with h then v."
	case "c":
		return m.beginAssistance()
	case "s":
		return m.confirmAssistance()
	case "a":
		if m.assistance.preview == nil {
			return m.previewAssistanceApply()
		}
	case "r":
		return m.recheckAssistance()
	case "v":
		if m.assistance.uncertain == nil {
			m.status = "No uncertain assistance command to recover."
			break
		}
		if m.blocked || m.pack == nil || m.pack.ID != m.assistance.uncertain.base.ChangeID {
			m.status = "Press r to inspect the original Change before recovery."
			break
		}
		m.assistance.preview = m.assistance.uncertain
		m.offset = 0
		m.status = "Retained command preview: s retries the identical input and key; no write was sent."
		m.rebuild()
	case "b":
		return m.listAssistance(0)
	case "n":
		if m.assistance.record == nil && m.assistance.preview == nil && m.assistance.page.NextBefore != "" {
			if len(m.assistance.cursors) >= 1000 {
				m.status = "Page limit reached; r restarts discovery."
				break
			}
			m.assistance.cursors = append(m.assistance.cursors[:m.assistance.pageIndex+1], m.assistance.page.NextBefore)
			return m.listAssistance(m.assistance.pageIndex + 1)
		}
	case "p":
		if m.assistance.record == nil && m.assistance.preview == nil && m.assistance.pageIndex > 0 {
			return m.listAssistance(m.assistance.pageIndex - 1)
		}
	case "enter":
		if m.assistance.record == nil && m.assistance.preview == nil && len(m.assistance.page.Requests) > 0 {
			return m.start(request{op: "assist-get", id: m.pack.ID, assistanceID: m.assistance.page.Requests[m.assistance.selected].ID})
		}
	case "esc":
		m.assistance.preview = nil
		m.offset = 0
		m.status = "Preview discarded; no new write sent. Any uncertain input/key remains retained."
		m.rebuild()
	case "down", "j":
		m.move(1)
	case "up", "k":
		m.move(-1)
	case "pgdown", " ":
		m.move(m.bodyHeight())
	case "pgup":
		m.move(-m.bodyHeight())
	case "home":
		m.move(-len(m.lines) - len(m.assistance.page.Requests))
	case "end":
		m.move(len(m.lines) + len(m.assistance.page.Requests))
	default:
		if m.assistance.preview == nil && m.assistance.record != nil && m.assistance.record.Suggestion != nil && len(key.String()) == 1 && key.String()[0] >= '1' && key.String()[0] <= '6' {
			i := int(key.String()[0] - '1')
			if _, ok := m.assistance.record.Suggestion.Sections[designFields[i].key]; ok {
				m.assistance.chosen[i] = !m.assistance.chosen[i]
				m.rebuild()
			}
		}
	}
	return m, nil
}
func (m model) assistanceFormKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := *m.assistance.form
	m.assistance.form = &f
	if key.Type == tea.KeyRunes && f.typing {
		var addition strings.Builder
		for _, r := range key.Runes {
			if r == '\n' || r == '\t' || (!unicode.IsControl(r) && !unicode.Is(unicode.Cf, r)) {
				addition.WriteRune(r)
			}
		}
		if len(f.instruction)+addition.Len() <= 4096 {
			f.instruction += addition.String()
		} else {
			m.status = "Instruction is limited to 4096 UTF-8 bytes."
		}
		m.rebuild()
		return m, nil
	}
	if key.Type == tea.KeyRunes {
		text := string(key.Runes)
		if len(text) == 1 && text[0] >= '1' && text[0] <= '6' {
			i := int(text[0] - '1')
			if f.editable[i] {
				f.chosen[i] = !f.chosen[i]
			}
		} else if text == "j" {
			m.move(1)
		} else if text == "k" {
			m.move(-1)
		}
		m.rebuild()
		return m, nil
	}
	switch key.String() {
	case "ctrl+s":
		return m.previewAssistanceRequest()
	case "esc":
		if f.typing {
			f.typing = false
		} else {
			m.assistance.form = nil
			m.status = "Request form discarded; no write sent."
		}
	case "tab", "shift+tab":
		delta := 1
		if key.String() == "shift+tab" {
			delta = -1
		}
		f.selected = (f.selected + delta + 7) % 7
		f.typing = false
		m.offset = 0
	case "enter":
		if f.selected == 6 {
			if f.typing {
				if len(f.instruction) < 4096 {
					f.instruction += "\n"
				}
			} else {
				f.typing = true
			}
		} else if f.editable[f.selected] {
			f.chosen[f.selected] = !f.chosen[f.selected]
		}
	case "ctrl+u":
		if f.typing {
			f.instruction = ""
		}
	case "backspace", "ctrl+h":
		if f.typing {
			r := []rune(f.instruction)
			if len(r) > 0 {
				f.instruction = string(r[:len(r)-1])
			}
		}
	case " ":
		if f.typing && len(f.instruction) < 4096 {
			f.instruction += " "
		}
	case "down", "j":
		if !f.typing {
			m.move(1)
		}
	case "up", "k":
		if !f.typing {
			m.move(-1)
		}
	case "pgdown":
		m.move(m.bodyHeight())
	case "pgup":
		m.move(-m.bodyHeight())
	case "home":
		m.move(-len(m.lines))
	case "end":
		m.move(len(m.lines))
	default:
		if !f.typing && len(key.String()) == 1 && key.String()[0] >= '1' && key.String()[0] <= '6' {
			i := int(key.String()[0] - '1')
			if f.editable[i] {
				f.chosen[i] = !f.chosen[i]
			}
		}
	}
	m.rebuild()
	return m, nil
}
func (m *model) assistanceCancelled(op string) {
	if isAssistanceWrite(op) {
		m.assistance.uncertain = m.assistance.active
		m.assistance.active = nil
		m.assistance.preview = nil
		m.blocked = true
		m.status = "Assistance write outcome unknown. r inspects shared state; v recovers the identical input/key."
	} else {
		m.assistance.record = nil
		m.assistance.page = domain.AssistancePage{}
		m.status = "Assistance read cancelled. r rechecks access and inspects."
	}
	m.rebuild()
}
func (m model) assistanceResult(res result) (tea.Model, tea.Cmd) {
	m.assistanceMode = true
	if res.err != nil {
		var apiErr *client.APIError
		if isAssistanceWrite(res.op) && errors.As(res.err, &apiErr) && (apiErr.StatusCode == 400 || apiErr.StatusCode == 409 || apiErr.StatusCode == 422) {
			m.assistance.active, m.assistance.preview, m.assistance.uncertain = nil, nil, nil
			m.blocked = true
			m.status = "Assistance command rejected. r inspects current facts before creating a fresh request. " + res.err.Error()
			m.rebuild()
			return m, nil
		}
		m.assistanceCancelled(res.op)
		if isAssistanceWrite(res.op) {
			m.status = "Assistance write not confirmed. r inspects; v recovers the same input/key. " + res.err.Error()
		} else {
			m.status = "Assistance unavailable. r rechecks access. " + res.err.Error()
		}
		return m, nil
	}
	switch res.op {
	case "assist-list":
		m.assistance.page = res.assistancePage
		m.assistance.record = nil
		m.assistance.selected = 0
		m.status = "Shared assistance requests loaded. c creates a request; Enter inspects."
	case "assist-get", "assist-request", "assist-apply":
		m.assistance.record = &res.assistance
		m.assistance.form = nil
		m.assistance.preview = nil
		m.assistance.chosen = [6]bool{}
		if res.assistance.Suggestion != nil {
			for i, f := range designFields {
				_, m.assistance.chosen[i] = res.assistance.Suggestion.Sections[f.key]
			}
		}
		m.status = "Assistance inspected. r explicitly checks for suggestions; no model is started here."
		if isAssistanceWrite(res.op) {
			m.assistance.active, m.assistance.uncertain = nil, nil
		}
		if res.op == "assist-request" {
			m.status = "Assistance request saved. Give the handoff to your native assistant; r checks for suggestions."
		}
		if res.op == "assist-apply" {
			m.pack = nil
			m.blocked = true
			m.status = fmt.Sprintf("Applied as revision %d. Design is unapproved; r inspects the latest Change before further actions.", res.assistance.Application.Revision)
		}
	}
	m.offset = 0
	m.rebuild()
	return m, nil
}
func (m model) validateAssistanceScope(res result) error {
	if res.op == "assist-list" {
		if len(res.assistancePage.Requests) > pageSize || !validCollectionCursor(res.assistancePage.NextBefore) {
			return errResponseScope
		}
		seen := map[string]bool{}
		for _, r := range res.assistancePage.Requests {
			if !domain.IsLowerHex(r.ID, 32) || !domain.IsLowerHex(r.Digest, 64) || domain.ValidateAccessID(r.RequesterID) != nil || r.Input.ChangeID != res.id || r.Input.ExpectedRevision < 1 || !domain.IsLowerHex(r.Input.ExpectedDigest, 64) || seen[r.ID] {
				return errResponseScope
			}
			seen[r.ID] = true
		}
		return nil
	}
	c := res.assistance
	if domain.ValidateAssistanceBase(c.Input, c.Base) != nil || c.CreatedAt.IsZero() {
		return errResponseScope
	}
	requestDigest, err := domain.DesignAssistanceDigest(c)
	if err != nil || requestDigest != c.Digest {
		return errResponseScope
	}
	if !domain.IsLowerHex(c.ID, 32) || !domain.IsLowerHex(c.Digest, 64) || c.WorkspaceID != m.access.workspaceID || c.RepositoryID != m.access.repositoryID || domain.ValidateAccessID(c.RequesterID) != nil || c.Input.ChangeID != res.id || c.Base.ChangeID != res.id || c.Base.Number != c.Input.ExpectedRevision || c.Base.Digest != c.Input.ExpectedDigest {
		return errResponseScope
	}
	if res.op != "assist-request" && c.ID != res.assistanceID {
		return errResponseScope
	}
	digest, err := domain.Digest(c.Base.Content)
	if err != nil || digest != c.Base.Digest {
		return errResponseScope
	}
	if res.op == "assist-request" && (!reflect.DeepEqual(c.Input, res.assistanceInput) || c.RequesterID != m.access.principal.ID) {
		return errResponseScope
	}
	if c.Suggestion != nil {
		proposal := domain.SuggestionInput{RequestDigest: c.Digest, Sections: c.Suggestion.Sections, Note: c.Suggestion.Note}
		suggestionDigest, err := domain.AssistanceSuggestionDigest(c.Digest, *c.Suggestion)
		if domain.ValidateAssistanceSuggestion(c, proposal) != nil || err != nil || suggestionDigest != c.Suggestion.Digest || c.Suggestion.CreatedAt.IsZero() {
			return errResponseScope
		}
		if !domain.IsLowerHex(c.Suggestion.Digest, 64) || domain.ValidateAccessID(c.Suggestion.AgentID) != nil {
			return errResponseScope
		}
		for k, v := range c.Suggestion.Sections {
			found := false
			for _, field := range c.Input.Sections {
				found = found || k == field
			}
			if !found || len(v) > 32768 || !utf8.ValidString(v) {
				return errResponseScope
			}
		}
	}
	if c.Application != nil && (c.Suggestion == nil || c.Application.Revision != c.Base.Number+1 || !domain.IsLowerHex(c.Application.RevisionDigest, 64) || c.Application.AppliedBy != c.RequesterID) {
		return errResponseScope
	}
	if c.Application != nil {
		a := c.Application
		digest, err := domain.AssistanceApplicationDigest(c.Digest, c.Suggestion.Digest, *a)
		_, mergedDigest, mergeErr := domain.MergeAssistanceSections(c, c.Base, domain.ApplySuggestionInput{RequestDigest: c.Digest, SuggestionDigest: c.Suggestion.Digest, ExpectedRevision: c.Base.Number, ExpectedDigest: c.Base.Digest, Sections: a.Sections})
		if err != nil || digest != a.Digest || mergeErr != nil || mergedDigest != a.RevisionDigest || a.CreatedAt.IsZero() {
			return errResponseScope
		}
	}
	if res.op == "assist-apply" {
		a := c.Application
		i := res.assistanceApply
		if a == nil || c.RequesterID != m.access.principal.ID || c.Digest != i.RequestDigest || c.Suggestion.Digest != i.SuggestionDigest || c.Base.Number != i.ExpectedRevision || c.Base.Digest != i.ExpectedDigest || !reflect.DeepEqual(a.Sections, i.Sections) {
			return errResponseScope
		}
	}
	return nil
}
