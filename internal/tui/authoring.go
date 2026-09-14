package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/phenixrizen/conductor/internal/domain"
)

var designFields = []struct{ key, label, hint string }{
	{"title", "Change title", "Name the change in a short sentence."},
	{"intent", "Intended outcome", "What should improve, and for whom?"},
	{"scope", "Scope", "What is included? What is outside this change?"},
	{"design", "Design", "Describe the approach and unresolved decisions."},
	{"tasks", "Planned work", "Break the work into understandable steps."},
	{"verification", "Verification plan", "Describe the checks needed. Planned checks are not passing evidence."},
}

const maxDesignBytes = 1 << 20

type designEditor struct {
	base             domain.Content
	values           [6]string
	editable         [6]bool
	selected, cursor int
	typing           bool
	revision         int64
	requireBasics    bool
}

type designRecovery struct {
	content        domain.Content
	op, id, digest string
	revision       int64
	guided         bool
}

func (m model) recoverDesign() (tea.Model, tea.Cmd) {
	if m.recovery == nil {
		m.status = "No retained design input to recover."
		return m, nil
	}
	if m.blocked {
		m.status = "Inspect saved work with r before recovering the retained input."
		return m, nil
	}
	if m.draft != nil {
		m.status = "Save or discard the current preview before recovering input."
		return m, nil
	}
	r := m.recovery
	if r.op == "revise" && (m.pack == nil || m.pack.ID != r.id) {
		m.status = "Open change " + r.id + " before comparing its retained design."
		return m, nil
	}
	if r.op == "revise" {
		retained, _ := domain.Digest(r.content)
		current, _ := domain.Digest(m.pack.Revision.Content)
		if retained == current {
			m.status = "Retained design matches the inspected saved revision. No replacement is needed."
			m.recovery = nil
			return m, nil
		}
	}
	if r.op == "create" && m.pack != nil {
		m.status = "Return to shared browsing with b before recovering a new change."
		return m, nil
	}
	if err := m.actionAllowed(r.op); err != nil {
		m.status = "Action blocked: " + err.Error()
		return m, nil
	}
	m.draft, m.draftOp, m.draftGuided = r.content, r.op, r.guided
	m.recoveryPreview, m.rawJSON, m.offset = true, false, 0
	m.status = "Compare the saved design and retained replacement. s confirms a new save; e edits the replacement."
	if r.op == "create" {
		m.status = "Previous create outcome is unknown. Inspect shared work: saving again may create a duplicate change."
	}
	m.rebuild()
	return m, nil
}

func (m model) beginDesign(op string) (tea.Model, tea.Cmd) {
	if err := m.actionAllowed(op); err != nil {
		m.status = "Action blocked: " + err.Error()
		return m, nil
	}
	if (op == "create" && m.draft != nil) || (op == "revise" && m.pack == nil && m.draft == nil) {
		m.status = "Save or discard the preview; inspect a change before editing its design."
		return m, nil
	}
	base := domain.Content{}
	requireBasics := op == "create" && m.draft == nil
	if m.draft != nil {
		base, op = m.draft, m.draftOp
		requireBasics = m.draftGuided
	} else if op == "revise" {
		base = m.pack.Revision.Content
	} else {
		m.pack = nil
		m.inspectID = ""
	}
	editor := &designEditor{base: base, requireBasics: requireBasics}
	if op == "revise" {
		editor.revision = m.pack.Revision.Number
	}
	for i, field := range designFields {
		value, exists := base[field.key]
		editor.values[i], editor.editable[i] = value.(string)
		editor.editable[i] = !exists || editor.editable[i]
	}
	m.editor, m.draftOp, m.offset, m.rawJSON = editor, op, 0, false
	m.status = "Fill in the design. Tab selects a field; Enter edits it; Ctrl+S previews before saving."
	m.rebuild()
	return m, nil
}

// Only changed string fields are written. Absent empty values, unknown fields,
// and structured values survive the form exactly as supplied by the API.
func (e designEditor) content() domain.Content {
	content := make(domain.Content, len(e.base)+len(designFields))
	for key, value := range e.base {
		content[key] = value
	}
	for i, field := range designFields {
		original, _ := e.base[field.key].(string)
		if e.editable[i] && e.values[i] != original {
			content[field.key] = e.values[i]
		}
	}
	return content
}

