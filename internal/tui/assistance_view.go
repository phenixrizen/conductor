package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

func (m model) assistanceBody(height int) []string {
	if m.assistance.record != nil || m.assistance.form != nil || m.assistance.preview != nil {
		end := min(len(m.lines), m.offset+height)
		return m.lines[min(m.offset, end):end]
	}
	if len(m.assistance.page.Requests) == 0 {
		return []string{"No assistance requests on this page.", "c asks for help on saved Design sections; no JSON file is needed.", "Your native assistant reads the request through Conductor MCP."}
	}
	var lines []string
	for i := m.assistance.selected; i < len(m.assistance.page.Requests) && len(lines) < height; i++ {
		r := m.assistance.page.Requests[i]
		marker := "  "
		if i == m.assistance.selected {
			marker = "> "
		}
		state := "waiting for a suggestion"
		if r.HasSuggestion {
			state = "suggestion available"
		}
		if r.AppliedRevision > 0 {
			state = fmt.Sprintf("applied as revision %d (historical fact)", r.AppliedRevision)
		}
		lines = append(lines, wrap(marker+safe(r.ID)+" | "+state, m.width)...)
		lines = append(lines, wrap("  "+safe(r.Input.Instruction), m.width)...)
	}
	return lines
}
func (m model) assistanceFooter() []string {
	position := fmt.Sprintf("Assistance page %d | n next, p previous", m.assistance.pageIndex+1)
	if m.assistance.page.NextBefore == "" {
		position += " | end"
	}
	if m.assistance.record != nil || m.assistance.form != nil || m.assistance.preview != nil {
		position = fmt.Sprintf("Lines %d-%d/%d | arrows/PgUp/PgDn/Home/End", min(m.offset+1, len(m.lines)), min(len(m.lines), m.offset+m.bodyHeight()), len(m.lines))
	}
	status := m.status
	if m.busy != "" {
		status = "Loading " + m.busy + "... Esc cancels; a cancelled write may have committed."
	}
	statusLines := fit(wrap(safe(status), m.width), 2, m.width)
	help := "Enter open | r inspect | b list | h Change | q exit"
	if m.assistanceAuthor() == nil && m.assistance.uncertain == nil && !m.blocked && m.pack != nil {
		help = "c request | " + help
	}
	if c := m.assistance.record; c != nil && c.Suggestion != nil && c.Application == nil && c.RequesterID == m.access.principal.ID && m.assistanceAuthor() == nil && m.assistance.uncertain == nil && !m.blocked && m.pack != nil && m.pack.Revision.Number == c.Input.ExpectedRevision && m.pack.Revision.Digest == c.Input.ExpectedDigest {
		help = "1-6 select | a preview | " + help
	}
	last := "Unverified suggestions; no approval or model execution is implied."
	if m.assistance.form != nil {
		help = "1-6 sections | Tab field | Enter edit | Ctrl+S preview | Esc back"
		last = "Instruction: 4096 UTF-8 bytes maximum; provider login stays in your native assistant."
	}
	if m.assistance.preview != nil {
		help = "s confirm | Esc discard | h Change | q exit"
	}
	if m.assistance.uncertain != nil && m.assistance.form == nil {
		last = "Uncertain command retained: r inspect, then v preview identical input/key."
	}
	if m.prompt != "" {
		help = "Type " + m.prompt + " to confirm displayed facts; Enter sends, Esc cancels"
		last = "> " + tail(safe(m.input), max(1, m.width-3)) + "_"
	}
	return []string{clip(position, m.width), statusLines[0], statusLines[1], clip(help, m.width), clip(last, m.width)}
}
func (m *model) assistanceLine(text string) {
	for _, line := range strings.Split(text, "\n") {
		m.lines = append(m.lines, wrap(safe(line), m.width)...)
	}
}
func (m *model) assistanceBase(base domain.Revision) {
	m.assistanceLine(fmt.Sprintf("Captured Change %s | base revision %d", base.ChangeID, base.Number))
	m.assistanceLine("Base digest: " + base.Digest)
	if m.pack == nil || m.pack.Revision.Number != base.Number || m.pack.Revision.Digest != base.Digest {
		m.assistanceLine("Historical or uninspected base. This is not a claim about the latest Design.")
	}
}
func (m *model) assistanceRecord(c domain.DesignAssistance) {
	m.assistanceLine("Request: " + c.ID)
	m.assistanceLine("Request digest: " + c.Digest)
	m.assistanceLine("Requester: " + c.RequesterID + " | created: " + c.CreatedAt.UTC().Format(time.RFC3339))
	m.assistanceBase(c.Base)
	m.assistanceLine("Requested sections: " + strings.Join(c.Input.Sections, ", "))
	m.assistanceLine("Instruction:")
	m.assistanceLine(c.Input.Instruction)
	if c.Suggestion == nil {
		m.assistanceLine("Waiting: no suggestion is recorded. An inbox request does not start a model.")
		m.assistanceLine("NATIVE ASSISTANT HANDOFF")
		m.assistanceLine("In Codex, Claude Code or Antigravity configured for this workspace/repository, ask:")
		m.assistanceLine("Read Conductor assistance request " + c.ID + " with conductor_get_design_assistance. Propose only its requested sections using conductor_propose_design_sections, retaining the request digest and a stable idempotency key. Leave the suggestion for my review.")
		m.assistanceLine("Provider login, consent and billing stay in the native assistant. Conductor has not checked whether it is connected.")
		m.assistanceLine("COMPLETE CAPTURED DESIGN")
		m.readableContent(c.Base.Content)
		return
	}
	s := c.Suggestion
	m.assistanceLine("Suggestion digest: " + s.Digest)
	m.assistanceLine("Recorded agent: " + s.AgentID + " | proposed: " + s.CreatedAt.UTC().Format(time.RFC3339))
	m.assistanceLine("Unverified proposed text and notes; model and source claims are not verified provenance.")
	if s.Note != "" {
		m.assistanceLine("Agent note:")
		m.assistanceLine(s.Note)
	}
	if c.Application != nil {
		a := c.Application
		m.assistanceLine(fmt.Sprintf("APPLIED by %s as revision %d (historical production fact)", a.AppliedBy, a.Revision))
		m.assistanceLine("Produced revision digest: " + a.RevisionDigest)
		m.assistanceLine("Applied sections: " + strings.Join(a.Sections, ", "))
		m.assistanceLine("This fact does not establish latest revision, approval or verification.")
	}
	for i, f := range designFields {
		proposed, ok := s.Sections[f.key]
		if !ok {
			continue
		}
		marker := "[ ]"
		if m.assistance.chosen[i] {
			marker = "[x]"
		}
		m.assistanceLine(fmt.Sprintf("%d %s %s", i+1, marker, f.label))
		original, present := c.Base.Content[f.key]
		if !present {
			m.assistanceLine("Before: (absent)")
		} else {
			m.assistanceLine("Before:")
			m.assistanceLine(original.(string))
		}
		m.assistanceLine("Proposed:")
		if proposed == "" {
			m.assistanceLine("(explicit empty string)")
		} else {
			m.assistanceLine(proposed)
		}
	}
	m.assistanceLine("COMPLETE ORIGINAL DESIGN (all unselected fields stay unchanged)")
	m.readableContent(c.Base.Content)
}
func (m *model) rebuildAssistance() {
	if f := m.assistance.form; f != nil {
		m.assistanceLine("FORM: Request Design assistance (not saved)")
		m.assistanceBase(f.base)
		for i, field := range designFields {
			marker := "[ ]"
			if f.chosen[i] {
				marker = "[x]"
			}
			current := "  "
			if i == f.selected {
				current = "> "
			}
			suffix := ""
			if !f.editable[i] {
				suffix = " (structured legacy section; read-only)"
			}
			m.assistanceLine(fmt.Sprintf("%s%d %s %s%s", current, i+1, marker, field.label, suffix))
		}
		marker := "  "
		if f.selected == 6 {
			marker = "> "
		}
		m.assistanceLine(marker + "Instruction (Enter types; Tab leaves)")
		m.assistanceLine(f.instruction)
		if f.typing {
			m.assistanceLine("_ typing instruction")
		}
	} else if p := m.assistance.preview; p != nil {
		m.assistanceLine("PREVIEW: " + p.command.op + " (not confirmed)")
		m.assistanceBase(p.base)
		m.assistanceLine("Idempotency key: " + p.command.assistanceKey)
		if p.command.op == "assist-request" {
			m.assistanceLine("Selected sections: " + strings.Join(p.command.assistanceInput.Sections, ", "))
			m.assistanceLine("Instruction:")
			m.assistanceLine(p.command.assistanceInput.Instruction)
			m.assistanceLine("COMPLETE CAPTURED DESIGN")
			m.readableContent(p.base.Content)
		} else {
			m.assistanceLine("Apply sections: " + strings.Join(p.command.assistanceApply.Sections, ", "))
			m.assistanceRecord(*p.record)
			m.assistanceLine("COMPLETE MERGED DESIGN (only selected sections change)")
			m.readableContent(p.merged)
		}
	} else if c := m.assistance.record; c != nil {
		m.assistanceRecord(*c)
	}
	m.offset = min(m.offset, max(0, len(m.lines)-m.bodyHeight()))
}
