package session

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// newClaudeSession is a session of Claude Code's, 120 by 40 as its dialogs
// were captured, with its questions asked during the work (the catalog's)
// and a controller attached.
func newClaudeSession(t *testing.T) (*Local, *fakeProc, *Subscription) {
	t.Helper()
	return newClaudeSessionAt(t, 120)
}

// newClaudeSessionAt is newClaudeSession cols wide.
func newClaudeSessionAt(t *testing.T, cols uint16) (*Local, *fakeProc, *Subscription) {
	t.Helper()
	p := newFakeProc()
	s := NewLocal(Info{ID: "sess", Cwd: realDir(t, t.TempDir()), Cols: cols, Rows: 40}, p, quiet(Options{Questions: claudeQuestions(t)}))
	t.Cleanup(func() { p.exit() })
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: cols, Rows: 40}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	return s, p, sub
}

// working draws Claude Code at work for d as it does: a frame every 100 ms,
// each a synchronized update, its spinner turning, so that the screen is
// never quiet (Claude Code 2.1.304 drew 19 frames in the 1.7 s after an
// answer in a PTY, none 400 ms after the last).
func working(p *fakeProc, d time.Duration) {
	spin := []string{"✻", "✽", "✶", "✳"}
	for i := 0; time.Duration(i)*100*time.Millisecond < d; i++ {
		p.outW.Write([]byte("\x1b[?2026h\x1b[H\x1b[2J\x1b[1;1H" + spin[i%len(spin)] + " Working… (esc to interrupt)\x1b[?2026l"))
		time.Sleep(100 * time.Millisecond)
	}
}

// drawn is what a TUI writes to show text, a screen's rows: the screen
// erased, then each row placed with a cursor move.
func drawn(text string) []byte {
	var b strings.Builder
	b.WriteString("\x1b[H\x1b[2J")
	for i, row := range strings.Split(text, "\n") {
		fmt.Fprintf(&b, "\x1b[%d;1H%s", i+1, row)
	}
	return []byte(b.String())
}

// claudeBashFor is claudeBash for another command.
func claudeBashFor(cmd string) string { return strings.ReplaceAll(claudeBash, "probe.txt", cmd) }

// claudeAtRest is Claude Code at rest after a dialog went: what it said,
// its prompt box and its status line.
func claudeAtRest(said string) string {
	return screenRows("  ⎿  "+said, "", solidRule, "❯ ", solidRule, "  ⏸ manual mode on · ? for shortcuts")
}

// The hook's report comes first, with no choices: a page offers none, for
// the dialog's are not read yet. The dialog drawn takes its place with its
// own words and choices, the one that switches to auto mode marked, and
// holds typed text; its choice's key answers it, and it goes with the
// dialog.
func TestClaudeCodesDialogIsAskedWithItsOwnChoices(t *testing.T) {
	s, p, sub := newClaudeSession(t)
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, nil); err != nil {
		t.Fatal(err)
	}
	if a := s.Info().Attention; a.Source != SourceAPI || a.Kind != KindPermission || a.Options != nil {
		t.Fatalf("the hook's report: %+v", a)
	}
	p.outW.Write(drawn(claudeBash))
	att := waitAttention(t, s, asked)
	want := []Option{{Label: "Yes", Input: "1"}, {Label: "Yes, and always allow access to /home/dev/project from this project", Input: "2"}, {Label: "Yes, and switch to auto mode · auto mode handles these prompts for you", Input: "3", Mode: true}, {Label: "No", Input: "4"}}
	if !sameOptions(att.Options, CleanOptions(want)) || att.Kind != KindPermission {
		t.Fatalf("the dialog's choices %+v", att.Options)
	}
	if att.Message != `Bash command Tip: auto mode handles these prompts for you — choose "switch to auto mode" below Create empty file probe.txt touch probe.txt Do you want to proceed?` {
		t.Fatalf("its words %q", att.Message)
	}
	// A report after it does not hide it (Claude Code's permission_prompt
	// notification comes later).
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "Claude needs your permission to use Bash", SourceAPI, KindPermission, nil); err != nil {
		t.Fatal(err)
	}
	if a := s.Info().Attention; a.ID != att.ID || !asked(a) {
		t.Fatalf("a later report hid the dialog: %+v", a)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "yes please", By: sub}); !errors.Is(err, ErrQuestion) {
		t.Fatalf("typed text at the dialog: %v", err)
	}
	if err := s.AnswerInput(t.Context(), sub, att.ID, []byte("4")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "4" {
		t.Fatalf("written %q", got)
	}
	p.outW.Write(drawn(claudeAtRest("Interrupted · What should Claude do instead?")))
	waitAttention(t, s, func(a Attention) bool { return a.State != AttentionNeedsInput })
}