func boundedDesign(content domain.Content, revision int64) error {
	command := map[string]any{"content": content}
	if revision > 0 {
		command["expectedRevision"] = revision
	}
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	// The shared Go client uses json.Encoder, which appends a newline.
	if len(data)+1 > maxDesignBytes {
		return errors.New("design command, including its revision, must contain at most 1 MiB")
	}
	return nil
}

func (m model) previewDesign() (tea.Model, tea.Cmd) {
	content := m.editor.content()
	if m.editor.requireBasics && (strings.TrimSpace(m.editor.values[0]) == "" || strings.TrimSpace(m.editor.values[1]) == "") {
		m.status = "Add a Change title and Intended outcome before previewing a new change."
		return m, nil
	}
	if err := boundedDesign(content, m.editor.revision); err != nil {
		m.status = "Cannot preview: " + err.Error()
		return m, nil
	}
	if err := domain.ValidateContent(content); err != nil {
		m.status = "Cannot preview: " + err.Error()
		return m, nil
	}
	if m.draftOp == "revise" {
		before, _ := domain.Digest(m.pack.Revision.Content)
		after, _ := domain.Digest(content)
		if before == after {
			m.status = "No design changes to save. Edit a field or press Esc to return."
			return m, nil
		}
	}
	m.draftGuided = m.editor.requireBasics
	m.draft, m.editor, m.offset = content, nil, 0
	m.status = "Design preview ready. s saves this draft; submission is a separate action after saving."
	m.rebuild()
	return m, nil
}

func (m model) designKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Copy form state before editing so prior Bubble Tea model values stay inert.
	e := *m.editor
	m.editor = &e
	// Names such as "enter" and "ctrl+s" can arrive as ordinary coalesced text.
	// Only actual key events may navigate, preview, or leave an active field.
	if key.Type == tea.KeyRunes {
		if e.typing {
			m.insertDesign(string(key.Runes))
		} else if string(key.Runes) == "j" {
			m.offset++
		} else if string(key.Runes) == "k" {
			m.offset--
		}
		m.rebuild()
		return m, nil
	}
	switch key.String() {
	case "ctrl+s":
		return m.previewDesign()
	case "esc":
		if e.typing {
			e.typing = false
			m.status = "Field kept in the form. Ctrl+S previews; Esc discards the form."
		} else {
			m.editor = nil
			m.status = "Design form discarded; no write sent."
		}
	case "tab", "shift+tab":
		delta := 1
		if key.String() == "shift+tab" {
			delta = -1
		}
		e.selected = (e.selected + delta + len(designFields)) % len(designFields)
		e.typing, e.cursor = false, 0
		m.offset = 0
	case "enter":
		if !e.editable[e.selected] {
			m.status = "This field has structured content. It is preserved; use advanced JSON import to replace it."
		} else if !e.typing {
			e.typing, e.cursor = true, len([]rune(e.values[e.selected]))
		} else {
			m.insertDesign("\n")
		}
	default:
		if !e.typing {
			switch key.String() {
			case "down", "j":
				m.offset++
			case "up", "k":
				m.offset--
			case "pgdown", " ":
				m.offset += m.bodyHeight()
			case "pgup":
				m.offset -= m.bodyHeight()
			case "home":
				m.offset = 0
			case "end":
				m.offset = len(m.lines)
			}
			break
		}
		runes := []rune(e.values[e.selected])
		switch key.String() {
		case " ":
			m.insertDesign(" ")
		case "left":
			e.cursor = max(0, e.cursor-1)
		case "right":
			e.cursor = min(len(runes), e.cursor+1)
		case "up", "down":
			start := e.cursor
			for start > 0 && runes[start-1] != '\n' {
				start--
			}
			column := e.cursor - start
			if key.String() == "up" && start > 0 {
				end := start - 1
				start = end
				for start > 0 && runes[start-1] != '\n' {
					start--
				}
				e.cursor = min(start+column, end)
			} else if key.String() == "down" {
				end := e.cursor
				for end < len(runes) && runes[end] != '\n' {
					end++
				}
				if end < len(runes) {
					start = end + 1
					end = start
					for end < len(runes) && runes[end] != '\n' {
						end++
					}
					e.cursor = min(start+column, end)
				}
			}
		case "home", "ctrl+a":
			for e.cursor > 0 && runes[e.cursor-1] != '\n' {
				e.cursor--
			}
		case "end", "ctrl+e":
			for e.cursor < len(runes) && runes[e.cursor] != '\n' {
				e.cursor++
			}
		case "backspace", "ctrl+h":
			if e.cursor > 0 {
				e.values[e.selected] = string(runes[:e.cursor-1]) + string(runes[e.cursor:])
				e.cursor--
			}
		case "delete":
			if e.cursor < len(runes) {
				e.values[e.selected] = string(runes[:e.cursor]) + string(runes[e.cursor+1:])
			}
		case "ctrl+u":
			e.values[e.selected], e.cursor = "", 0
		default:
			if key.Type == tea.KeyRunes {
				m.insertDesign(string(key.Runes))
			}
		}
	}
	m.rebuild()
	return m, nil
}

