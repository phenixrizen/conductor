package session

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/proto"
)

// codexUpdateScreen is Codex 0.161.0's update question as it drew it in a
// PTY on 2026-10-11 (an isolated CODEX_HOME whose version.json named
// 0.162.1): the heading, the highlighted Update now, and Skip until next
// version drawn word by word with cursor moves between.
const codexUpdateScreen = "\x1b[?2026h\x1b[?25l\x1b[2;3H\x1b[1mUpdate available\x1b[22m\x1b[2m\x1b[2m \xc2\xb7 0.161.0 \xe2\x86\x92 0.162.1\x1b[3;3HRelease notes: \x1b[4m\x1b]8;;https://github.com/openai/codex/releases/latest\ahttps://github.com/openai/codex/releases/latest\x1b]8;;\a\x1b[5;1H\x1b[24m\x1b[22m\x1b[7m\x1b[1m\xe2\x80\xba 1. Update now (runs `npm install -g @openai/codex`)                                               \x1b[6;3H\x1b[27m\x1b[22m2.\x1b[6;6HSkip\x1b[7;3H3.\x1b[7;6HSkip\x1b[7;11Huntil\x1b[7;17Hnext\x1b[7;22Hversion\x1b[9;3H\x1b[1menter\x1b[22m\x1b[2m\x1b[2m continue \xc2\xb7 \x1b[22m\x1b[1mesc\x1b[22m\x1b[2m\x1b[2m skip\x1b[39m\x1b[49m\x1b[0m\x1b[?2026l"

// A question's words can be other words of its screen than the ones that
// find it (the catalog's words): Codex's update question, found by its
// highlighted "Update now (runs", shows its heading, so that "answered:"
// never reads as if Update had been picked; its "Skip until next version"
// is the digit 3 alone, which answers it without an Enter.
func TestAQuestionsWordsAreItsHeading(t *testing.T) {
	upd := []Option{{Label: "Skip this update", Input: "\x1b"}, {Label: "Skip until next version", Input: "3"}}
	s, p := newLocalWith(t, quiet(Options{Questions: []StartupQuestion{{
		Pattern: regexp.MustCompile(`(?i)update\s*now\s*\(\s*runs`),
		Words:   regexp.MustCompile(`(?i)(update\s*available)\W*?(\d+(?:\.\d+)+\s*(?:→|->)\s*\d+(?:\.\d+)+)`),
		Answers: upd,
	}}}))
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte(codexUpdateScreen))
	att := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if att.State != AttentionNeedsInput || att.Message != "Update available 0.161.0 → 0.162.1" || len(att.Options) != 2 || att.Options[1] != upd[1] {
		t.Fatalf("attention %+v", att)
	}
	if err := s.Input(sub, []byte("3")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "3" {
		t.Fatalf("written %q", got)
	}
	info := s.Info()
	if info.Attention.State != AttentionNone || info.LastAnswer == nil || info.LastAnswer.Message != "Update available 0.161.0 → 0.162.1" {
		t.Fatalf("the choice did not answer the question by its words: %+v %+v", info.Attention, info.LastAnswer)
	}
	// Without its words a question is told by what found it; a words pattern that matches nothing changes nothing.
	q := StartupQuestion{Pattern: regexp.MustCompile(`(?i)update\s*now\s*\(\s*runs`), Words: regexp.MustCompile(`never here`)}
	if got := q.words("› 1. Update now (runs `npm`"); got != "Update now (runs" {
		t.Fatalf("words %q", got)
	}
	q.Words = regexp.MustCompile(`(?i)release\s*notes`)
	if got := q.words("Release notes: x › 1. Update now (runs `npm`"); got != "Release notes" {
		t.Fatalf("words without a group %q", got)
	}
	if got := q.words("nothing to see"); got != "" {
		t.Fatalf("words of a screen without the question %q", got)
	}
}

// approvalChoices are the answers to Codex's command approval, as the
// catalog has them: each key picks at once.
var approvalChoices = []Option{{Label: "Yes, proceed", Input: "y"}, {Label: "Yes, and don't ask again", Input: "p"}, {Label: "No, tell Codex what to do differently", Input: "\x1b"}}