// Two dialogs one after the other are two questions, Claude Code at work
// between them with no quiet screen and no hook (Conductor installs no
// PreToolUse): the dialog answered is forgotten at the first whole frame
// that no longer shows it, so the next, the same command again included,
// is answered as the first was.
func TestClaudeCodesDialogsOneAfterTheOtherAreEachAsked(t *testing.T) {
	s, p, sub := newClaudeSession(t)
	last := ""
	for i, cmd := range []string{"probe.txt", "probe.txt", "notes.txt"} {
		p.outW.Write(drawn(claudeBashFor(cmd)))
		att := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.ID != last && strings.Contains(a.Message, "touch "+cmd) })
		last = att.ID
		if err := s.AnswerInput(t.Context(), sub, att.ID, []byte("1")); err != nil {
			t.Fatalf("dialog %d: %v", i+1, err)
		}
		if got := nextWrite(t, p); got != "1" {
			t.Fatalf("dialog %d: written %q", i+1, got)
		}
		working(p, 1200*time.Millisecond)
	}
}

// A dialog 44 columns wide, its footer wrapped onto the next row, is asked
// with its choices and holds typed text, as a phone's terminal shows it.
func TestAClaudeDialogOnANarrowScreenIsAsked(t *testing.T) {
	s, p, sub := newClaudeSessionAt(t, 44)
	p.outW.Write(drawn(claudePlanNarrow))
	att := waitAttention(t, s, asked)
	if len(att.Options) != 3 || !att.Options[0].Mode || att.Message != planQuestion {
		t.Fatalf("the plan at 44 columns %+v", att)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "go ahead", By: sub}); !errors.Is(err, ErrQuestion) {
		t.Fatalf("typed text at the narrow dialog: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
}

// A reply is weighed against the dialog's choices whole: one whose second
// choice changed past what its label shows (a folder's long path, cut at
// 60 characters) is another dialog, and the reply writes nothing. That
// dialog is a question of its own, which takes its reply.
func TestAReplyToAClaudeDialogChangedPastItsLabelsIsRefused(t *testing.T) {
	s, p, sub := newClaudeSession(t)
	long := "/home/dev/a-project-with-a-name-long-enough-to-pass-the-cut"
	first := strings.ReplaceAll(claudeBash, "/home/dev/project", long+"/one")
	p.outW.Write(drawn(first))
	att := waitAttention(t, s, asked)
	if !strings.HasSuffix(att.Options[1].Label, "…") {
		t.Fatalf("the label is not cut: %+v", att.Options[1])
	}
	p.outW.Write(drawn(strings.ReplaceAll(claudeBash, "/home/dev/project", long+"/two")))
	time.Sleep(100 * time.Millisecond) // read, not yet quiet
	if err := s.AnswerInput(t.Context(), sub, att.ID, []byte("2")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply to the dialog changed past its labels: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
	next := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.ID != att.ID })
	if err := s.AnswerInput(t.Context(), sub, att.ID, []byte("2")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply to the first dialog after the second was asked: %v", err)
	}
	if err := s.AnswerInput(t.Context(), sub, next.ID, []byte("2")); err != nil {
		t.Fatalf("a reply to the second: %v", err)
	}
	if got := nextWrite(t, p); got != "2" {
		t.Fatalf("written %q", got)
	}
}