func (m *model) insertDesign(text string) {
	e := m.editor
	var cleaned strings.Builder
	for _, r := range text {
		if r == '\n' || (!unicode.IsControl(r) && !unicode.Is(unicode.Cf, r)) {
			cleaned.WriteRune(r)
		}
	}
	text = cleaned.String()
	if len(text)+len(e.values[e.selected]) > maxDesignBytes {
		m.status = "Input not added: design content is limited to 1 MiB."
		return
	}
	runes := []rune(e.values[e.selected])
	before := e.values[e.selected]
	e.values[e.selected] = string(runes[:e.cursor]) + text + string(runes[e.cursor:])
	if err := boundedDesign(e.content(), e.revision); err != nil {
		e.values[e.selected] = before
		m.status = "Input not added: " + err.Error()
		return
	}
	e.cursor += len([]rune(text))
}

func (m *model) rebuildDesign() {
	e := m.editor
	field := designFields[e.selected]
	label := fmt.Sprintf("Field %d/%d: %s", e.selected+1, len(designFields), field.label)
	if e.selected < 2 && e.requireBasics {
		label += " (required)"
	}
	m.lines = append(m.lines, wrap(label, m.width)...)
	m.lines = append(m.lines, wrap(field.hint, m.width)...)
	var text string
	if !e.editable[e.selected] {
		m.lines = append(m.lines, wrap("Structured value: preserved, read only in this form.", m.width)...)
		data, _ := json.Marshal(e.base[field.key])
		text = prettyJSON(data)
	} else {
		text = e.values[e.selected]
		if e.typing {
			runes := []rune(text)
			prefix := string(runes[:e.cursor])
			cursorLine := len(m.lines)
			for _, line := range strings.Split(prefix, "\n") {
				cursorLine += len(wrap(safe(line), m.width))
			}
			cursorLine--
			m.offset = max(0, min(m.offset, cursorLine))
			if cursorLine >= m.offset+m.bodyHeight() {
				m.offset = cursorLine - m.bodyHeight() + 1
			}
			text = prefix + "_" + string(runes[e.cursor:])
		} else if text == "" {
			text = "(empty; Enter to edit)"
		}
	}
	for _, line := range strings.Split(text, "\n") {
		m.lines = append(m.lines, wrap(safe(line), m.width)...)
	}
	m.offset = max(0, min(m.offset, max(0, len(m.lines)-m.bodyHeight())))
}

func (m *model) readableContent(content domain.Content) {
	remaining := make(domain.Content, len(content))
	for key, value := range content {
		remaining[key] = value
	}
	for _, field := range designFields {
		value, exists := content[field.key]
		m.lines = append(m.lines, field.label)
		text, isText := value.(string)
		if !exists {
			text = "Not supplied."
		} else if !isText {
			data, _ := json.Marshal(value)
			text = "Structured content (preserved):\n" + prettyJSON(data)
		}
		for _, line := range strings.Split(text, "\n") {
			m.lines = append(m.lines, wrap(safe(line), m.width)...)
		}
		m.lines = append(m.lines, "")
		delete(remaining, field.key)
	}
	if len(remaining) > 0 {
		m.lines = append(m.lines, "Additional content (preserved)")
		data, _ := json.Marshal(remaining)
		for _, line := range strings.Split(prettyJSON(data), "\n") {
			m.lines = append(m.lines, wrap(safe(line), m.width)...)
		}
	}
}