// approvalQuestions are the questions Codex's approvals are held by, as the
// built-in catalog entry has them: asked at any time.
func approvalQuestions() []StartupQuestion {
	codex, _ := catalog.Default().Get("codex")
	var out []StartupQuestion
	for _, q := range codex.Questions {
		if !q.Anytime {
			continue
		}
		answers := make([]Option, len(q.Answers))
		for i, a := range q.Answers {
			answers[i] = Option{Label: a.Label, Input: a.Input}
		}
		out = append(out, StartupQuestion{Pattern: regexp.MustCompile(q.Prompt), Words: regexp.MustCompile(q.Words), Anytime: true, Answers: answers})
	}
	return out
}

// approvalDialog is Codex 0.161's command approval for cmd as a TUI draws
// it at the bottom of an 80 by 24 screen: each run of cells placed with a
// cursor move, styles between the words, each key in a style of its own
// (the words of the screen the owner saw in the rc.7 app).
func approvalDialog(cmd string) string {
	return "\x1b[?2026h\x1b[12;1H\x1b[J\x1b[13;3H\x1b[1mWould you like to run the following command?\x1b[22m" +
		"\x1b[15;3HEnvironment: local\x1b[17;3HReason: May I rerun the curl command with network access?" +
		"\x1b[19;3H\x1b[2m$\x1b[22m " + cmd +
		"\x1b[20;1H\x1b[36m› 1. Yes, proceed (\x1b[1my\x1b[22m)" +
		"\x1b[21;3H\x1b[39m2. Yes, and don't ask again for commands that start with" +
		"\x1b[22;6H`" + cmd + "` (\x1b[1mp\x1b[22m)" +
		"\x1b[23;3H3. No, and tell Codex what to do differently (\x1b[1mesc\x1b[22m)" +
		"\x1b[24;3H\x1b[2mPress enter to confirm or esc to cancel\x1b[22m\x1b[?2026l"
}

// approvalNext is the next queued approval, for cmd, drawn over
// approvalDialog's the way Codex 0.161 draws it (approval_overlay.rs
// advances its queue in place, custom_terminal.rs writes only the cells
// that changed): the command's row and the prefix's, nothing else.
func approvalNext(cmd string) string {
	return "\x1b[?2026h\x1b[19;5H" + cmd + "\x1b[K\x1b[22;7H" + cmd + "` (\x1b[1mp\x1b[22m)\x1b[K\x1b[?2026l"
}

// approvalGone is what Codex draws once the dialog is answered: the overlay
// gives way to what it says next.
func approvalGone(next string) string { return "\x1b[12;1H\x1b[J" + next }

const approvalWords = "Would you like to run the following command? $ curl -sI https://example.com"

func asked(a Attention) bool { return a.State == AttentionNeedsInput && a.Source == SourceQuestion }

