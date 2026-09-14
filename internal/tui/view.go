package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (m model) View() string {
	if m.tooSmall() {
		return strings.Join(fit(wrap("Conductor: enlarge terminal to at least 36 columns and enough rows for revision details. Ctrl+C always quits.", m.width), m.height, m.width), "\n")
	}
	header := m.header()
	bodyHeight := m.bodyHeight()
	var body []string
	if m.access.authenticated && !m.access.ready {
		body = []string{"Review requires confirmed server access.", "r checks access; q exits. Credentials and scope stay fixed until exit."}
	} else if m.collectionMode {
		body = m.collectionBody(bodyHeight)
	} else if m.pack != nil || m.draft != nil || m.editor != nil {
		end := min(len(m.lines), m.offset+bodyHeight)
		body = m.lines[min(m.offset, end):end]
	} else if len(m.page.Changes) == 0 {
		body = []string{"No shared changes on this page.", "c starts a New Change. Describe its title and intent here.", "i imports advanced JSON; o opens an ID."}
	} else {
		// Keep the selected row visible even when a page exceeds terminal height.
		for i := m.selected; i < len(m.page.Changes) && len(body) < bodyHeight; i++ {
			p := m.page.Changes[i]
			marker := "  "
			if i == m.selected {
				marker = "> "
			}
			state := "design not approved"
			if p.Approved {
				state = "design approved"
			}
			title := p.Title
			if title == "" {
				title = p.ID
			}
			if p.TitleTruncated {
				title = "[shortened title] " + title
			}
			body = append(body, wrap(marker+safe(title), m.width)...)
			body = append(body, wrap(fmt.Sprintf("  %s r%d | %s", safe(p.ID), p.Revision, state), m.width)...)
			if p.Intent != "" {
				label := "  "
				if p.IntentTruncated {
					label += "[shortened outcome] "
				}
				body = append(body, wrap(label+safe(p.Intent), m.width)...)
			}
		}
	}
	footer := m.footer()
	lines := append(header, fit(body, bodyHeight, m.width)...)
	lines = append(lines, footer...)
	return strings.Join(lines, "\n")
}

func (m model) tooSmall() bool {
	return m.width < 36 || m.height < max(14, len(m.header())+6)
}

func (m model) footer() []string {
	if m.collectionMode && m.access.ready {
		return m.collectionFooter()
	}
	position := fmt.Sprintf("Page %d | n next, p previous", m.pageIndex+1)
	if m.page.NextBefore == "" {
		position += " | end"
	}
	if m.pack != nil || m.draft != nil || m.editor != nil {
		position = fmt.Sprintf("Lines %d-%d/%d | arrows/PgUp/PgDn/Home/End", min(m.offset+1, len(m.lines)), min(len(m.lines), m.offset+m.bodyHeight()), len(m.lines))
	}
	status := m.status
	if m.busy != "" {
		status = "Loading " + m.busy + "... Esc cancels; a cancelled write may have committed."
	}
	statusLines := fit(wrap(safe(status), m.width), 2, m.width)
	help := "c New Change | Enter open | o ID | r refresh | b browse | q quit"
	if m.pack != nil {
		help = "e Edit Design | u submit | a approve | r refresh | b browse | q quit"
	}
	if m.draft != nil {
		help = "e edit preview | s save draft | Esc discard | r refresh | q quit"
	}
	if m.access.authenticated {
		if !m.access.ready {
			help = "r recheck access | q quit"
		} else if m.draft == nil {
			help = "enter open | o ID | r refresh access | n/p page | q quit"
			if m.pack != nil {
				help = "r refresh access | b browse | q quit"
			}
			if m.actionAllowed("create") == nil {
				if m.pack != nil {
					help = "e Edit Design | u submit | " + help
				} else {
					help = "c New Change | " + help
				}
			}
			if m.pack != nil && m.actionAllowed("approve") == nil {
				help = "a approve | " + help
			}
		}
	}
	if m.access.authenticated && m.access.ready && m.prompt == "" {
		help = "g collections | " + help
	}
	last := "i import JSON | J full JSON | Missing evidence is not passing."
	if m.rawJSON {
		last = "J readable design | Full recorded JSON; missing evidence is not passing."
	}
	if m.recovery != nil {
		last = "v recover retained design | i import JSON | J full JSON | Ctrl+C quit"
	}
	if m.editor != nil {
		help = "Tab/Shift+Tab field | Enter edit | Ctrl+S preview | Esc back"
		last = "Describe the change, outcome, scope, design, planned work and verification."
		if m.editor.typing {
			last = "Enter newline | arrows/Home/End cursor | Ctrl+U clear field | Ctrl+C quit"
		}
	}
	if m.prompt != "" {
		switch m.prompt {
		case "file":
			help = "JSON file path (complete content); Enter loads, Esc cancels"
		case "open":
			help = "Package ID; Enter loads latest, Esc cancels"
		default:
			help = "Type " + m.prompt + " to confirm displayed content; Enter sends, Esc cancels"
		}
		last = "> " + tail(safe(m.input), max(1, m.width-3)) + "_"
	}
	return []string{clip(position, m.width), statusLines[0], statusLines[1], clip(help, m.width), clip(last, m.width)}
}

