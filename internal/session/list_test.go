package session

import (
	"regexp"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/catalog"
)

// Claude Code 2.1.304's dialogs as it drew them in a PTY 120 columns wide
// on 2026-10-19 (#288), the screen's rows as the questions asked during
// the work are matched against (Screen.Text); the folder and the plan's
// path are made up, the banner left out.
var (
	solidRule  = strings.Repeat("─", 120)
	dashedRule = strings.Repeat("╌", 120)
	planRule   = "  " + strings.Repeat("─", 116)
)

func screenRows(rows ...string) string { return strings.Join(rows, "\n") }

// claudeBash is the permission dialog for a Bash command.
var claudeBash = screenRows(
	"",
	"❯ Run exactly this bash command and nothing else: touch probe.txt",
	"",
	"● Creating empty file probe.txt",
	"  ⎿  $ touch probe.txt",
	"",
	solidRule,
	" Bash command",
	` Tip: auto mode handles these prompts for you — choose "switch to auto mode" below`,
	" Create empty file probe.txt",
	dashedRule,
	" touch probe.txt",
	dashedRule,
	" Do you want to proceed?",
	" ❯ 1. Yes",
	"   2. Yes, and always allow access to /home/dev/project from this project",
	"   3. Yes, and switch to auto mode · auto mode handles these prompts for you",
	"   4. No",
	"",
	" Esc to cancel · Tab to amend",
)

// claudeWrite is the permission dialog for a file the Write tool creates.
var claudeWrite = screenRows(
	"❯ Use the Write tool to create a file named notes.txt containing the word hi. Do nothing else.",
	"",
	"● Write(notes.txt)",
	"",
	solidRule,
	" Create file",
	" notes.txt",
	dashedRule,
	"  1 hi",
	dashedRule,
	" Do you want to create notes.txt?",
	" ❯ 1. Yes",
	"   2. Yes, and switch to accept edits (auto-approve file edits and common file commands) for this session (shift+tab)",
	"   3. No",
	"",
	" Esc to cancel · Tab to amend",
)

// claudePlan is the plan's approval, its first choice as it is drawn when
// the session may bypass permissions or not.
func claudePlan(first string, highlight int) string {
	choices := []string{first, "Yes, manually approve edits", "Tell Claude what to change"}
	rows := []string{
		planRule,
		"   Ready to code?",
		"",
		"   Here is Claude's plan:",
		"  " + strings.Repeat("╌", 116),
		"   Create /home/dev/project/hello.txt containing the text hi.",
		"  " + strings.Repeat("╌", 116),
		"", "", "", "",
		planRule,
		"   Claude has written up a plan and is ready to execute. Would you like to proceed?",
		"",
	}
	for i, c := range choices {
		mark := "    "
		if i+1 == highlight {
			mark = "  ❯ "
		}
		rows = append(rows, mark+string(rune('1'+i))+". "+c)
	}
	return screenRows(append(rows,
		"       shift+tab to approve with this feedback",
		"",
		"   ctrl+g to edit in Vim · ~/.claude/plans/plan-creating-a-file.md",
	)...)
}

const (
	planAuto   = "Yes, and use auto mode"
	planBypass = "Yes, and switch to BYPASS PERMISSIONS (no further prompts) for this session"
)

// claudeAsk is AskUserQuestion's dialog: a description below each choice,
// the choice that takes typed words, a rule, and the one that chats.
var claudeAsk = screenRows(
	"❯ Use the AskUserQuestion tool to ask me one question: do I prefer red or blue?",
	"",
	solidRule,
	" ☐ Color",
	"",
	"Do you prefer red or blue?",
	"",
	"❯ 1. Red",
	"     Prefer red",
	"  2. Blue",
	"     Prefer blue",
	"  3. Type something.",
	solidRule,
	"  4. Chat about this",
	"",
	"Enter to select · ↑/↓ to navigate · Esc to cancel",
)

// claudeAskReview is the last page of an AskUserQuestion of two
// questions: the answers, and its own list at the bottom with no footer.
var claudeAskReview = screenRows(
	"❯ Ask me two questions with AskUserQuestion: red or blue, then small or large.",
	"",
	solidRule,
	"←  ☒ Color  ☒ Size  ✔ Submit  →",
	"",
	"Review your answers",
	"",
	" ● Do you prefer red or blue?",
	"   → Red",
	" ● Do you prefer small or large?",
	"   → Small",
	"",
	"Ready to submit your answers?",
	"",
	"❯ 1. Submit answers",
	"  2. Cancel",
)

// claudeNarrow is the plan's approval on a screen narrow enough to wrap
// its first choice, the words that make it a change of mode on its second
// row.
var claudeNarrow = screenRows(
	"  "+strings.Repeat("─", 36),
	"   Claude has written up a plan and is",
	"   ready to execute. Would you like to",
	"   proceed?",
	"",
	"   ❯ 1. Yes, and switch to",
	"        BYPASS PERMISSIONS (no further",
	"        prompts) for this session",
	"     2. Yes, manually approve edits",
	"     3. Tell Claude what to change",
	"        shift+tab to approve with this",
	"        feedback",
	"",
	"   ctrl+g to edit in Vim · ~/.claude/plans/plan.md",
)