// A dialog answered by a key typed in its terminal leaves its question
// answered while Claude Code works, its spinner never letting the screen
// go quiet: the first whole frame without the dialog withdraws it, so the
// same dialog asked again is a fresh question that takes a reply.
func TestAClaudeDialogAnsweredInTheTerminalGivesWayToTheSameAgain(t *testing.T) {
	s, p, sub := newClaudeSession(t)
	p.outW.Write(drawn(claudeBash))
	first := waitAttention(t, s, asked)
	if err := s.Input(sub, []byte("1")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if a := s.Info().Attention; a.ID != first.ID || a.Answered != AnsweredTerminal {
		t.Fatalf("the key typed in the terminal: %+v", a)
	}
	working(p, 1200*time.Millisecond)
	if a := s.Info().Attention; a.State == AttentionNeedsInput {
		t.Fatalf("the answered dialog still shown as Claude Code works: %+v", a)
	}
	p.outW.Write(drawn(claudeBash))
	again := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.ID != first.ID })
	if again.Answered != "" {
		t.Fatalf("the same dialog asked again is answered already: %+v", again)
	}
	if err := s.AnswerInput(t.Context(), sub, again.ID, []byte("1")); err != nil {
		t.Fatalf("a reply to the same dialog asked again: %v", err)
	}
	if got := nextWrite(t, p); got != "1" {
		t.Fatalf("written %q", got)
	}
}

// AskUserQuestion's "Type something." is a field: arrows typed in the
// terminal take the highlight there, and every key after that is typed
// into it. The dialog then offers no choices, and a reply to the question
// asked before (its Red is 1, which would be typed into the field) writes
// nothing.
func TestTheClaudeQuestionsFieldOffersNoChoices(t *testing.T) {
	s, p, sub := newClaudeSession(t)
	p.outW.Write(drawn(claudeAsk))
	att := waitAttention(t, s, asked)
	if len(att.Options) != 4 {
		t.Fatalf("the question %+v", att)
	}
	for range 2 {
		if err := s.Input(sub, []byte("\x1b[B")); err != nil {
			t.Fatal(err)
		}
		nextWrite(t, p)
	}
	// The highlight in the field, nothing typed yet: its choices read as
	// before, the highlight no part of them.
	p.outW.Write(drawn(claudeAskField))
	time.Sleep(100 * time.Millisecond) // read, not yet quiet
	if err := s.AnswerInput(t.Context(), sub, att.ID, []byte("1")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply as the field has the highlight: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
	field := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Options == nil })
	if field.Message != "☐ Color Do you prefer red or blue?" {
		t.Fatalf("the question with its field in use %+v", field)
	}
	p.outW.Write(drawn(claudeAskFieldTyped))
	if a := waitAttention(t, s, func(a Attention) bool { return asked(a) }); a.Options != nil {
		t.Fatalf("words typed into the field: %+v", a)
	}
}

// One read may bring a whole frame without the dialog answered and the
// same dialog drawn again: each frame is looked at, so the question
// answered in the terminal goes with the first and the second is a fresh
// question that takes a reply.
func TestAClaudeDialogGoneAndBackInOneReadIsAskedAgain(t *testing.T) {
	s, p, sub := newClaudeSession(t)
	p.outW.Write([]byte("\x1b[?2026h" + string(drawn(claudeBash)) + "\x1b[?2026l"))
	first := waitAttention(t, s, asked)
	if err := s.Input(sub, []byte("1")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	p.outW.Write([]byte("\x1b[?2026h\x1b[H\x1b[2J\x1b[1;1H✻ Working… (esc to interrupt)\x1b[?2026l" + "\x1b[?2026h" + string(drawn(claudeBash)) + "\x1b[?2026l"))
	again := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.ID != first.ID })
	if err := s.AnswerInput(t.Context(), sub, again.ID, []byte("1")); err != nil {
		t.Fatalf("a reply to the dialog drawn again: %v", err)
	}
	if got := nextWrite(t, p); got != "1" {
		t.Fatalf("written %q", got)
	}
}