// Codex's command approval comes after the first prompt, which stops the
// startup questions' watcher: the questions asked during the work have a
// watcher of their own. The session needs input with the dialog's words
// and its three choices; a person's typed text is refused with nothing
// written (its letters would reach the dialog's keys, its Enter would pick
// Yes), Conductor's own text waits, an arrow answers nothing, and the
// choice's key alone answers it.
func TestAnApprovalAfterTheFirstPromptIsAskedWithItsChoices(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{
		TrustPattern: regexp.MustCompile(`Trust\s*this\s*folder\?`),
		TrustAnswers: []Option{{Label: "Yes, trust this folder", Input: "\r"}},
		Questions:    approvalQuestions(),
	}))
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte("\x1b[?2004h› Ask Codex to do anything"))
	waitPaste(t, s, true)
	// The crew's first prompt: its Enter ends the startup questions' watch.
	if res, err := s.Submit(t.Context(), Submission{Text: "check example.com", ByName: "crew"}); err != nil || !res.Entered {
		t.Fatalf("the first prompt: %+v %v", res, err)
	}
	nextWrite(t, p)
	nextWrite(t, p)
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	att := waitAttention(t, s, asked)
	if att.Message != approvalWords || att.Kind != KindPermission || !sameOptions(att.Options, approvalChoices) {
		t.Fatalf("attention %+v", att)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "yes please", By: sub}); !errors.Is(err, ErrQuestion) {
		t.Fatalf("typed text at the approval: %v", err)
	}
	// A chat to the agent gets the refusal's words, not the trust question's.
	if m := decodeControl(t, ChatErrorFrame(ErrQuestion, "r1")); m["code"] != proto.ErrCodeNotSent || m["message"] != QuestionWords || m["requestId"] != "r1" {
		t.Fatalf("the chat's refusal %v", m)
	}
	if res, err := s.Submit(t.Context(), Submission{Text: "handoff", ByName: "crew", UnlessWaiting: true}); err != nil || res.Typed {
		t.Fatalf("Conductor's own text: %+v %v", res, err)
	}
	noWrite(t, p, 200*time.Millisecond)
	if err := s.Input(sub, []byte("\x1b[B")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if a := s.Info().Attention; !asked(a) || a.Since == nil || *a.Since != *att.Since {
		t.Fatalf("an arrow answered the approval: %+v", a)
	}
	if err := s.Input(sub, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
	info := s.Info()
	if info.Attention.State != AttentionNone || info.LastAnswer == nil || info.LastAnswer.Message != approvalWords || info.LastAnswer.ByName != "nater" {
		t.Fatalf("the choice did not answer: %+v %+v", info.Attention, info.LastAnswer)
	}
}

// The bell Codex rings as it shows the dialog (while its terminal is not
// focused) gives way to the dialog's question once the screen is quiet; a
// bell after the question is seen leaves it as it is, words, choices and
// all. A bell with nothing waiting still asks.
func TestABellBeforeTheApprovalGivesWayAndOneAfterLeavesIt(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	sub, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte("\a"))
	if a := waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNeedsInput }); a.Source != SourceBell || a.Message != "terminal bell" {
		t.Fatalf("a bell with nothing waiting: %+v", a)
	}
	s.Input(sub, []byte("x"))
	nextWrite(t, p)
	time.Sleep(600 * time.Millisecond) // past the bell's burst limit
	// The bell before the dialog, in the same write.
	p.outW.Write([]byte("\a" + approvalDialog("curl -sI https://example.com")))
	att := waitAttention(t, s, asked)
	if att.Message != approvalWords || len(att.Options) != 3 {
		t.Fatalf("the question after the bell: %+v", att)
	}
	time.Sleep(600 * time.Millisecond)
	p.outW.Write([]byte("\a"))
	time.Sleep(300 * time.Millisecond)
	if a := s.Info().Attention; !asked(a) || a.Message != approvalWords || len(a.Options) != 3 || *a.Since != *att.Since {
		t.Fatalf("a bell after the question replaced it: %+v", a)
	}
}

// The same dialog, answered, comes again for the next command: it is asked
// again with its choices, and answered again, by Escape this time.
func TestTheSameApprovalAnsweredThenShownAgainIsAskedAgain(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	sub, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	first := waitAttention(t, s, asked)
	s.Input(sub, []byte("y"))
	nextWrite(t, p)
	if st := s.Info().Attention.State; st != AttentionNone {
		t.Fatalf("not answered: %q", st)
	}
	p.outW.Write([]byte(approvalGone("✔ You approved codex to run curl -sI https://example.com this time\r\nHTTP/2 200\r\n\x1b[2m• Working\x1b[22m")))
	time.Sleep(800 * time.Millisecond)
	if a := s.Info().Attention; a.State != AttentionNone {
		t.Fatalf("the answered dialog was asked again without being drawn: %+v", a)
	}
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	again := waitAttention(t, s, asked)
	if again.Message != approvalWords || len(again.Options) != 3 || !again.Since.After(*first.Since) {
		t.Fatalf("asked again: %+v", again)
	}
	s.Input(sub, []byte("\x1b"))
	if got := nextWrite(t, p); got != "\x1b" {
		t.Fatalf("written %q", got)
	}
	if st := s.Info().Attention.State; st != AttentionNone {
		t.Fatalf("Escape did not answer: %q", st)
	}
}

// A person who answers the dialog in its terminal before the session has
// seen the screen quiet leaves its words in the text the watcher keeps:
// the key that answers it (one of the choices' keys, or an Enter) puts
// them behind, so that the question is not raised once Codex has moved on.
func TestAnApprovalAnsweredInTheTerminalBeforeItIsSeenIsNotAskedAfter(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	sub, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	time.Sleep(100 * time.Millisecond) // read, not yet quiet
	s.Input(sub, []byte("y"))
	nextWrite(t, p)
	p.outW.Write([]byte(approvalGone("HTTP/2 200\r\n› ")))
	time.Sleep(900 * time.Millisecond)
	if a := s.Info().Attention; a.State == AttentionNeedsInput {
		t.Fatalf("the answered dialog was asked: %+v", a)
	}
}