func (m *model) rebuild() {
	m.lines = nil
	var value any
	if m.collectionMode {
		m.rebuildCollection()
		return
	}
	if m.editor != nil {
		m.rebuildDesign()
		return
	}
	if m.draft != nil {
		value = m.draft
	} else if m.pack != nil {
		value = m.pack
	}
	if value != nil {
		data, err := json.Marshal(value)
		if err != nil {
			m.blocked = true
			m.status = "Cannot render content; actions blocked: " + err.Error()
		} else if !m.rawJSON {
			content := m.draft
			if content == nil {
				content = m.pack.Revision.Content
			}
			if m.recoveryPreview && m.pack != nil {
				m.lines = append(m.lines, "CURRENT SAVED DESIGN (inspect before replacing)")
				m.readableContent(m.pack.Revision.Content)
				m.lines = append(m.lines, "", "RECOVERED REPLACEMENT (s saves this complete content)")
			}
			m.readableContent(content)
		} else {
			for _, line := range strings.Split(prettyJSON(data), "\n") {
				m.lines = append(m.lines, wrap(safe(line), m.width)...)
			}
		}
	}
	m.offset = min(m.offset, max(0, len(m.lines)-m.bodyHeight()))
}

// Escape all non-ASCII and control characters before terminal output. This keeps
// OSC/CSI sequences and bidi formatting inert while preserving their exact values
// visibly (including in actor labels, error messages, and JSON string literals).
func safe(s string) string {
	var out strings.Builder
	for _, r := range s {
		if r >= 0x20 && r <= 0x7e {
			out.WriteRune(r)
		} else {
			quoted := strconv.QuoteToASCII(string(r))
			out.WriteString(quoted[1 : len(quoted)-1])
		}
	}
	return out.String()
}

// prettyJSON bounds indentation rather than letting deeply nested but valid JSON
// expand quadratically. It only inserts whitespace outside JSON string literals.
func prettyJSON(data []byte) string {
	var out strings.Builder
	depth, inString, escaped := 0, false, false
	newline := func() { out.WriteByte('\n'); out.WriteString(strings.Repeat("  ", min(depth, 8))) }
	for i, b := range data {
		if inString {
			out.WriteByte(b)
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}
		switch b {
		case '"':
			inString = true
			out.WriteByte(b)
		case '{', '[':
			out.WriteByte(b)
			depth++
			if i+1 < len(data) && data[i+1] != '}' && data[i+1] != ']' {
				newline()
			}
		case '}', ']':
			depth--
			if i > 0 && data[i-1] != '{' && data[i-1] != '[' {
				newline()
			}
			out.WriteByte(b)
		case ',':
			out.WriteByte(b)
			newline()
		case ':':
			out.WriteString(": ")
		default:
			out.WriteByte(b)
		}
	}
	return out.String()
}

func wrap(s string, width int) []string {
	width = max(1, width)
	var lines []string
	for len(s) > width {
		lines = append(lines, s[:width])
		s = s[width:]
	}
	return append(lines, s)
}

func clip(s string, width int) string {
	if len(s) <= width {
		return s
	}
	if width < 4 {
		return s[:width]
	}
	return s[:width-3] + "..."
}

func tail(s string, width int) string {
	if len(s) <= width {
		return s
	}
	return s[len(s)-width:]
}

func fit(lines []string, height, width int) []string {
	result := make([]string, max(0, height))
	for i := 0; i < len(result) && i < len(lines); i++ {
		result[i] = clip(lines[i], width)
	}
	if len(lines) > height && height > 0 {
		result[height-1] = clip(result[height-1]+"...", width)
	}
	return result
}
