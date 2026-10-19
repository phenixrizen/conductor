package session

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// listItem matches a row of a dialog's numbered list: what comes before
// its number (the indent, and on the row the dialog would pick on Enter
// the highlight a TUI draws there: Claude Code's ❯, Codex's ›), the
// number, one digit, and the label.
var listItem = regexp.MustCompile(`^([ \t]*(?:[❯›][ \t]*)?)([1-9])\.[ \t]+(\S.*)$`)

// Bounds of the list readList takes for a dialog's, in rows.
const (
	// listTail: rows after its last choice, blank ones aside (a hint, the
	// footer).
	listTail = 6
	// listGap: rows between two of its choices (a choice's description, a
	// rule, the rest of a long label).
	listGap = 4
	// listHead: rows of its words, at most.
	listHead = 16
)

// readList reads the numbered list a dialog draws at the bottom of text, a
// screen's rows joined by line breaks, as the questions asked during the
// work are matched against (Screen.Text): from the last row numbered N,
// at most listTail rows from the end, up through the rows numbered N-1 to 1
// whose numbers stand at its column, at most listGap rows apart. Each
// choice is its row's label and its number, the key that picks it, with no
// Enter (a digit picks at once in Claude Code's dialogs), marked Mode when
// mode matches its label with the rows that follow it up to the next
// choice (the last one's up to a blank row): its description, or the rest
// of a label a narrow screen wrapped ("Yes, and switch to", then "BYPASS
// PERMISSIONS …"), which the label shown leaves out. The words are the
// rows above the list up to the dialog's top rule (a row of ─ alone),
// listHead at most, each trimmed, blank rows and rows of rule characters
// left out, joined by line breaks. No list (fewer than two choices, no row
// numbered 1, a number out of its turn at that column, rows too far
// apart): no choices, and the dialog is not taken for one.
func readList(text string, mode *regexp.Regexp) (choices []Option, words string) {
	rows := strings.Split(text, "\n")
	last, tail := len(rows)-1, 0
	for ; last >= 0 && !listItem.MatchString(rows[last]); last-- {
		if strings.TrimSpace(rows[last]) != "" {
			if tail++; tail > listTail {
				return nil, ""
			}
		}
	}
	if last < 0 {
		return nil, ""
	}
	// The rows that follow each choice, for mode: the last one's up to a
	// blank row, the others' up to the next choice.
	var after []string
	for i := last + 1; i < len(rows) && strings.TrimSpace(rows[i]) != ""; i++ {
		after = append(after, rows[i])
	}
	m := listItem.FindStringSubmatch(rows[last])
	col := utf8.RuneCountInString(m[1])
	choices = []Option{listChoice(m, after, mode)}
	want, top := m[2][0]-1, last
	after = nil
	for i := last - 1; i >= 0 && want > '0'; i-- {
		if m := listItem.FindStringSubmatch(rows[i]); m != nil && utf8.RuneCountInString(m[1]) == col {
			if m[2][0] != want {
				return nil, ""
			}
			slices.Reverse(after)
			choices = append(choices, listChoice(m, after, mode))
			want, top, after = want-1, i, nil
			continue
		}
		if after = append(after, rows[i]); len(after) > listGap {
			return nil, ""
		}
	}
	if want != '0' || len(choices) < 2 {
		return nil, ""
	}
	slices.Reverse(choices)
	var above []string
	for i := top - 1; i >= 0 && len(above) < listHead; i-- {
		row := strings.TrimSpace(rows[i])
		rule, solid := ruleRow(row)
		if solid {
			break
		}
		if row != "" && !rule {
			above = append(above, row)
		}
	}
	slices.Reverse(above)
	return choices, strings.Join(above, "\n")
}

// listChoice is the choice a list's row shows (listItem's match m), marked
// Mode when mode matches its label with the rows after it.
func listChoice(m []string, after []string, mode *regexp.Regexp) Option {
	label := strings.TrimSpace(m[3])
	all := label
	for _, row := range after {
		all += " " + strings.TrimSpace(row)
	}
	return Option{Label: label, Input: m[2], Mode: mode != nil && mode.MatchString(all)}
}

// ruleRow reports whether row (trimmed) is a rule, three or more of the
// characters a TUI draws lines with and nothing else, and whether it is a
// solid one, of ─ alone: the top of a Claude Code dialog, where ╌ marks
// off a command or a file within it.
func ruleRow(row string) (rule, solid bool) {
	if utf8.RuneCountInString(row) < 3 {
		return false, false
	}
	solid = true
	for _, r := range row {
		switch r {
		case '─':
		case '━', '═', '╌', '╍', '┄', '┅', '┈', '┉':
			solid = false
		default:
			return false, false
		}
	}
	return true, solid
}