// A dialog drawn just before Conductor types (a handoff, a broadcast) and
// not yet seen quiet is looked at before the text: nothing is written (the
// overlay would drop the paste, and the handoff with it), the handoff waits
// for the answer, and a person's reply is refused. One drawn during the
// pause, after the text, holds the Enter back: the screen is looked at
// again before it, and the Enter would have approved the command.
func TestAnApprovalDrawnJustBeforeASubmissionHoldsIt(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	sub, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte("\x1b[?2004h› "))
	waitPaste(t, s, true)
	time.Sleep(700 * time.Millisecond)
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	time.Sleep(50 * time.Millisecond) // read, not yet quiet
	res, err := s.Submit(t.Context(), Submission{Text: "over to you", ByName: "crew", UnlessWaiting: true})
	if err != nil || res.Typed || res.Entered {
		t.Fatalf("a handoff at the dialog: %+v %v", res, err)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "yes", By: sub}); !errors.Is(err, ErrQuestion) {
		t.Fatalf("a reply at the dialog: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
	if a := s.Info().Attention; !asked(a) {
		t.Fatalf("attention %+v", a)
	}

	s2, p2 := newLocalWith(t, quiet(Options{Questions: approvalQuestions(), SubmitPause: 100 * time.Millisecond}))
	p2.outW.Write([]byte("\x1b[?2004h› "))
	waitPaste(t, s2, true)
	time.Sleep(700 * time.Millisecond)
	var once sync.Once
	p2.onWrite = func() {
		once.Do(func() { go p2.outW.Write([]byte(approvalDialog("curl -sI https://example.com"))) })
	}
	res, err = s2.Submit(t.Context(), Submission{Text: "over to you", ByName: "crew", UnlessWaiting: true})
	if err != nil || !res.Typed || res.Entered {
		t.Fatalf("a handoff whose pause saw the dialog drawn: %+v %v", res, err)
	}
	nextWrite(t, p2)
	noWrite(t, p2, 200*time.Millisecond)
	if a := s2.Info().Attention; !asked(a) {
		t.Fatalf("attention %+v", a)
	}
}

// What the question takes the place of, and what it does not: a hook's
// report without choices (the PermissionRequest hook's "Allow exec?") gives
// way to the dialog's words and choices, and the same report coming late
// does not hide them; a report with choices of its own stays, and so does
// the question under an OSC notification.
func TestAnApprovalTakesThePlaceOfAWaitWithoutChoicesOnly(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "Allow exec?", SourceAPI, KindPermission, nil); err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	att := waitAttention(t, s, asked)
	if att.Message != approvalWords || len(att.Options) != 3 {
		t.Fatalf("attention %+v", att)
	}
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "Allow exec?", SourceAPI, KindPermission, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	p.outW.Write([]byte("\x1b]9;Codex wants to run a command\a"))
	time.Sleep(300 * time.Millisecond)
	if a := s.Info().Attention; !asked(a) || *a.Since != *att.Since {
		t.Fatalf("a report without choices hid the question: %+v", a)
	}

	s2, p2 := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	mine := []Option{{Label: "Allow", Input: "y"}, {Label: "Deny", Input: "n"}}
	if err := s2.TrySetAttentionFull(AttentionNeedsInput, "Allow curl?", SourceAPI, KindPermission, mine); err != nil {
		t.Fatal(err)
	}
	p2.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	time.Sleep(900 * time.Millisecond)
	if a := s2.Info().Attention; a.Source != SourceAPI || a.Message != "Allow curl?" || !sameOptions(a.Options, mine) {
		t.Fatalf("the question replaced a report with its choices: %+v", a)
	}
}