// claudeIdle is Claude Code at rest after a numbered answer, its prompt
// box and status below; claudeTyped has a numbered line typed in the box.
var (
	claudeIdle = screenRows(
		"● Here is what I would do:",
		"  1. Add the test",
		"  2. Fix the bug",
		"",
		solidRule,
		"❯ ",
		solidRule,
		"  ⏵⏵ auto mode on (shift+tab to cycle)",
	)
	claudeTyped = screenRows(
		"● Done.",
		"",
		solidRule,
		"❯ 1. add the test",
		"  2. fix the bug",
		solidRule,
		"  ? for shortcuts",
	)
	// claudeQuoted has a whole dialog in the transcript, quoted, and
	// claudePasted one pasted into the prompt box: Claude Code's prompt box
	// and status line stand below each.
	claudeQuoted = screenRows(
		"● The dialog you saw:",
		"   Do you want to proceed?",
		"   ❯ 1. Yes",
		"     2. No",
		"   Esc to cancel · Tab to amend",
		"",
		solidRule,
		"❯ ",
		solidRule,
		"  ? for shortcuts",
	)
	claudePasted = screenRows(
		"● Done.",
		"",
		solidRule,
		"❯ why did this show up?",
		"  Do you want to proceed?",
		"  ❯ 1. Yes",
		"    2. No",
		"  Esc to cancel · Tab to amend",
		solidRule,
		"  ? for shortcuts",
	)
	claudeWorking = screenRows(
		"❯ 1. add the test",
		"  2. fix the bug",
		"",
		"✻ Thinking… (esc to interrupt)",
		"",
		solidRule,
		"❯ ",
		solidRule,
		"  ? for shortcuts",
	)
)

func labels(choices []Option) []string {
	out := make([]string, len(choices))
	for i, c := range choices {
		out[i] = c.Input + " " + c.Label
	}
	return out
}

func TestReadListReadsClaudeCodesDialogs(t *testing.T) {
	for _, c := range []struct {
		name, screen string
		choices      []string
		words        string
	}{
		{"bash", claudeBash,
			[]string{"1 Yes", "2 Yes, and always allow access to /home/dev/project from this project", "3 Yes, and switch to auto mode · auto mode handles these prompts for you", "4 No"},
			"Bash command\nTip: auto mode handles these prompts for you — choose \"switch to auto mode\" below\nCreate empty file probe.txt\ntouch probe.txt\nDo you want to proceed?"},
		{"write", claudeWrite,
			[]string{"1 Yes", "2 Yes, and switch to accept edits (auto-approve file edits and common file commands) for this session (shift+tab)", "3 No"},
			"Create file\nnotes.txt\n1 hi\nDo you want to create notes.txt?"},
		{"plan", claudePlan(planAuto, 1),
			[]string{"1 " + planAuto, "2 Yes, manually approve edits", "3 Tell Claude what to change"},
			"Claude has written up a plan and is ready to execute. Would you like to proceed?"},
		{"plan, bypass allowed", claudePlan(planBypass, 1),
			[]string{"1 " + planBypass, "2 Yes, manually approve edits", "3 Tell Claude what to change"},
			"Claude has written up a plan and is ready to execute. Would you like to proceed?"},
		{"plan, its feedback field highlighted", claudePlan(planAuto, 3),
			[]string{"1 " + planAuto, "2 Yes, manually approve edits", "3 Tell Claude what to change"},
			"Claude has written up a plan and is ready to execute. Would you like to proceed?"},
		{"ask", claudeAsk,
			[]string{"1 Red", "2 Blue", "3 Type something.", "4 Chat about this"},
			"☐ Color\nDo you prefer red or blue?"},
		{"ask, its last page", claudeAskReview,
			[]string{"1 Submit answers", "2 Cancel"},
			"←  ☒ Color  ☒ Size  ✔ Submit  →\nReview your answers\n● Do you prefer red or blue?\n→ Red\n● Do you prefer small or large?\n→ Small\nReady to submit your answers?"},
		{"plan, narrow", claudeNarrow,
			[]string{"1 Yes, and switch to", "2 Yes, manually approve edits", "3 Tell Claude what to change"},
			"Claude has written up a plan and is\nready to execute. Would you like to\nproceed?"},
	} {
		choices, words := readList(c.screen, nil)
		if got := labels(choices); strings.Join(got, "|") != strings.Join(c.choices, "|") {
			t.Errorf("%s: choices %q", c.name, got)
		}
		if words != c.words {
			t.Errorf("%s: words %q", c.name, words)
		}
	}
}

