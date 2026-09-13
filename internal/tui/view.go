package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (m model) View() string {
	if m.tooSmall() {
		return strings.Join(fit(wrap("Conductor: enlarge terminal to at least 36 columns and enough rows for revision details. q quits; Ctrl+C always quits.", m.width), m.height, m.width), "\n")
	}
	header := m.header()
	bodyHeight := m.bodyHeight()
	var body []string
	if m.access.authenticated && !m.access.ready {
		body = []string{"Review requires confirmed server access.", "r checks access; q exits. Credentials and scope stay fixed until exit."}
	} else if m.pack != nil || m.draft != nil {
		end := min(len(m.lines), m.offset+bodyHeight)
		body = m.lines[min(m.offset, end):end]
	} else if len(m.page.Changes) == 0 {
		body = []string{"No shared packages on this page.", "c creates a package; o opens an ID."}
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
			body = append(body, wrap(fmt.Sprintf("%s%s r%d | %s | %s", marker, safe(p.ID), p.Revision, state, safe(p.Repository)), m.width)...)
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
	position := fmt.Sprintf("Page %d | n next, p previous", m.pageIndex+1)
	if m.page.NextBefore == "" {
		position += " | end"
	}
	if m.pack != nil || m.draft != nil {
		position = fmt.Sprintf("Lines %d-%d/%d | arrows/PgUp/PgDn/Home/End", min(m.offset+1, len(m.lines)), min(len(m.lines), m.offset+m.bodyHeight()), len(m.lines))
	}
	status := m.status
	if m.busy != "" {
		status = "Loading " + m.busy + "... Esc cancels; a cancelled write may have committed."
	}
	statusLines := fit(wrap(safe(status), m.width), 2, m.width)
	help := "enter open | o ID | c create | r refresh | n/p page | q quit"
	if m.pack != nil {
		help = "e revise | s submit | a approve | r refresh | b browse | q quit"
	}
	if m.draft != nil {
		help = "s save preview | Esc discard | r refresh | q quit"
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
					help = "e revise | s submit | " + help
				} else {
					help = "c create | " + help
				}
			}
			if m.pack != nil && m.actionAllowed("approve") == nil {
				help = "a approve | " + help
			}
		}
	}
	last := "Evidence is shown as recorded; missing is not passing."
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