// Codex 0.161 advances queued approvals in place and writes only the cells
// that changed: the next approval, drawn after an answer, sends its command
// and its prefix alone. It is asked again, with its own command, since the
// session reads the screen, not the bytes since the answer.
func TestAQueuedApprovalDrawnAsWhatChangedIsAskedAgain(t *testing.T) {
	// The first answered by one of its choices, by a digit (Codex's list
	// picks on it at once), by Ctrl+C, or by a key the dialog takes that
	// Conductor does not know (d, n): the next is asked with its own words
	// and a Since of its own.
	for _, key := range []string{"y", "\x1b", "1", "3", "\x03", "d"} {
		s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
		sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
		if err != nil {
			t.Fatal(err)
		}
		p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
		first := waitAttention(t, s, asked)
		s.Input(sub, []byte(key))
		nextWrite(t, p)
		p.outW.Write([]byte(approvalNext("rm -rf build")))
		next := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Message != approvalWords })
		if next.Message != "Would you like to run the following command? $ rm -rf build" || len(next.Options) != 3 || !next.Since.After(*first.Since) {
			t.Fatalf("%q, the queued approval: %+v", key, next)
		}
		if key != "d" {
			if info := s.Info(); info.LastAnswer == nil || info.LastAnswer.Message != approvalWords || info.LastAnswer.ByName != "nater" {
				t.Fatalf("%q did not answer the first: %+v", key, info.LastAnswer)
			}
		}
	}
}

// The screen is as tall as the window, whatever its size: a dialog drawn
// at the bottom of a 500-row window reads whole.
func TestAnApprovalAtTheBottomOfATallWindowIsAsked(t *testing.T) {
	p := newFakeProc()
	s := NewLocal(Info{ID: "tall", Cwd: t.TempDir(), Cols: 80, Rows: 500}, p, quiet(Options{Questions: approvalQuestions()}))
	t.Cleanup(p.exit)
	dialog := strings.NewReplacer("\x1b[12;", "\x1b[488;", "\x1b[13;", "\x1b[489;", "\x1b[15;", "\x1b[491;", "\x1b[17;", "\x1b[493;", "\x1b[19;", "\x1b[495;",
		"\x1b[20;", "\x1b[496;", "\x1b[21;", "\x1b[497;", "\x1b[22;", "\x1b[498;", "\x1b[23;", "\x1b[499;", "\x1b[24;", "\x1b[500;").Replace(approvalDialog("curl -sI https://example.com"))
	p.outW.Write([]byte(dialog))
	if a := waitAttention(t, s, asked); a.Message != approvalWords {
		t.Fatalf("attention %+v", a)
	}
}

// A program that never stops drawing (a spinner) runs out the wait before
// a submission's Enter; a dialog drawn as it runs out is still found, since
// the screen is looked at once more, quiet or not, before the Enter.
func TestAnApprovalDrawnAsTheLookRunsOutHoldsTheEnter(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	stop := make(chan struct{})
	done := make(chan struct{})
	started := make(chan time.Time)
	go func() {
		defer close(done)
		var start time.Time
		drawn := false
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			case start = <-started:
			case <-time.After(40 * time.Millisecond):
			}
			p.outW.Write([]byte(fmt.Sprintf("\x1b[10;1H\x1b[K• Working (%d)", i)))
			if !drawn && !start.IsZero() && time.Since(start) > lookBudget-300*time.Millisecond {
				p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
				drawn = true
			}
		}
	}()
	time.Sleep(200 * time.Millisecond) // the spinner is drawing
	started <- time.Now()
	res, err := s.Submit(t.Context(), Submission{Text: "over to you", ByName: "crew", UnlessWaiting: true})
	close(stop)
	<-done
	if err != nil || !res.Typed || res.Entered {
		t.Fatalf("result %+v %v", res, err)
	}
	if a := s.Info().Attention; !asked(a) || a.Message != approvalWords {
		t.Fatalf("attention %+v", a)
	}
}