// The plan's "Tell Claude what to change" takes the highlight into its
// field, where every key is typed: the dialog then offers no choices (a
// digit clicked would be typed into the feedback) and still holds typed
// text, until the feedback is entered in the terminal.
func TestTheClaudePlansFeedbackFieldOffersNoChoices(t *testing.T) {
	s, p, sub := newClaudeSession(t)
	p.outW.Write(drawn(claudePlan(planAuto, 1)))
	att := waitAttention(t, s, asked)
	if len(att.Options) != 3 {
		t.Fatalf("the plan %+v", att)
	}
	if err := s.Input(sub, []byte("3")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	p.outW.Write(drawn(claudePlanTyped))
	typed := waitAttention(t, s, func(a Attention) bool {
		return a.State == AttentionNeedsInput && a.Source == SourceQuestion && a.Options == nil
	})
	if typed.Message != planQuestion || typed.Kind != KindPermission {
		t.Fatalf("the plan with its field in use %+v", typed)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "use a map instead", By: sub}); !errors.Is(err, ErrQuestion) {
		t.Fatalf("typed text at the feedback field: %v", err)
	}
}

// A reply to a dialog whose choices changed under it (another plan's
// approval drawn, the words the same) is refused, nothing written: the
// question a page showed is told by its choices too.
func TestAReplyToAClaudeDialogWhoseChoicesChangedIsRefused(t *testing.T) {
	s, p, sub := newClaudeSession(t)
	p.outW.Write(drawn(claudePlan(planAuto, 1)))
	att := waitAttention(t, s, asked)
	if len(att.Options) != 3 || !att.Options[0].Mode || att.Options[1].Mode || att.Message != "Claude has written up a plan and is ready to execute. Would you like to proceed?" {
		t.Fatalf("the plan's approval %+v", att)
	}
	p.outW.Write(drawn(claudePlan(planBypass, 1)))
	time.Sleep(100 * time.Millisecond) // read, not yet quiet
	if err := s.AnswerInput(t.Context(), sub, att.ID, []byte("2")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply to the plan whose choices changed: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
	again := waitAttention(t, s, func(a Attention) bool {
		return asked(a) && len(a.Options) == 3 && a.Options[0].Label == "Yes, and switch to BYPASS PERMISSIONS (no further prompts)…"
	})
	if err := s.AnswerInput(t.Context(), sub, again.ID, []byte("2")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "2" {
		t.Fatalf("written %q", got)
	}
}

// AskUserQuestion of two questions asks each page and then its last one,
// "Ready to submit your answers?" with no footer, which holds Conductor's
// own text as the pages do: its Enter would submit the answers.
func TestClaudeCodesQuestionPagesAndItsLastPageAreAsked(t *testing.T) {
	s, p, sub := newClaudeSession(t)
	second := strings.NewReplacer("☐ Color", "☒ Color", "red or blue", "small or large", "Red", "Small", "Blue", "Large").Replace(claudeAsk)
	for i, page := range []string{claudeAsk, second} {
		p.outW.Write(drawn(page))
		att := waitAttention(t, s, func(a Attention) bool { return asked(a) && len(a.Options) == 4 })
		if i == 1 && !strings.Contains(att.Message, "small or large") {
			t.Fatalf("the second page: %+v", att)
		}
		if err := s.AnswerInput(t.Context(), sub, att.ID, []byte("1")); err != nil {
			t.Fatalf("page %d: %v", i+1, err)
		}
		nextWrite(t, p)
	}
	p.outW.Write(drawn(claudeAskReview))
	last := waitAttention(t, s, func(a Attention) bool { return asked(a) && len(a.Options) == 2 })
	if last.Options[0] != (Option{Label: "Submit answers", Input: "1"}) || !strings.HasSuffix(last.Message, "Ready to submit your answers?") {
		t.Fatalf("the last page %+v", last)
	}
	if res, err := s.Submit(t.Context(), Submission{Text: "handoff", ByName: "crew", UnlessWaiting: true}); err != nil || res.Typed {
		t.Fatalf("Conductor's own text at the last page: %+v %v", res, err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// A whole dialog quoted in the transcript, Claude Code's prompt box below
// it, asks nothing: the hook's report stays as it is, without choices.
func TestAClaudeDialogQuotedInTheTranscriptAsksNothing(t *testing.T) {
	s, p, _ := newClaudeSession(t)
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, nil); err != nil {
		t.Fatal(err)
	}
	p.outW.Write(drawn(claudeQuoted))
	time.Sleep(900 * time.Millisecond)
	if a := s.Info().Attention; a.Source != SourceAPI || a.Options != nil {
		t.Fatalf("a quoted dialog was asked: %+v", a)
	}
}