// A list is the dialog's when its rows are numbered 1 to N in turn at one
// column, close together, at the bottom of the screen: a numbered list
// above the dialog is not read into it, a label wrapped at a narrow width
// is its first row, and anything else is no list.
func TestReadListTakesOnlyTheDialogsOwnList(t *testing.T) {
	above := screenRows(
		"● I will:",
		"  1. Add the test",
		"  2. Fix the bug",
		"  3. Run the checks",
		"",
		claudeBash,
	)
	if choices, words := readList(above, nil); len(choices) != 4 || choices[0].Label != "Yes" || !strings.HasPrefix(words, "Bash command\n") {
		t.Fatalf("a list above the dialog: %q %q", labels(choices), words)
	}
	narrow := screenRows(
		strings.Repeat("─", 60),
		" Do you want to proceed?",
		" ❯ 1. Yes",
		"   2. Yes, and always allow access to /home/dev/project from",
		"      this project",
		"   3. No",
		"",
		" Esc to cancel · Tab to amend",
	)
	if choices, _ := readList(narrow, nil); strings.Join(labels(choices), "|") != "1 Yes|2 Yes, and always allow access to /home/dev/project from|3 No" {
		t.Fatalf("a wrapped label: %q", labels(choices))
	}
	for name, screen := range map[string]string{
		"no row 1":           screenRows(" ❯ 2. Yes", "   3. No", "", " Esc to cancel"),
		"out of turn":        screenRows(" ❯ 1. Yes", "   3. No", "", " Esc to cancel"),
		"another column":     screenRows(" ❯ 1. Yes", "     2. No", "", " Esc to cancel"),
		"one choice":         screenRows(" ❯ 1. Yes", "", " Esc to cancel"),
		"too far apart":      screenRows(" ❯ 1. Yes", "a", "b", "c", "d", "e", "   2. No", "", " Esc to cancel"),
		"too far up":         screenRows(" ❯ 1. Yes", "   2. No", "a", "b", "c", "d", "e", "f", "g"),
		"no list":            screenRows(solidRule, "❯ ", solidRule, "  ? for shortcuts"),
		"nothing":            "",
		"a ten for a choice": screenRows(" ❯ 1. Yes", "  10. No", "", " Esc to cancel"),
	} {
		if choices, words := readList(screen, nil); choices != nil || words != "" {
			t.Errorf("%s: %q %q", name, labels(choices), words)
		}
	}
}

// claudeQuestion is Claude Code's built-in question asked during the work,
// as a session gets it from the catalog.
func claudeQuestion(t *testing.T) StartupQuestion {
	t.Helper()
	claude, ok := catalog.Default().Get("claude")
	if !ok || len(claude.Questions) != 1 {
		t.Fatalf("Claude Code's questions: %+v", claude.Questions)
	}
	q := claude.Questions[0]
	if !q.Anytime || !q.AnswersFromScreen || len(q.Answers) != 0 || q.ModeAnswers == "" {
		t.Fatalf("Claude Code's question %+v", q)
	}
	return StartupQuestion{Pattern: regexp.MustCompile(q.Prompt), Anytime: true, FromScreen: true, Mode: regexp.MustCompile(q.ModeAnswers)}
}

// Claude Code's question finds each of its dialogs, with the dialog's own
// choices, the ones that change the permission mode marked (when a narrow
// screen wraps the words that say so too), and never a numbered list in
// the transcript, a numbered prompt typed at rest or while it works, or a
// whole dialog quoted in the transcript or pasted into the prompt box.
func TestClaudeCodesQuestionIsItsDialogAlone(t *testing.T) {
	q := claudeQuestion(t)
	for _, c := range []struct {
		name, screen string
		mode         []bool
	}{
		{"bash", claudeBash, []bool{false, false, true, false}},
		{"write", claudeWrite, []bool{false, true, false}},
		{"plan", claudePlan(planAuto, 1), []bool{true, false, false}},
		{"plan, bypass allowed", claudePlan(planBypass, 1), []bool{true, false, false}},
		{"plan, its feedback field highlighted", claudePlan(planAuto, 3), []bool{true, false, false}},
		{"ask", claudeAsk, []bool{false, false, false, false}},
		{"ask, its last page", claudeAskReview, []bool{false, false}},
		{"plan, narrow: the change of mode on the label's second row", claudeNarrow, []bool{true, false, false}},
	} {
		words, answers := q.read(c.screen)
		if words == "" || len(answers) != len(c.mode) {
			t.Errorf("%s: %q %+v", c.name, words, answers)
			continue
		}
		for i, a := range answers {
			if a.Mode != c.mode[i] {
				t.Errorf("%s: choice %d %+v, mode %v wanted", c.name, i+1, a, c.mode[i])
			}
		}
	}
	for name, screen := range map[string]string{"idle": claudeIdle, "typed": claudeTyped, "working": claudeWorking, "quoted": claudeQuoted, "pasted": claudePasted} {
		if words, answers := q.read(screen); words != "" || answers != nil {
			t.Errorf("%s: %q %+v", name, words, answers)
		}
	}
}