// The dialog can go with a key Conductor does not know as an answer (d,
// which Codex shows on some of its dialogs; a click): once a person typed
// into the session while the question showed and the screen no longer
// shows it, the question gives way, answered by that person. With no key
// typed it stays: the dialog cannot have been answered, and the held
// question keeps every Enter off it.
func TestAnApprovalGoneAfterAKeyTypedIntoItGivesWay(t *testing.T) {
	for _, key := range []string{"d", "\x1b[<0;12;23M"} {
		s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
		sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
		if err != nil {
			t.Fatal(err)
		}
		p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
		waitAttention(t, s, asked)
		s.Input(sub, []byte(key))
		nextWrite(t, p)
		if a := s.Info().Attention; !asked(a) {
			t.Fatalf("%q answered the question before the dialog went: %+v", key, a)
		}
		p.outW.Write([]byte(approvalGone("■ Conversation interrupted - tell the model what to do differently.\r\n› ")))
		a := waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone })
		if info := s.Info(); info.LastAnswer == nil || info.LastAnswer.ByName != "nater" || info.LastAnswer.Message != approvalWords {
			t.Fatalf("%q: %+v %+v", key, a, info.LastAnswer)
		}
	}

	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	att := waitAttention(t, s, asked)
	p.outW.Write([]byte(approvalGone("› ")))
	time.Sleep(900 * time.Millisecond)
	if a := s.Info().Attention; !asked(a) || *a.Since != *att.Since {
		t.Fatalf("the question went without a key: %+v", a)
	}
}

// The words are the command whole: a "$ " inside it does not start them
// again, rows of it are joined, and a command longer than a message can
// hold is cut with an ellipsis, never silently.
func TestAnApprovalsWordsAreItsWholeCommand(t *testing.T) {
	q := approvalQuestions()[0]
	screen := func(cmdRows ...string) string {
		return "  Would you like to run the following command?\n\n  Reason: needs the network.\n\n  " + strings.Join(cmdRows, "\n  ") +
			"\n\n› 1. Yes, proceed (y)\n  2. Yes, and don't ask again for commands that start with `rm` (p)\n  3. No, and tell Codex what to do differently (esc)\n\n  Press enter to confirm or esc to cancel"
	}
	if got := q.words(screen("$ rm -rf build; printf '$ done'")); got != "Would you like to run the following command? $ rm -rf build; printf '$ done'" {
		t.Fatalf("words %q", got)
	}
	if got := q.words(screen("$ cat <<'EOF' > notes.txt", "hello", "EOF")); got != "Would you like to run the following command? $ cat <<'EOF' > notes.txt hello EOF" {
		t.Fatalf("words of a command on three rows %q", got)
	}
	long := "$ " + strings.Repeat("echo abcdefghij && ", 40) + "rm -rf /tmp/x"
	got := q.words(screen(long))
	if len(got) > MaxAttentionMessage || !strings.HasSuffix(got, "…") || !strings.Contains(got, "$ echo abcdefghij && echo") || !utf8.ValidString(got) {
		t.Fatalf("words of a long command (%d bytes) %q", len(got), got)
	}
}

// Codex 0.161's other approvals: an accept-or-decline request ("No,
// continue without running it (d)", Escape only in the footer) and a
// network one ("Do you want to approve network access to …?", "Yes, just
// this once (y)"). Each is asked with the keys it shows, and neither text
// nor an Enter is typed into it.
func TestCodexsOtherApprovalsAreAsked(t *testing.T) {
	for _, tc := range []struct {
		name, screen, words string
		keys                []string
	}{
		{"deny", "\x1b[14;3HWould you like to run the following command?\x1b[16;3H$ git push --force\x1b[18;1H› 1. Yes, proceed (y)\x1b[19;3H2. No, continue without running it (d)\x1b[21;3HPress enter to confirm or esc to cancel",
			"Would you like to run the following command? $ git push --force", []string{"y", "d", "\x1b"}},
		{"network", "\x1b[14;3HDo you want to approve network access to \"example.com\"?\x1b[16;1H› 1. Yes, just this once (y)\x1b[17;3H2. Yes, and allow this host in the future (p)\x1b[18;3H3. No, and block this host in the future (d)\x1b[20;3HPress enter to confirm or esc to cancel",
			`Do you want to approve network access to "example.com"?`, []string{"y", "\x1b"}},
	} {
		s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
		sub, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false))
		if err != nil {
			t.Fatal(err)
		}
		p.outW.Write([]byte(tc.screen))
		a := waitAttention(t, s, asked)
		var keys []string
		for _, o := range a.Options {
			keys = append(keys, o.Input)
		}
		if a.Message != tc.words || strings.Join(keys, ",") != strings.Join(tc.keys, ",") {
			t.Fatalf("%s: %+v", tc.name, a)
		}
		if _, err := s.Submit(t.Context(), Submission{Text: "no, stop", By: sub}); !errors.Is(err, ErrQuestion) {
			t.Fatalf("%s: a reply: %v", tc.name, err)
		}
		noWrite(t, p, 100*time.Millisecond)
	}
}

// Conductor's own text that is no handoff, a crew's prompt, is refused like
// a person's at a dialog the screen shows: neither the text (the overlay
// drops a paste) nor its Enter (which picks Yes) is written.
func TestAPromptAtAnApprovalIsRefused(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	time.Sleep(50 * time.Millisecond) // read, not yet quiet
	res, err := s.Submit(t.Context(), Submission{Text: "Plan it.", ByName: "crew", Confirm: true})
	if !errors.Is(err, ErrQuestion) || res.Typed {
		t.Fatalf("a prompt at the dialog: %+v %v", res, err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// A look before a submission that finds nothing leaves the quiet period's
// own look to come: a question a person's unknown key took off the screen
// gives way once the screen is quiet, though a handoff looked in between and
// no output follows.
func TestALookBeforeASubmissionLeavesTheQuietLook(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	waitAttention(t, s, asked)
	s.Input(sub, []byte("n"))
	nextWrite(t, p)
	p.outW.Write([]byte(approvalGone("› ")))
	time.Sleep(50 * time.Millisecond)
	if res, err := s.Submit(t.Context(), Submission{Text: "over to you", ByName: "crew", UnlessWaiting: true}); err != nil || res.Typed {
		t.Fatalf("the handoff: %+v %v", res, err)
	}
	if a := waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone }); s.Info().LastAnswer == nil {
		t.Fatalf("not answered: %+v", a)
	}
}

// A report with choices of its own keeps its place over the dialog
// (unlessChoices), but the dialog on the screen holds every submission all
// the same: a reply to the report, a crew's prompt and a handoff write
// nothing, since the dialog would take the text and its Enter would pick
// Yes.
func TestADialogOnTheScreenHoldsSubmissionsWhateverTheSessionShows(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	sub, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	mine := []Option{{Label: "Postgres", Input: "Postgres\r"}, {Label: "SQLite", Input: "SQLite\r"}}
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "Which database?", SourceAPI, KindPrompt, mine); err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	time.Sleep(700 * time.Millisecond) // seen quiet: the report stays
	if a := s.Info().Attention; a.Source != SourceAPI {
		t.Fatalf("attention %+v", a)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "Postgres", By: sub}); !errors.Is(err, ErrQuestion) {
		t.Fatalf("a reply: %v", err)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "Plan it.", ByName: "crew", Confirm: true}); !errors.Is(err, ErrQuestion) {
		t.Fatalf("a prompt: %v", err)
	}
	if res, err := s.Submit(t.Context(), Submission{Text: "over to you", ByName: "crew", UnlessWaiting: true}); err != nil || res.Typed {
		t.Fatalf("a handoff: %+v %v", res, err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// A handoff that comes as a person answers the dialog, before the program
// has drawn what follows, does not ask the answered dialog again (the look
// before a submission finds nothing until output comes): it is typed with
// its Enter once the dialog is gone, and no question is left held.
func TestAHandoffAsTheDialogIsAnsweredLeavesNoQuestion(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte("\x1b[?2004h"))
	waitPaste(t, s, true)
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	waitAttention(t, s, asked)
	s.Input(sub, []byte("y"))
	nextWrite(t, p)
	type result struct {
		res SubmitResult
		err error
	}
	done := make(chan result, 1)
	go func() {
		res, err := s.Submit(t.Context(), Submission{Text: "over to you", ByName: "crew", UnlessWaiting: true})
		done <- result{res, err}
	}()
	time.Sleep(20 * time.Millisecond)
	p.outW.Write([]byte(approvalGone("✔ You approved codex to run curl -sI https://example.com this time\r\n› ")))
	r := <-done
	if r.err != nil || !r.res.Typed || !r.res.Entered {
		t.Fatalf("the handoff: %+v %v", r.res, r.err)
	}
	time.Sleep(900 * time.Millisecond)
	if a := s.Info().Attention; a.State == AttentionNeedsInput {
		t.Fatalf("a question left held: %+v", a)
	}
}
