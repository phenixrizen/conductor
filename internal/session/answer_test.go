package session

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/catalog"
	"github.com/phenixrizen/conductor/internal/proto"
)

// Replies that name the question they were shown for (AnswerInput,
// Submission.Question): of two replies to one question, the first is
// written and the second refused, nothing written, so that a late click
// never lands on the question that came next.

// blockFirstWrite holds the process's first write until release is closed,
// so that writes asked for meanwhile wait in the writer's queue.
func blockFirstWrite(p *fakeProc) (release func()) {
	ch := make(chan struct{})
	var once sync.Once
	p.onWrite = func() { once.Do(func() { <-ch }) }
	var closed sync.Once
	return func() { closed.Do(func() { close(ch) }) }
}

// writing waits until s's writer is making a write.
func writing(t *testing.T, s *Local) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.writer.mu.Lock()
		busy := s.writer.current != nil
		s.writer.mu.Unlock()
		if busy {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the writer never began a write")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// queued waits until n writes wait in s's writer behind the one being made.
func queued(t *testing.T, s *Local, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.writer.mu.Lock()
		got := len(s.writer.queue)
		s.writer.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d writes queued, want %d", got, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Every change of attention that shows a state gets an id of its own,
// which the wire's attention message carries and the question's line in
// the chat is; a cleared one has none. The welcome says the owner takes
// answers.
func TestEachQuestionHasAnIDOfItsOwn(t *testing.T) {
	s, _ := newLocalWith(t, quiet(Options{}))
	sink := newChanSink(false)
	if _, err := s.Attach("", RoleControl, "", 80, 24, sink); err != nil {
		t.Fatal(err)
	}
	if m := waitControl(sink, func(m map[string]any) bool { return m["t"] == proto.CtlWelcome }); m == nil || m["answer"] != true {
		t.Fatalf("welcome %v", m)
	}
	s.SetAttentionFull(AttentionNeedsInput, "Which database?", SourceAPI, KindPrompt, nil)
	first := s.Info().Attention
	if !proto.ValidQuestionID(first.ID) {
		t.Fatalf("first id %q", first.ID)
	}
	if m := waitControl(sink, func(m map[string]any) bool { return m["t"] == proto.CtlAttention && m["state"] == "needs_input" }); m == nil || m["id"] != first.ID {
		t.Fatalf("attention frame %v, want id %s", m, first.ID)
	}
	if m := waitControl(sink, func(m map[string]any) bool { return m["t"] == proto.CtlChat && m["kind"] == proto.ChatKindQuestion }); m == nil || m["id"] != first.ID {
		t.Fatalf("the question's line %v, want id %s", m, first.ID)
	}
	s.SetAttentionFull(AttentionNeedsInput, "Which port?", SourceAPI, KindPrompt, nil)
	second := s.Info().Attention
	if second.ID == "" || second.ID == first.ID {
		t.Fatalf("second id %q after %q", second.ID, first.ID)
	}
	// The same words again change nothing, the id included.
	s.SetAttentionFull(AttentionNeedsInput, "Which port?", SourceAPI, KindPrompt, nil)
	if got := s.Info().Attention.ID; got != second.ID {
		t.Fatalf("the same question again: id %q, want %q", got, second.ID)
	}
	s.SetAttention(AttentionNone, "", SourceAdmin)
	if got := s.Info().Attention; got.ID != "" {
		t.Fatalf("a cleared attention has id %q", got.ID)
	}
}

// Two controllers click the same choice of Codex's approval together,
// while the program is still taking another write: both replies pass the
// look as they are asked for and wait in the writer's queue; the first
// taken is written and answers the question, the second is refused with
// nothing written. The next approval, drawn after, is asked and waits.
func TestTwoRepliesToOneApprovalTheSecondIsRefused(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	jane, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	q := waitAttention(t, s, asked)
	// An arrow typed in the terminal moves the dialog's selection and
	// answers nothing; the program is slow to take it.
	release := blockFirstWrite(p)
	defer release()
	go s.Input(nater, []byte("\x1b[B"))
	writing(t, s)
	errs := make(chan error, 2)
	go func() { errs <- s.AnswerInput(t.Context(), nater, q.ID, []byte("y")) }()
	go func() { errs <- s.AnswerInput(t.Context(), jane, q.ID, []byte("y")) }()
	queued(t, s, 2)
	release()
	if got := nextWrite(t, p); got != "\x1b[B" {
		t.Fatalf("written %q", got)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
	var ok, refused int
	for range 2 {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrQuestionAnswered):
			refused++
		default:
			t.Fatalf("a reply: %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("%d written, %d refused", ok, refused)
	}
	noWrite(t, p, 200*time.Millisecond)
	if a := s.Info(); a.Attention.State != AttentionNone || a.LastAnswer == nil || a.LastAnswer.Message != approvalWords {
		t.Fatalf("the approval was not answered: %+v %+v", a.Attention, a.LastAnswer)
	}
	// Codex's next approval: asked with an id of its own, and it waits.
	p.outW.Write([]byte(approvalGone("✔ You approved codex to run curl -sI https://example.com\r\n")))
	p.outW.Write([]byte(approvalDialog("rm -rf build")))
	next := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.ID != q.ID })
	time.Sleep(300 * time.Millisecond)
	if a := s.Info().Attention; !asked(a) || a.ID != next.ID {
		t.Fatalf("the next approval did not wait: %+v", a)
	}
}

// A reply that comes once its question was answered, the next one asked
// meanwhile (the click that arrives late), is refused: nothing is written,
// and the next question still waits, its own reply answering it.
func TestALateReplyNeverLandsOnTheNextQuestion(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	q := waitAttention(t, s, asked)
	if err := s.AnswerInput(t.Context(), nater, q.ID, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
	p.outW.Write([]byte(approvalGone("✔ You approved codex to run curl -sI https://example.com\r\n")))
	p.outW.Write([]byte(approvalDialog("rm -rf build")))
	next := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.ID != q.ID })
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("the late reply: %v", err)
	}
	// Its line, typed for the question answered, likewise.
	if _, err := s.Submit(t.Context(), Submission{Text: "go on", By: jane, Question: q.ID}); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("the late line: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
	if a := s.Info().Attention; !asked(a) || a.ID != next.ID {
		t.Fatalf("the next question: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, next.ID, []byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "\x1b" {
		t.Fatalf("written %q", got)
	}
	// An Escape that ends a write may be read with what comes next: the
	// question is put behind, and cleared once the dialog goes.
	p.outW.Write([]byte(approvalGone("✗ You canceled the request to run rm -rf build\r\n")))
	waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone })
	if a := s.Info(); a.LastAnswer == nil || a.LastAnswer.ByName != "jane" {
		t.Fatalf("the next question's own reply: %+v %+v", a.Attention, a.LastAnswer)
	}
}

// A key typed into the terminal that answers the question (no id: the
// terminal's keys, an older page's click) puts it behind as the writer
// takes it: a reply to it is refused, though the question still shows
// while the key is being written, and so is one queued before the key was
// taken.
func TestAKeyTypedInTheTerminalPutsTheQuestionBehind(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	q := waitAttention(t, s, asked)
	release := blockFirstWrite(p)
	defer release()
	// An arrow the program is slow to take; behind it the terminal's y,
	// then the reply, both looked at as they were asked for, the question
	// waiting then.
	go s.Input(nater, []byte("\x1b[B"))
	writing(t, s)
	typed := make(chan error, 1)
	go func() { typed <- s.Input(nater, []byte("y")) }()
	queued(t, s, 1)
	replied := make(chan error, 1)
	go func() { replied <- s.AnswerInput(t.Context(), jane, q.ID, []byte("p")) }()
	queued(t, s, 2)
	release()
	if got := nextWrite(t, p); got != "\x1b[B" {
		t.Fatalf("written %q", got)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
	if err := <-typed; err != nil {
		t.Fatal(err)
	}
	if err := <-replied; !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("the reply after the key: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// An Enter typed into the terminal before a question showed, and written
// once it shows (it waited behind another write), answers nothing of it: a
// person's keys never resolve a question (the agent moves on from it), and
// a reply to the question queued after them is written. (Codex's review of
// this change, finding 2; the question's claim rule, 2026-10-15.)
func TestAKeyQueuedBeforeAQuestionShowedAnswersNothing(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater"}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	release := blockFirstWrite(p)
	defer release()
	go s.Input(nater, []byte("x"))
	writing(t, s)
	typed := make(chan error, 1)
	go func() { typed <- s.Input(nater, []byte("\r")) }()
	queued(t, s, 1)
	// Claude Code's permission dialog, reported by its hook while the Enter waits.
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "3"}}); err != nil {
		t.Fatal(err)
	}
	q := s.Info().Attention
	replied := make(chan error, 1)
	go func() { replied <- s.AnswerInput(t.Context(), jane, q.ID, []byte("1")) }()
	queued(t, s, 2)
	release()
	for _, want := range []string{"x", "\r", "1"} {
		if got := nextWrite(t, p); got != want {
			t.Fatalf("written %q, want %q", got, want)
		}
	}
	if err := <-typed; err != nil {
		t.Fatal(err)
	}
	if err := <-replied; err != nil {
		t.Fatalf("the reply behind the Enter: %v", err)
	}
	if info := s.Info(); info.Attention.State != AttentionNone || info.LastAnswer == nil || info.LastAnswer.ByName != "jane" {
		t.Fatalf("the reply did not answer: %+v %+v", info.Attention, info.LastAnswer)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// approvalDecided is the line Codex writes above its overlay as an approval
// is decided, before the next queued one.
func approvalDecided(cmd string) string {
	return "\x1b[5;1H\x1b[K✔ You approved codex to run " + cmd
}

// A dialog answered by a choice and asked again before the screen's watcher
// has seen it go (the program is slow to take the key) is the same dialog:
// asked answered, a click on it is refused, nothing written. Neither output
// that changes nothing on the screen (the cursor shown again), nor a change
// above the dialog (a status line), nor the dialog drawn again with the
// decision's line above it is a sign the key was taken: a next approval of
// the same words that never leaves the screen cannot be told from the one
// answered, and is answered in the terminal. Once the watcher has seen a
// quiet screen with no dialog, a dialog with those words is a question of its
// own, and takes its reply. (Codex's review of this change: first pass,
// finding 3; second pass, finding 1; third pass, finding 1.)
func TestADialogAskedAgainBeforeTheProgramDrewIsStillAnswered(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	first := waitAttention(t, s, asked)
	if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("y")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	again := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Since.After(*first.Since) })
	if err := s.AnswerInput(t.Context(), jane, again.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a click on the dialog asked again before the program drew: %v", err)
	}
	// stillRefused waits a look of the watcher, then checks that the dialog
	// it shows is answered: a click on it is refused, nothing written.
	stillRefused := func(what string) {
		t.Helper()
		time.Sleep(patternQuiet + 200*time.Millisecond)
		a := s.Info().Attention
		if !asked(a) {
			t.Fatalf("%s: the dialog is not asked: %+v", what, a)
		}
		if err := s.AnswerInput(t.Context(), jane, a.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
			t.Fatalf("%s: a click on the dialog: %v", what, err)
		}
		noWrite(t, p, 100*time.Millisecond)
	}
	p.outW.Write([]byte("\x1b[?25h"))
	stillRefused("output that changed nothing")
	p.outW.Write([]byte("\x1b[3;1H\x1b[KWorking (12s)"))
	stillRefused("a status line above the dialog")
	p.outW.Write([]byte(approvalDecided("curl -sI https://example.com") + approvalDialog("curl -sI https://example.com")))
	stillRefused("the same dialog drawn again under the decision's line")
	// The dialog goes, and the watcher sees the screen without it: the
	// same approval drawn after is a question of its own.
	p.outW.Write([]byte(approvalGone("✔ You approved codex to run curl -sI https://example.com\r\n")))
	time.Sleep(patternQuiet + 200*time.Millisecond)
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	fresh := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.ID != again.ID && a.ID != first.ID })
	if err := s.AnswerInput(t.Context(), jane, fresh.ID, []byte("y")); err != nil {
		t.Fatalf("the approval drawn anew: %v", err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
}

// The next approval, of other words, drawn while the answer's key is still
// being written (the program takes it and draws before the write returns)
// is a question of its own, and takes its reply. (Codex's review, second
// pass, finding 3.)
func TestTheNextDialogDrawnAsTheKeyIsWrittenIsAQuestionOfItsOwn(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	first := waitAttention(t, s, asked)
	var once sync.Once
	p.onWrite = func() {
		once.Do(func() {
			p.outW.Write([]byte(approvalDecided("curl -sI https://example.com") + approvalDialog("rm -rf build")))
		})
	}
	if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("y")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	next := waitAttention(t, s, func(a Attention) bool {
		return asked(a) && a.ID != first.ID && strings.Contains(a.Message, "rm -rf build")
	})
	if err := s.AnswerInput(t.Context(), jane, next.ID, []byte("y")); err != nil {
		t.Fatalf("the next approval: %v", err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
}

// A key that answers a dialog typed before the session has asked it (the
// watcher asks once the screen is quiet) answers it: the dialog asked
// after, before the program took it away, is asked answered, and a click
// on it is refused. (Codex's review, third pass, finding 2.)
func TestAKeyTypedBeforeTheDialogIsAskedClaimsIt(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	time.Sleep(100 * time.Millisecond) // read, not yet quiet
	if err := s.Input(nater, []byte("y")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	a := waitAttention(t, s, asked)
	if err := s.AnswerInput(t.Context(), jane, a.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a click on the dialog a key answered before it was asked: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// A digit answers a dialog only on one of its numbered lines, as the screen
// shows them, whatever choices the session offers for it: past them it does
// nothing, and the dialog, asked again, still waits and takes a click; on
// one of them it answers, and a click after it is refused. Codex's deny
// approval has two numbered lines and three choices (Escape among them),
// its network approval three lines and two choices. (Codex's review: second
// pass, finding 2; third pass, finding 3.)
func TestADigitAnswersADialogOnlyOnItsNumberedLines(t *testing.T) {
	deny := "\x1b[14;3HWould you like to run the following command?\x1b[16;3H$ git push --force\x1b[18;1H› 1. Yes, proceed (y)\x1b[19;3H2. No, continue without running it (d)\x1b[21;3HPress enter to confirm or esc to cancel"
	network := "\x1b[14;3HDo you want to approve network access to \"example.com\"?\x1b[16;1H› 1. Yes, just this once (y)\x1b[17;3H2. Yes, and allow this host in the future (p)\x1b[18;3H3. No, and block this host in the future (d)\x1b[20;3HPress enter to confirm or esc to cancel"
	for _, tc := range []struct {
		name, screen, digit string
		answers             bool
	}{
		{"past the lines", approvalDialog("curl -sI https://example.com"), "9", false},
		{"deny, past its two lines", deny, "3", false},
		{"network, its third line", network, "3", true},
		// A numbered list in the output above is not the dialog's (Codex's review, fourth pass, finding 3).
		{"deny, under a list of three", "\x1b[8;1H1. Build it\x1b[9;1H2. Test it\x1b[10;1H3. Deploy the build" + deny, "3", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(tc.screen))
			first := waitAttention(t, s, asked)
			s.Input(nater, []byte(tc.digit))
			nextWrite(t, p)
			if a := s.Info().Attention; a.ID != first.ID {
				t.Fatalf("the digit changed the dialog shown: %+v", a)
			}
			err := s.AnswerInput(t.Context(), jane, first.ID, []byte("y"))
			if tc.answers {
				// Consumed: the program's dialog has the digit.
				if !errors.Is(err, ErrQuestionAnswered) || err.Error() != QuestionConsumedWords {
					t.Fatalf("a click after the digit answered: %v", err)
				}
				noWrite(t, p, 200*time.Millisecond)
				return
			}
			if err != nil {
				t.Fatalf("a click on the dialog the digit left: %v", err)
			}
			if got := nextWrite(t, p); got != "y" {
				t.Fatalf("written %q", got)
			}
		})
	}
}

// A key typed at the next dialog the screen shows, before the session has
// asked it, while it still shows the one answered before (asked again,
// answered): the key reaches the dialog on the screen and answers it, and
// that dialog, asked after, is asked answered. (Codex's review, fourth
// pass, finding 1.)
func TestAKeyAtTheNextDialogAnswersItNotTheOneStillShown(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	first := waitAttention(t, s, asked)
	if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("y")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Since.After(*first.Since) })
	// The next approval drawn over the first, and a y typed at it at once.
	p.outW.Write([]byte(approvalNext("rm -rf build")))
	time.Sleep(100 * time.Millisecond) // read, not yet quiet
	if err := s.Input(jane, []byte("y")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	next := waitAttention(t, s, func(a Attention) bool { return asked(a) && strings.Contains(a.Message, "rm -rf build") })
	if err := s.AnswerInput(t.Context(), nater, next.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a click on the next approval a key answered before it was asked: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// A shortcut a dialog's rows name answers it although the session offers
// no choice for it: Codex's network approval shows (p) and (d) and is
// offered Yes and Cancel; p or d typed in its terminal answers it, and a
// click after is refused. (Codex's review, fourth pass, finding 2.)
func TestAShortcutTheDialogNamesAnswersIt(t *testing.T) {
	network := "\x1b[14;3HDo you want to approve network access to \"example.com\"?\x1b[16;1H› 1. Yes, just this once (y)\x1b[17;3H2. Yes, and allow this host in the future (p)\x1b[18;3H3. No, and block this host in the future (d)\x1b[20;3HPress enter to confirm or esc to cancel"
	for _, key := range []string{"p", "d"} {
		t.Run(key, func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(network))
			q := waitAttention(t, s, asked)
			if isChoice(q, []byte(key)) {
				t.Fatalf("%q is offered as a choice: the test needs one that is not", key)
			}
			if err := s.Input(nater, []byte(key)); err != nil {
				t.Fatal(err)
			}
			nextWrite(t, p)
			if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
				t.Fatalf("a click after %q: %v", key, err)
			}
			noWrite(t, p, 200*time.Millisecond)
		})
	}
}

// A reply to the dialog the session shows, sent once the screen shows the
// next one but before the session has asked it (it asks once the screen is
// quiet), is refused: its key would reach the next dialog. The dialog
// asked after takes its own reply.
func TestAReplyToADialogTheScreenNoLongerShowsIsRefused(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	first := waitAttention(t, s, asked)
	p.outW.Write([]byte(approvalNext("rm -rf build")))
	time.Sleep(100 * time.Millisecond) // read, not yet quiet
	if a := s.Info().Attention; a.ID != first.ID {
		t.Fatalf("the next dialog was asked already: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, first.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a click on the dialog the screen no longer shows: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
	next := waitAttention(t, s, func(a Attention) bool { return asked(a) && strings.Contains(a.Message, "rm -rf build") })
	if err := s.AnswerInput(t.Context(), jane, next.ID, []byte("y")); err != nil {
		t.Fatalf("the next dialog's own reply: %v", err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
}

// Keys a terminal sends at once (the host's own terminal forwards what it
// reads) answer a dialog when one of them does: an arrow then y is y. A
// bracketed paste is text the dialog does not take: a y pasted answers
// nothing, and the dialog still takes a click. (Codex's review, fifth
// pass, finding 1.)
func TestKeysSentAtOnceAnswerADialogWhenOneOfThemDoes(t *testing.T) {
	for _, tc := range []struct {
		name, keys string
		answers    bool
	}{
		{"an arrow then y", "\x1b[By", true},
		{"y then an arrow", "y\x1b[A", true},
		{"a y pasted", "\x1b[200~y\x1b[201~", false},
		{"two arrows", "\x1b[B\x1b[A", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
			q := waitAttention(t, s, asked)
			if err := s.InputWaiting(t.Context(), nater, []byte(tc.keys)); err != nil {
				t.Fatal(err)
			}
			nextWrite(t, p)
			err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y"))
			if tc.answers {
				if !errors.Is(err, ErrQuestionAnswered) {
					t.Fatalf("a click after %q: %v", tc.keys, err)
				}
				noWrite(t, p, 200*time.Millisecond)
				return
			}
			if err != nil {
				t.Fatalf("a click after %q, which answers nothing: %v", tc.keys, err)
			}
			if got := nextWrite(t, p); got != "y" {
				t.Fatalf("written %q", got)
			}
		})
	}
}

// The keys of writes in order: an escape sequence or a paste one write
// leaves unfinished goes on in the next, whose bytes are not keys of their
// own until it ends.
func TestTheKeysOfWritesCarryWhatOneLeavesUnfinished(t *testing.T) {
	var r keyReader
	read := func(data string) string {
		var out []string
		for _, k := range r.keys([]byte(data)) {
			out = append(out, string(k))
		}
		return strings.Join(out, "|")
	}
	for _, step := range []struct{ write, keys string }{
		{"\x1b[By", "\x1b[B|y"},
		{"\x1b[200~hello ", ""},
		{"y there\x1b[20", ""},
		{"1~1", "1"},
		{"\x1b[", ""},
		{"1;5Ay", "\x1b[1;5A|y"},
		{"\x1bO", ""},
		{"Pq", "\x1bOP|q"},
		// An Escape that ends a write is held, and read with the next as
		// one write: the start of the sequence it goes on with, an Alt
		// chord with its first key, the Escape key with an ESC after it.
		{"y\x1b", "y"},
		{"[3;1R", "\x1b[3;1R"},
		{"\x1b", ""},
		{"q", "\x1bq"},
		{"\x1b", ""},
		{"\x1b]11;rgb:1/2/3\ay", "\x1b|\x1b]11;rgb:1/2/3\a|y"},
		{"\x1b", ""},
		{"\x1b[A", "\x1b|\x1b[A"},
		// An X10 mouse report is one key, cut anywhere.
		{"\x1b[M 1!y", "\x1b[M 1!|y"},
		{"\x1b[M", ""},
		{" 1", ""},
		{"!y", "\x1b[M 1!|y"},
		{"\x1bP>|xterm\x1b\\\x1b[B", "\x1bP>|xterm\x1b\\|\x1b[B"},
		{"\x1b]10;rgb:f", ""},
		{"f/f/f\x1b", ""},
		{"\\y", "y"},
		{"\x1b[?" + strings.Repeat("1;", 20), ""},
		{"1;1c2", "2"},
	} {
		if got := read(step.write); got != step.keys {
			t.Fatalf("keys of %q: %q, want %q", step.write, got, step.keys)
		}
	}
}

// A startup question answered by one key of several written at once (the
// host's terminal forwards what it reads: an arrow, then the digit of
// Codex's "Skip until next version") reaches it as its choice alone does:
// consumed once written, a click after it refused, and withdrawn once the
// screen shows the program moved on. (Codex's review, sixth pass, finding
// 1.)
func TestAStartupQuestionAnsweredByOneKeyOfSeveral(t *testing.T) {
	upd := []Option{{Label: "Skip this update", Input: "\x1b"}, {Label: "Skip until next version", Input: "3"}}
	s, p := newLocalWith(t, quiet(Options{Questions: []StartupQuestion{{
		Pattern: regexp.MustCompile(`(?i)update\s*now\s*\(\s*runs`),
		Words:   regexp.MustCompile(`(?i)(update\s*available)\W*?(\d+(?:\.\d+)+\s*(?:→|->)\s*\d+(?:\.\d+)+)`),
		Answers: upd,
	}}}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(codexUpdateScreen))
	q := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.InputWaiting(t.Context(), nater, []byte("\x1b[B3")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("\x1b")); !errors.Is(err, ErrQuestionAnswered) || err.Error() != QuestionConsumedWords {
		t.Fatalf("a click after the batch: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
	p.outW.Write([]byte("\x1b[2J\x1b[H\x1b[2;3H› Ask Codex to do anything"))
	if a := waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone }); a.Source != SourceScreen || s.Info().LastAnswer != nil {
		t.Fatalf("the batch's question withdrawn: %+v, answered %+v", a, s.Info().LastAnswer)
	}
}

// A key typed before a startup question showed and written once it shows
// (it waited behind another write) reaches that question: consumed, a click
// after it refused, and the next startup question, of other words, asked in
// its place and answered in its turn. (Codex's review, seventh pass,
// finding 2.)
func TestAKeyQueuedBeforeAStartupQuestionAnswersAndClearsIt(t *testing.T) {
	upd := []Option{{Label: "Skip this update", Input: "\x1b"}, {Label: "Skip until next version", Input: "3"}}
	s, p := newLocalWith(t, quiet(Options{Questions: []StartupQuestion{{
		Pattern: regexp.MustCompile(`(?i)update\s*now\s*\(\s*runs`),
		Words:   regexp.MustCompile(`(?i)(update\s*available)\W*?(\d+(?:\.\d+)+\s*(?:→|->)\s*\d+(?:\.\d+)+)`),
		Answers: upd,
	}, {
		Pattern: regexp.MustCompile(`(?i)start the background server\?`),
		Answers: []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "2"}},
	}}}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	release := blockFirstWrite(p)
	defer release()
	go s.Input(nater, []byte("x"))
	writing(t, s)
	typed := make(chan error, 1)
	go func() { typed <- s.Input(nater, []byte("3")) }()
	queued(t, s, 1)
	p.outW.Write([]byte(codexUpdateScreen))
	q := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	release()
	for _, want := range []string{"x", "3"} {
		if got := nextWrite(t, p); got != want {
			t.Fatalf("written %q, want %q", got, want)
		}
	}
	if err := <-typed; err != nil {
		t.Fatal(err)
	}
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("\x1b")); !errors.Is(err, ErrQuestionAnswered) || err.Error() != QuestionConsumedWords {
		t.Fatalf("a click after the key: %v", err)
	}
	p.outW.Write([]byte("\x1b[2J\x1b[HStart the background server?\r\n› 1. Yes\r\n  2. No"))
	next := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust && a.ID != q.ID })
	if err := s.AnswerInput(t.Context(), jane, next.ID, []byte("1")); err != nil {
		t.Fatalf("the next startup question: %v", err)
	}
	if got := nextWrite(t, p); got != "1" {
		t.Fatalf("written %q", got)
	}
}

// startupQuestions are Codex's startup questions as the built-in catalog
// entry has them (those not asked at any time): hooks review, the
// background server, the update.
func startupQuestions() []StartupQuestion {
	codex, _ := catalog.Default().Get("codex")
	var out []StartupQuestion
	for _, q := range codex.Questions {
		if q.Anytime {
			continue
		}
		answers := make([]Option, len(q.Answers))
		for i, a := range q.Answers {
			answers[i] = Option{Label: a.Label, Input: a.Input}
		}
		sq := StartupQuestion{Pattern: regexp.MustCompile(q.Prompt), Answers: answers}
		if q.Words != "" {
			sq.Words = regexp.MustCompile(q.Words)
		}
		out = append(out, sq)
	}
	return out
}

// codexBackgroundScreen is Codex 0.161's background-server question as the
// e2e stub draws it: a digit picks at once, 1 runs without the daemon.
const codexBackgroundScreen = "Background server has incompatible feature settings\r\nThis session requires api_key_model_discovery to be enabled\r\n  1. Run without daemon this time\r\n  2. Restart with these settings\r\n> 3. Cancel\r\n"

// Codex asks whether to run without its background server, then whether to
// update, where 1 (or Enter) runs npm install -g @openai/codex. A 1 meant
// for the first never reaches the second: a second click on the first,
// doubled or late, is refused, nothing written, and the update question
// waits for its own answer. A look of the startup questions' watcher that
// took its text before the 1 was written, and fires after, does not ask the
// answered question again with an id a click could name. (The bell agent's
// review of its own design, the startup chain.)
func TestALateOrDoubledAnswerNeverReachesTheNextStartupQuestion(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if !strings.Contains(first.Message, "Background server") {
		t.Fatalf("the first question: %+v", first)
	}
	if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("1")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "1" {
		t.Fatalf("written %q", got)
	}
	if err := s.AnswerInput(t.Context(), jane, first.ID, []byte("1")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("the doubled 1: %v", err)
	}
	// A look in flight since before the 1 (its text the first question's)
	// fires only now; looks are made one at a time.
	s.trust.fireMu.Lock()
	s.trust.mu.Lock()
	s.trust.looked = 0
	s.trust.mu.Unlock()
	s.fireTrust(codexBackgroundScreen)
	s.trust.fireMu.Unlock()
	if a := s.Info().Attention; a.State != AttentionNone {
		t.Fatalf("an older look asked the answered question again: %+v", a)
	}
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust && a.ID != first.ID })
	if !strings.Contains(update.Message, "Update available") {
		t.Fatalf("the update question: %+v", update)
	}
	if err := s.AnswerInput(t.Context(), jane, first.ID, []byte("1")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("the late 1: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
	if a := s.Info().Attention; a.ID != update.ID {
		t.Fatalf("the update question does not wait: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); err != nil {
		t.Fatalf("the update question's own answer: %v", err)
	}
	if got := nextWrite(t, p); got != "3" {
		t.Fatalf("written %q", got)
	}
}

// A 1 typed at the background-server question before the session has asked
// it (it asks once the screen is quiet) answers it: the update question
// drawn after is the one asked, and takes its own reply; the answered
// question is never asked again. (Codex's review, tenth pass, finding 1.)
func TestAStartupAnswerTypedBeforeItIsAskedLeavesTheNextAskable(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	time.Sleep(100 * time.Millisecond) // read, not yet quiet
	if err := s.Input(nater, []byte("1")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if !strings.Contains(update.Message, "Update available") {
		t.Fatalf("the question asked after the 1: %+v", update)
	}
	if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); err != nil {
		t.Fatalf("the update question's own answer: %v", err)
	}
	if got := nextWrite(t, p); got != "3" {
		t.Fatalf("written %q", got)
	}
}

// The background-server question answered, then drawn again by the program
// before it moves on (it is slow to take the key): asked again, it is
// answered, and a second 1 is refused, nothing written; the update question
// drawn after takes its place and its own reply. (Codex's review, eleventh
// pass, finding 1.)
func TestAStartupQuestionDrawnAgainAfterItsAnswerStaysAnswered(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("1")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexBackgroundScreen))
	again := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust && a.ID != first.ID })
	if err := s.AnswerInput(t.Context(), jane, again.ID, []byte("1")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a second 1 for the question drawn again: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool {
		return a.Source == SourceTrust && strings.Contains(a.Message, "Update available")
	})
	if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); err != nil {
		t.Fatalf("the update question's own answer: %v", err)
	}
	if got := nextWrite(t, p); got != "3" {
		t.Fatalf("written %q", got)
	}
}

// A startup question noted answered while it still shows (published as a
// key was being written, between the write's look and its answer) never
// keeps the next startup question from being asked: the update question
// takes its place. (Codex's review, eleventh pass, finding 2.)
func TestAnAnsweredStartupQuestionNeverHoldsTheNextBack(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	// Consumed, still showing: what the race leaves.
	s.mu.Lock()
	s.consumeLocked(s.info.Attention.ID)
	s.mu.Unlock()
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust && a.ID != first.ID })
	if !strings.Contains(update.Message, "Update available") {
		t.Fatalf("the question asked: %+v", update)
	}
	if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); err != nil {
		t.Fatalf("the update question's own answer: %v", err)
	}
	if got := nextWrite(t, p); got != "3" {
		t.Fatalf("written %q", got)
	}
}

// A look of the startup questions' watcher older than an answer is weighed
// as the question is set, under the lock that sets it: one that comes in
// after the answer, though it began before, sets nothing. (Codex's review,
// tenth pass, finding 2.)
func TestAnOlderStartupLookSetsNothingOnceAnAnswerCame(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	look := s.trust.LookedAt() // the look that asked it, its check passed
	if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("1")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	// The look's question set only now, after the answer.
	s.setAttentionLooked(AttentionNeedsInput, first.Message, SourceTrust, KindPrompt, first.Options, unlessWaiting|startupLook, anyTurn, look)
	if a := s.Info().Attention; a.State != AttentionNone {
		t.Fatalf("an older look set the answered question again: %+v", a)
	}
}

// A terminal's colour report cut in its body (its end, with the BEL, in a
// second write) is no key a person typed: a question an agent reported (one
// the session does not hold, which any key typed would answer) keeps its
// id, still waits, and takes a click. (Codex's review, tenth pass, finding
// 3.)
func TestAReportCutInItsBodyLeavesAReportedQuestionWaiting(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater"}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, []Option{{Label: "Yes", Input: "1"}}); err != nil {
		t.Fatal(err)
	}
	q := s.Info().Attention
	for _, w := range []string{"\x1b]10;rgb:ff", "ff/ffff/ffff\a"} {
		if err := s.InputWaiting(t.Context(), nater, []byte(w)); err != nil {
			t.Fatal(err)
		}
		nextWrite(t, p)
	}
	if a := s.Info().Attention; a.ID != q.ID || a.State != AttentionNeedsInput {
		t.Fatalf("a report cut in two answered the question: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("1")); err != nil {
		t.Fatalf("a click after the report: %v", err)
	}
	if got := nextWrite(t, p); got != "1" {
		t.Fatalf("written %q", got)
	}
}

// A reply to a startup question the screen no longer shows (the program has
// drawn the next before the watcher asked it) is refused: its key would
// reach the next question.
func TestAReplyToAStartupQuestionTheScreenNoLongerShowsIsRefused(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexUpdateScreen))
	time.Sleep(100 * time.Millisecond) // read, not yet quiet
	if a := s.Info().Attention; a.ID != first.ID {
		t.Fatalf("the update question was asked already: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, first.ID, []byte("1")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a 1 for the question the screen no longer shows: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// A paste, or an escape sequence, a terminal's reads cut in two is still
// no key in its second part: a y inside the paste, the digit of a cut
// sequence, answer no dialog, which still takes a click. (Codex's review,
// sixth pass, finding 2.)
func TestAPasteOrASequenceCutInTwoAnswersNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes []string
	}{
		{"a paste", []string{"\x1b[200~hello ", "y there\x1b[201~"}},
		{"a sequence", []string{"\x1b[", "1;5A"}},
		// A paste of a choice's key alone, its markers apart (Codex's review, seventh pass, finding 1).
		{"a paste around a choice", []string{"\x1b[200~", "y", "\x1b[201~"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
			q := waitAttention(t, s, asked)
			for _, w := range tc.writes {
				if err := s.InputWaiting(t.Context(), nater, []byte(w)); err != nil {
					t.Fatal(err)
				}
				nextWrite(t, p)
			}
			if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y")); err != nil {
				t.Fatalf("a click after %s cut in two: %v", tc.name, err)
			}
			if got := nextWrite(t, p); got != "y" {
				t.Fatalf("written %q", got)
			}
		})
	}
}

// A terminal's own report cut across two writes (xterm answering a mode
// query, its final y in the second) is no key a person typed: it answers
// no dialog, which still waits and takes a click. (Codex's review, eighth
// pass, finding 1.)
func TestAReportCutBeforeItsFinalByteAnswersNothing(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	q := waitAttention(t, s, asked)
	for _, w := range []string{"\x1b[?2004;1$", "y"} {
		if err := s.InputWaiting(t.Context(), nater, []byte(w)); err != nil {
			t.Fatal(err)
		}
		nextWrite(t, p)
	}
	if a := s.Info().Attention; a.ID != q.ID {
		t.Fatalf("the report's end cleared the dialog: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y")); err != nil {
		t.Fatalf("a click after a report cut in two: %v", err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
}

// A terminal's colour or version report (an OSC or DCS string, ended by
// BEL or ST), written with an arrow, or cut across writes, answers no
// dialog: its Escapes are the string's, not the Escape key, and the dialog
// still takes a click. (Codex's review, ninth pass.)
func TestAColourOrVersionReportAnswersNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes []string
	}{
		{"a colour report and an arrow", []string{"\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\\x1b[B"}},
		{"a version report and an arrow", []string{"\x1bP>|xterm.js(6.0.0)\x1b\\\x1b[A"}},
		{"a colour report cut in its ST", []string{"\x1b]11;rgb:1e1e/1e1e/2e2e\x1b", "\\"}},
		{"a colour report cut in its body", []string{"\x1b]10;rgb:ff", "ff/ffff/ffff\a"}},
		// A device report cut past the reader's carry: its rest is no key either (Codex's review, twelfth pass).
		{"a long report cut past the carry", []string{"\x1b[?" + strings.Repeat("1;", 16), "1c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
			q := waitAttention(t, s, asked)
			for _, w := range tc.writes {
				if err := s.InputWaiting(t.Context(), nater, []byte(w)); err != nil {
					t.Fatal(err)
				}
				nextWrite(t, p)
			}
			if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y")); err != nil {
				t.Fatalf("a click after %s: %v", tc.name, err)
			}
			if got := nextWrite(t, p); got != "y" {
				t.Fatalf("written %q", got)
			}
		})
	}
}

// A reply that comes while another connection's paste is being written
// (its start written, its end not yet) is refused, nothing written and the
// question not noted, for its key would be read as the paste's text; once
// the paste has ended it is taken. (Codex's review, eighth pass, finding 2.)
func TestAReplyDuringAnotherConnectionsPasteWaitsForItsEnd(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	q := waitAttention(t, s, asked)
	if err := s.InputWaiting(t.Context(), nater, []byte("\x1b[200~notes from ")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y")); !errors.Is(err, ErrInputBusy) {
		t.Fatalf("a click during a paste: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
	if err := s.InputWaiting(t.Context(), nater, []byte("the meeting\x1b[201~")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y")); err != nil {
		t.Fatalf("a click once the paste ended: %v", err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
	if m := decodeControl(t, AnswerResult("r", ErrInputBusy)); m["code"] != "input_failed" || m["message"] != ErrInputBusy.Error() {
		t.Fatalf("the busy word: %v", m)
	}
}

// A look of the screen's watcher that took its text before a key answered
// the dialog (its miss runs after) does not see the answered dialog go: the
// dialog, asked after, is asked answered. (Codex's review, fifth pass,
// finding 2.)
func TestALookOlderThanAnAnswerDoesNotForgetIt(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	time.Sleep(100 * time.Millisecond) // read, not yet quiet
	if err := s.Input(nater, []byte("y")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	// A look in flight since before the dialog was drawn (its text blank)
	// misses only now; looks are made one at a time.
	s.asked.fireMu.Lock()
	s.asked.mu.Lock()
	s.asked.looked = 0
	s.asked.mu.Unlock()
	s.missAsked("")
	s.asked.fireMu.Unlock()
	a := waitAttention(t, s, asked)
	if err := s.AnswerInput(t.Context(), jane, a.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a click on the dialog an older look forgot: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// A reply that names no question is written as keystrokes always were (an
// older page): it answers nothing, the question stays until the agent
// moves on, and a reply naming it is still taken.
func TestAReplyWithNoIDAnswersNothing(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	sub, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater"}, newChanSink(false))
	s.SetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "3"}})
	q := s.Info().Attention
	if err := s.Input(sub, []byte("1")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "1" {
		t.Fatalf("written %q", got)
	}
	if a := s.Info(); a.Attention.ID != q.ID || a.LastAnswer != nil {
		t.Fatalf("keys naming no question answered: %+v %+v", a.Attention, a.LastAnswer)
	}
	if err := s.AnswerInput(t.Context(), sub, q.ID, []byte("3")); err != nil {
		t.Fatalf("a reply after the keys: %v", err)
	}
	if got := nextWrite(t, p); got != "3" {
		t.Fatalf("written %q", got)
	}
	if a := s.Info(); a.Attention.State != AttentionNone || a.LastAnswer == nil || a.LastAnswer.ByName != "nater" {
		t.Fatalf("%+v %+v", a.Attention, a.LastAnswer)
	}
}

// Claude Code's numbered permission question (a hook's report, no dialog
// watched): of a line and a choice sent for it together, the first the
// writer takes answers it; the line's text claims the question as it is
// written, so the choice is refused rather than typed into the line's
// pause. A second line for the same question is refused once the first
// has answered it, and a view link may not reply at all.
func TestALineAndAChoiceForOneQuestion(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{SubmitPause: 100 * time.Millisecond}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater"}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	guest, _ := s.AttachWith(AttachOptions{Role: RoleView, Name: "guest"}, newChanSink(false))
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "Which database?", SourceAPI, KindPrompt, nil); err != nil {
		t.Fatal(err)
	}
	q := s.Info().Attention
	if err := s.AnswerInput(t.Context(), guest, q.ID, []byte("1")); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("a view link's reply: %v", err)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "x", By: guest, Question: q.ID}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("a view link's line: %v", err)
	}
	line := make(chan error, 1)
	go func() {
		_, err := s.Submit(t.Context(), Submission{Text: "Postgres", By: nater, Question: q.ID})
		line <- err
	}()
	if got := nextWrite(t, p); got != "Postgres" {
		t.Fatalf("written %q", got)
	}
	// In the line's pause the question still shows, claimed by the line.
	if a := s.Info().Attention; a.ID != q.ID {
		t.Fatalf("attention %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("2")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("the choice in the line's pause: %v", err)
	}
	if got := nextWrite(t, p); got != "\r" {
		t.Fatalf("written %q", got)
	}
	if err := <-line; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "SQLite", By: jane, Question: q.ID}); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a second line: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
	if a := s.Info(); a.Attention.State != AttentionNone || a.LastAnswer == nil || a.LastAnswer.ByName != "nater" {
		t.Fatalf("%+v %+v", a.Attention, a.LastAnswer)
	}
	// A line for a question that never was, or with nothing waiting.
	if _, err := s.Submit(t.Context(), Submission{Text: "hello", By: jane, Question: "0123456789abcdef"}); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a line for no question: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
}

// The owner's word on an answer: answer_ok naming its request, nothing
// without one; the refusal naming it, question_answered with its words; a
// request id out of its bounds named by nothing.
func TestAnswerResultFrames(t *testing.T) {
	if f := AnswerResult("", nil); f != nil {
		t.Fatalf("an answer with no request id: %s", f)
	}
	if m := decodeControl(t, AnswerResult("a1", nil)); m["t"] != proto.CtlAnswerOK || m["requestId"] != "a1" {
		t.Fatalf("ok %v", m)
	}
	if f := AnswerResult("a2", ErrSubmitPending); f != nil {
		t.Fatalf("a line accepted has no word yet: %s", f)
	}
	if m := decodeControl(t, AnswerResult("a4", ErrLineStranded)); m["t"] != proto.CtlError || m["code"] != "input_failed" || m["message"] != LineStrandedWords {
		t.Fatalf("a line stranded %v", m)
	}
	if m := decodeControl(t, AnswerResult("a5", &questionAnsweredError{QuestionClaimedWords})); m["code"] != proto.ErrCodeQuestionAnswered || m["message"] != QuestionClaimedWords {
		t.Fatalf("a question being answered %v", m)
	}
	if m := decodeControl(t, AnswerResult("a3", ErrQuestionAnswered)); m["t"] != proto.CtlError || m["code"] != proto.ErrCodeQuestionAnswered || m["message"] != QuestionAnsweredWords || m["requestId"] != "a3" {
		t.Fatalf("refused %v", m)
	}
	for err, code := range map[error]string{ErrReadOnly: proto.ErrCodeReadOnly, ErrSessionEnded: proto.ErrCodeSessionEnded, ErrQuestion: proto.ErrCodeNotSent, ErrTrustQuestion: proto.ErrCodeNotSent, ErrBadAnswer: proto.ErrCodeBadFrame, ErrInputStalled: "input_failed", ErrTooManyRequests: proto.ErrCodeTooManyRequests} {
		if m := decodeControl(t, AnswerResult("r", err)); m["code"] != code || m["requestId"] != "r" {
			t.Fatalf("%v: %v", err, m)
		}
	}
	long := "0123456789abcdef0123456789abcdef0"
	if m := decodeControl(t, AnswerResult(long, ErrBadAnswer)); m["requestId"] != nil {
		t.Fatalf("a request id past its bound was named: %v", m)
	}
}

// A terminal Codex asked to tell its keys apart (the kitty keyboard
// protocol, a host's own terminal) sends Escape as ESC [ 2 7 u and Ctrl+C
// as ESC [ 9 9 ; 5 u: each cancels the approval dialog as the plain key
// does, and puts the question behind for a reply queued after it, nothing
// more written. (Codex's review, thirteenth pass, finding 1.)
func TestAnEncodedEscapeOrCtrlCPutsTheQuestionBehind(t *testing.T) {
	for _, key := range []string{"\x1b[27u", "\x1b[99;5u", "\x1b[27;129u"} {
		t.Run(strings.ReplaceAll(key, "\x1b", "ESC"), func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
			q := waitAttention(t, s, asked)
			release := blockFirstWrite(p)
			defer release()
			go s.Input(nater, []byte("\x1b[B"))
			writing(t, s)
			typed := make(chan error, 1)
			go func() { typed <- s.Input(nater, []byte(key)) }()
			queued(t, s, 1)
			replied := make(chan error, 1)
			go func() { replied <- s.AnswerInput(t.Context(), jane, q.ID, []byte("y")) }()
			queued(t, s, 2)
			release()
			nextWrite(t, p)
			if got := nextWrite(t, p); got != key {
				t.Fatalf("written %q", got)
			}
			if err := <-typed; err != nil {
				t.Fatal(err)
			}
			if err := <-replied; !errors.Is(err, ErrQuestionAnswered) {
				t.Fatalf("the reply after the encoded key: %v", err)
			}
			noWrite(t, p, 200*time.Millisecond)
		})
	}
}

// xterm's modifyOtherKeys Ctrl+C (ESC [ 2 7 ; 5 ; 9 9 ~), which Codex's
// crossterm does not read as Ctrl+C: the approval stays open, and a reply
// to it after the key is written. (Codex's review, twenty-second pass,
// finding 2.)
func TestModifyOtherKeysAnswersNothing(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	q := waitAttention(t, s, asked)
	if err := s.Input(nater, []byte("\x1b[27;5;99~")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if a := s.Info().Attention; a.ID != q.ID || a.State != AttentionNeedsInput {
		t.Fatalf("the key cleared the approval: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y")); err != nil {
		t.Fatalf("the reply after the key: %v", err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
}

// A key's release (ESC [ 2 7 ; 1 : 3 u), and the terminal's reply to the
// protocol's query (ESC [ ? 1 u), are no keys: they answer nothing, and a
// reply to the dialog after them is written.
func TestAKeysReleaseAnswersNothing(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	q := waitAttention(t, s, asked)
	for _, in := range []string{"\x1b[27;1:3u", "\x1b[?1u"} {
		if err := s.Input(nater, []byte(in)); err != nil {
			t.Fatal(err)
		}
		nextWrite(t, p)
	}
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y")); err != nil {
		t.Fatalf("the reply after a release: %v", err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
}

// The key reader reads what a terminal encodes as the key it is: a press or
// a repeat (Escape, Enter, a character, Shift and a letter, Ctrl and a
// letter, Caps or Num Lock on or not), a release as no key; what reads
// otherwise unencoded (Shift+Enter, an arrow, a report) stays the
// sequence it is; one cut across two writes is read whole.
func TestTheKeyReaderReadsEncodedKeys(t *testing.T) {
	for in, want := range map[string][]string{
		"\x1b[27u":        {"\x1b"},
		"\x1b[27;1:2u":    {"\x1b[27;1:2u"},
		"\x1b[27;1:3u":    nil,
		"\x1b[99;5u":      {"\x03"},
		"\x1b[99;133u":    {"\x03"},
		"\x1b[99;5:3u":    nil,
		"\x1b[49u":        {"1"},
		"\x1b[121u":       {"y"},
		"\x1b[97:65;2u":   {"A"},
		"\x1b[97;2u":      {"A"},
		"\x1b[13u":        {"\r"},
		"\x1b[13;2u":      {"\x1b[13;2u"},
		"\x1b[99;7u":      {"\x1b[99;7u"},
		"\x1b[109;5u":     {"\x1b[109;5u"},
		"\x1b[57399u":     {"0"},
		"\x1b[57400u":     {"1"},
		"\x1b[57414u":     {"\r"},
		"\x1b[57400;1:3u": nil,
		"\x1b[57441u":     {"\x1b[57441u"},
		"\x1b[27;5;99~":   {"\x1b[27;5;99~"},
		"\x1b[2~":         {"\x1b[2~"},
		"\x1b[?1u":        {"\x1b[?1u"},
		"\x1b[A":          {"\x1b[A"},
		"y\x1b[27u2":      {"y", "\x1b", "2"},
		"\x1b[99999999u":  {"\x1b[99999999u"},
		// ESC and a key at once is one Alt chord, two ESCs the Escape key.
		"\x1bz":        {"\x1bz"},
		"\x1b\r":       {"\x1b\r"},
		"\x1b\xc3\xa9": {"\x1b\xc3\xa9"},
		"\x1b\x1b":     {"\x1b"},
		"y\x1b\x1b[A":  {"y", "\x1b", "\x1b[A"},
	} {
		var r keyReader
		var got []string
		for _, k := range r.keys([]byte(in)) {
			got = append(got, string(k))
		}
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("keys(%q) = %q, want %q", in, got, want)
		}
	}
	var r keyReader
	if got := r.keys([]byte("\x1b[2")); len(got) != 0 {
		t.Fatalf("the start of a cut sequence: %q", got)
	}
	if got := r.keys([]byte("7u")); len(got) != 1 || string(got[0]) != "\x1b" {
		t.Fatalf("its end: %q", got)
	}
}

// Codex's background-server question takes 2 (restart with these
// settings), which no quick reply offers: typed in a terminal, it answers
// the question as its offered choices do, so a 1 queued behind it is
// refused, nothing written, and the update question drawn after is asked
// and takes its own reply. A digit past its items answers nothing.
// (Codex's review, thirteenth pass, finding 2.)
func TestADigitNoReplyOffersAnswersAStartupQuestion(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.Input(nater, []byte("7")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if err := s.questionWaits(first.ID); err != nil {
		t.Fatalf("a digit past its items answered the question: %v", err)
	}
	release := blockFirstWrite(p)
	defer release()
	go s.Input(nater, []byte("\x1b[B"))
	writing(t, s)
	typed := make(chan error, 1)
	go func() { typed <- s.Input(nater, []byte("2")) }()
	queued(t, s, 1)
	replied := make(chan error, 1)
	go func() { replied <- s.AnswerInput(t.Context(), jane, first.ID, []byte("1")) }()
	queued(t, s, 2)
	release()
	nextWrite(t, p)
	if got := nextWrite(t, p); got != "2" {
		t.Fatalf("written %q", got)
	}
	if err := <-typed; err != nil {
		t.Fatal(err)
	}
	if err := <-replied; !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("the 1 after the 2: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool {
		return a.Source == SourceTrust && strings.Contains(a.Message, "Update available")
	})
	if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); err != nil {
		t.Fatalf("the update question's own answer: %v", err)
	}
	if got := nextWrite(t, p); got != "3" {
		t.Fatalf("written %q", got)
	}
}

// A startup question whose digit only moves the highlight (Codex's hooks
// review: an Enter picks) is not answered by a digit: its reply is still
// written after one.
func TestADigitThatMovesTheHighlightAnswersNothing(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte("Hooks need review\r\n7 hooks are new or changed.\r\n› 1. Review hooks\r\n  2. Trust all and continue\r\n  3. Continue without trusting (hooks won't run)"))
	hooks := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.Input(nater, []byte("2")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if err := s.AnswerInput(t.Context(), jane, hooks.ID, []byte("3\r")); err != nil {
		t.Fatalf("the reply after a digit that moved the highlight: %v", err)
	}
	if got := nextWrite(t, p); got != "3\r" {
		t.Fatalf("written %q", got)
	}
}

// Codex's update question whose heading and first item have gone from the
// text the startup questions' watcher keeps (the program redrew a line of
// it many times): its items are not read, so a digit it offers no reply
// for (7, which does nothing) answers nothing, and the question still
// holds a submission back, whose Enter would run npm install. (Codex's
// review, fourteenth pass, finding 1.)
func TestAnUnreadDigitNeverClearsAStartupQuestion(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	p.outW.Write([]byte(strings.Repeat("\x1b[6;6HSkip", 300)))
	time.Sleep(200 * time.Millisecond)
	if text := s.trust.Text(); strings.Contains(text, "Update") {
		t.Fatalf("the heading is still in the text kept: %q", text[:80])
	}
	if err := s.Input(nater, []byte("7")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
		t.Fatalf("a digit not read cleared the question: %+v", a)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); !errors.Is(err, ErrTrustQuestion) {
		t.Fatalf("a submission at the question: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// A terminal's report cut in two by the host's reads, at any byte (right
// after its ESC too), while Codex's update question shows: Escape and 3
// are among its answers, and neither the ESC nor the report's digits clear
// it; a submission is still held back, nothing written but the report.
// The rest comes at once, or late (the program may read both reads as
// one): an Escape that ends a read is never taken for the key on time
// alone. (Codex's review, fourteenth pass, finding 2; fifteenth, 2.)
func TestAReportCutAnywhereNeverClearsAStartupQuestion(t *testing.T) {
	const report = "\x1b[3;1R"
	for cut := 1; cut < len(report); cut++ {
		for _, late := range []time.Duration{0, 4 * escWait} {
			t.Run(fmt.Sprintf("cut at %d, rest after %s", cut, late), func(t *testing.T) {
				s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
				nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
				p.outW.Write([]byte(codexUpdateScreen))
				update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
				for i, part := range []string{report[:cut], report[cut:]} {
					if i == 1 {
						time.Sleep(late)
					}
					if err := s.InputWaiting(t.Context(), nater, []byte(part)); err != nil {
						t.Fatal(err)
					}
					if got := nextWrite(t, p); got != part {
						t.Fatalf("written %q", got)
					}
				}
				time.Sleep(2 * escWait)
				if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
					t.Fatalf("the report cleared the question: %+v", a)
				}
				if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); !errors.Is(err, ErrTrustQuestion) {
					t.Fatalf("a submission at the question: %v", err)
				}
				noWrite(t, p, 100*time.Millisecond)
			})
		}
	}
}

// The update question drawn and not asked yet (the watcher asks once the
// screen is quiet) when a terminal's read ends in an ESC and the next
// goes on with a report: the question is still asked, and holds a
// submission back. (Codex's review, fifteenth pass, finding 1.)
func TestAnEscapeBeforeAStartupQuestionIsAskedKeepsItAskable(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: append(startupQuestions(), approvalQuestions()...)}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexUpdateScreen))
	time.Sleep(50 * time.Millisecond) // read, not yet quiet
	for _, part := range []string{"\x1b", "[3;1R"} {
		if err := s.InputWaiting(t.Context(), nater, []byte(part)); err != nil {
			t.Fatal(err)
		}
		nextWrite(t, p)
	}
	update := waitAttention(t, s, func(a Attention) bool {
		return a.Source == SourceTrust && strings.Contains(a.Message, "Update available")
	})
	time.Sleep(2 * patternQuiet)
	if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
		t.Fatalf("the question was cleared: %+v", a)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); !errors.Is(err, ErrTrustQuestion) {
		t.Fatalf("a submission at the question: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
}

// An Escape that ends a terminal's read at Codex's update question may be
// the Escape key that skips it: a reply to the question is refused, and
// it still holds a submission back while the screen shows it; once the
// program has moved on and the screen shows no startup question, it is
// withdrawn (no answer recorded: keys typed in the terminal answer no
// question) and never asked again. An Escape typed before the question
// was asked: once the screen shows no startup question, it is never asked.
func TestAnEscapeEndingATerminalsReadHoldsTheQuestionUntilItGoes(t *testing.T) {
	for _, asked := range []bool{true, false} {
		t.Run(fmt.Sprintf("asked %v", asked), func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(codexUpdateScreen))
			if asked {
				update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
				if err := s.InputWaiting(t.Context(), nater, []byte("\x1b")); err != nil {
					t.Fatal(err)
				}
				nextWrite(t, p)
				time.Sleep(2 * escWait)
				if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); !errors.Is(err, ErrQuestionAnswered) {
					t.Fatalf("a reply after the Escape: %v", err)
				}
				if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: jane}); !errors.Is(err, ErrTrustQuestion) {
					t.Fatalf("a submission while the screen shows the question: %v", err)
				}
			} else {
				time.Sleep(50 * time.Millisecond) // read, not yet quiet
				if err := s.InputWaiting(t.Context(), nater, []byte("\x1b")); err != nil {
					t.Fatal(err)
				}
				nextWrite(t, p)
			}
			noWrite(t, p, 100*time.Millisecond)
			// Codex took the Escape: its screen cleared, its prompt drawn.
			p.outW.Write([]byte("\x1b[2J\x1b[H\x1b[2;3H› Ask Codex to do anything"))
			if asked {
				if a := waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone }); a.Source != SourceScreen || s.Info().LastAnswer != nil {
					t.Fatalf("withdrawn %+v, answered %+v", a, s.Info().LastAnswer)
				}
			}
			time.Sleep(3 * patternQuiet)
			if a := s.Info().Attention; a.State != AttentionNone {
				t.Fatalf("a question the screen no longer shows was asked: %+v", a)
			}
		})
	}
}

// A look of the screen that took its text before the update question was
// drawn (a blank screen), its callback late, comes after the question is
// asked and held answered by an Escape that ends a terminal's read, the
// program having drawn more since: it decides nothing, the screen as it is
// now (the question still on it) does, and the question still holds a
// submission back. (Codex's review, sixteenth pass.)
func TestAStaleLookOfTheScreenNeverClearsAHeldStartupQuestion(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: append(startupQuestions(), approvalQuestions()...)}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.InputWaiting(t.Context(), nater, []byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	// The program redraws the footer: the question is still on the screen.
	fed := s.asked.Fed()
	p.outW.Write([]byte("\x1b[9;3H\x1b[1menter\x1b[22m continue"))
	deadline := time.Now().Add(3 * time.Second)
	for s.asked.Fed() == fed && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	// The late callback of a look that saw a blank screen.
	s.asked.fireMu.Lock()
	s.missAsked("")
	s.asked.fireMu.Unlock()
	if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
		t.Fatalf("a stale look cleared the question: %+v", a)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); !errors.Is(err, ErrTrustQuestion) {
		t.Fatalf("a submission at the question: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
}

// An Alt chord pressed in a page's terminal at Codex's update question
// (xterm sends Alt+z as ESC z, which Codex reads as one key and ignores)
// is no Escape: the question still shows, and holds a submission back,
// whose Enter would run npm install. A chord whose key alone answers it
// (Alt+3, Alt+Enter: a program may ignore the Alt) puts it behind, a reply
// refused, and clears nothing; one whose key does not (Alt+z) leaves it
// waiting for its reply. The same written as two writes, the ESC and then
// its key (a page's frames the program may read as one), but for Alt+z:
// its ESC alone may have been the Escape key, and puts the question
// behind. (Codex's review, seventeenth pass; twentieth, finding 1.)
func TestAnAltChordNeverClearsAStartupQuestion(t *testing.T) {
	for _, c := range []struct {
		chord string
		split bool
	}{{"\x1bz", false}, {"\x1b3", false}, {"\x1b\r", false}, {"\x1bz", true}, {"\x1b3", true}, {"\x1b\r", true}} {
		chord := c.chord
		t.Run(fmt.Sprintf("%q split %v", chord, c.split), func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(codexUpdateScreen))
			update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
			// Split, the ESC and its key come as two writes (a page's two
			// frames), which the program may read as one.
			parts := []string{chord}
			if c.split {
				parts = []string{chord[:1], chord[1:]}
			}
			for _, part := range parts {
				if err := s.Input(nater, []byte(part)); err != nil {
					t.Fatal(err)
				}
				nextWrite(t, p)
			}
			time.Sleep(100 * time.Millisecond)
			if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
				t.Fatalf("the chord cleared the question: %+v", a)
			}
			if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: jane}); !errors.Is(err, ErrTrustQuestion) {
				t.Fatalf("a submission at the question: %v", err)
			}
			noWrite(t, p, 100*time.Millisecond)
			err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3"))
			if chord == "\x1bz" && !c.split {
				if err != nil {
					t.Fatalf("the reply after Alt+z: %v", err)
				}
				if got := nextWrite(t, p); got != "3" {
					t.Fatalf("written %q", got)
				}
			} else if !errors.Is(err, ErrQuestionAnswered) {
				t.Fatalf("the reply after %q: %v", chord, err)
			}
		})
	}
}

// A page's Escape, a quick reply's "Skip this update", resolves the update
// question as it is written (the reply claimed it); a key pressed in its
// terminal consumes it (a second reply refused) until the screen shows
// the program moved on, when it is withdrawn with no answer recorded.
// Either way, while the screen still shows the question (the page's next
// frame can reach the program in the same read: ESC, then z, one Alt+z,
// which Codex ignores), a submission is held back, and a z after the
// Escape leaves it so. (Codex's review, eighteenth pass, finding 1; the
// question's claim rule, 2026-10-15.)
func TestAnEscapeFromAPageHoldsTheQuestionUntilItGoes(t *testing.T) {
	for _, reply := range []bool{true, false} {
		t.Run(fmt.Sprintf("reply %v", reply), func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(codexUpdateScreen))
			update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
			if reply {
				if err := s.AnswerInput(t.Context(), nater, update.ID, []byte("\x1b")); err != nil {
					t.Fatal(err)
				}
			} else if err := s.Input(nater, []byte("\x1b")); err != nil {
				t.Fatal(err)
			}
			nextWrite(t, p)
			if err := s.Input(nater, []byte("z")); err != nil {
				t.Fatal(err)
			}
			nextWrite(t, p)
			time.Sleep(2 * escWait)
			if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); !errors.Is(err, ErrQuestionAnswered) {
				t.Fatalf("a second reply: %v", err)
			}
			if a := s.Info(); reply && (a.Attention.State != AttentionNone || a.LastAnswer == nil || a.LastAnswer.ByName != "nater") {
				t.Fatalf("the page's Escape did not resolve the question: %+v %+v", a.Attention, a.LastAnswer)
			} else if !reply && (a.Attention.ID != update.ID || a.Attention.State != AttentionNeedsInput || a.LastAnswer != nil) {
				t.Fatalf("the terminal's Escape: %+v %+v", a.Attention, a.LastAnswer)
			}
			if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: jane}); !errors.Is(err, ErrTrustQuestion) {
				t.Fatalf("a submission while the screen shows the question: %v", err)
			}
			noWrite(t, p, 100*time.Millisecond)
			p.outW.Write([]byte("\x1b[2J\x1b[H\x1b[2;3H› Ask Codex to do anything"))
			waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone })
			if !reply && s.Info().LastAnswer != nil {
				t.Fatalf("the terminal's Escape was recorded as an answer: %+v", s.Info().LastAnswer)
			}
			// The program moved on: a line is typed.
			if res, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: jane}); err != nil || !res.Entered {
				t.Fatalf("a submission once the screen moved on: %+v %v", res, err)
			}
		})
	}
}

// Ctrl+M as a terminal with the kitty protocol sends it (ESC [ 1 0 9 ; 5 u),
// whole or cut, at Codex's update question: crossterm reads Ctrl and m,
// which the question ignores, never Enter, so the question still shows and
// holds a submission back, whose Enter would pick "Update now". (Codex's
// review, nineteenth pass, finding 1.)
func TestAnEncodedCtrlMIsNoEnter(t *testing.T) {
	const ctrlM = "\x1b[109;5u"
	for cut := 0; cut < len(ctrlM); cut++ {
		t.Run(fmt.Sprintf("cut at %d", cut), func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			p.outW.Write([]byte(codexUpdateScreen))
			update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
			parts := []string{ctrlM}
			if cut > 0 {
				parts = []string{ctrlM[:cut], ctrlM[cut:]}
			}
			for _, part := range parts {
				if err := s.InputWaiting(t.Context(), nater, []byte(part)); err != nil {
					t.Fatal(err)
				}
				nextWrite(t, p)
			}
			if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
				t.Fatalf("Ctrl+M cleared the question: %+v", a)
			}
			if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); !errors.Is(err, ErrTrustQuestion) {
				t.Fatalf("a submission at the question: %v", err)
			}
			noWrite(t, p, 100*time.Millisecond)
		})
	}
}

// A key with modifiers the protocol encodes and that reads as no other key
// (Shift+Enter, Ctrl+3) may be read by a program as its key alone (a
// handler that looks at the key, not its modifiers): at Codex's update
// question it puts the question behind, a reply refused, and clears
// nothing, a submission still held back.
func TestAModifiedEncodedKeyOnlyPutsTheQuestionBehind(t *testing.T) {
	for _, key := range []string{"\x1b[13;2u", "\x1b[51;5u"} {
		t.Run(fmt.Sprintf("%q", key), func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(codexUpdateScreen))
			update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
			if err := s.InputWaiting(t.Context(), nater, []byte(key)); err != nil {
				t.Fatal(err)
			}
			nextWrite(t, p)
			if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
				t.Fatalf("the key cleared the question: %+v", a)
			}
			if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); !errors.Is(err, ErrQuestionAnswered) {
				t.Fatalf("a reply after the key: %v", err)
			}
			if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: jane}); !errors.Is(err, ErrTrustQuestion) {
				t.Fatalf("a submission at the question: %v", err)
			}
			noWrite(t, p, 100*time.Millisecond)
		})
	}
}

// Codex's update question answered (3), drawn again before Codex took the
// key, then left for Codex's prompt with no other startup question: asked
// again answered, it goes once the screen shows no startup question, and a
// submission is typed then. (Codex's review, nineteenth pass, finding 2.)
func TestAStartupQuestionAskedAgainAnsweredGoesWithTheScreen(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.AnswerInput(t.Context(), nater, update.ID, []byte("3")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexUpdateScreen))
	again := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust && a.ID != update.ID })
	if err := s.AnswerInput(t.Context(), jane, again.ID, []byte("3")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply to the question asked again: %v", err)
	}
	p.outW.Write([]byte("\x1b[2J\x1b[H\x1b[2;3H› Ask Codex to do anything"))
	waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone })
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: jane}); err != nil {
		t.Fatalf("a submission once the question has gone: %v", err)
	}
}

// In a terminal 120 rows tall, Codex's update question at its top and a
// footer at its bottom: more than the 80 rows a watcher looks at apart.
// Held by an Escape, the question is not taken for gone when the footer
// is drawn again: the whole screen still shows it, and a submission is
// still held back. (Codex's review, twenty-first pass.)
func TestAHeldStartupQuestionAboveTheLastRowsIsNotGone(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 120}, newChanSink(false))
	p.outW.Write([]byte(codexUpdateScreen + "\x1b[120;1Hstatus: ready"))
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.Input(nater, []byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	p.outW.Write([]byte("\x1b[120;1Hstatus: still ready"))
	time.Sleep(2 * patternQuiet)
	if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
		t.Fatalf("a question on the screen was taken for gone: %+v", a)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); !errors.Is(err, ErrTrustQuestion) {
		t.Fatalf("a submission at the question: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
}

// Codex redraws in a synchronized update (ESC [ ? 2 0 2 6 h, then l), which a
// terminal shows only once it ends: a redraw paused right after its erase
// leaves the screen this session reads blank, the terminal still showing
// the update question. Held by an Escape, the question is not taken for
// gone on half a frame: a submission is still held back, and the redraw,
// ended with the question, leaves it held; a frame without it lets it go.
// (Codex's review, twenty-second pass, finding 1.)
func TestAHeldStartupQuestionIsNotGoneOnHalfAFrame(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	for _, part := range []string{"\x1b", "z"} {
		if err := s.Input(nater, []byte(part)); err != nil {
			t.Fatal(err)
		}
		nextWrite(t, p)
	}
	p.outW.Write([]byte("\x1b[?2026h\x1b[2J"))
	time.Sleep(2 * patternQuiet)
	if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
		t.Fatalf("half a frame cleared the question: %+v", a)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); !errors.Is(err, ErrTrustQuestion) {
		t.Fatalf("a submission at the question: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
	p.outW.Write([]byte("\x1b[H" + strings.TrimPrefix(codexUpdateScreen, "\x1b[?2026h")))
	time.Sleep(2 * patternQuiet)
	if a := s.Info().Attention; a.State != AttentionNeedsInput || a.Source != SourceTrust {
		t.Fatalf("the question drawn again was cleared: %+v", a)
	}
	p.outW.Write([]byte("\x1b[?2026h\x1b[2J\x1b[H\x1b[2;3H› Ask Codex to do anything\x1b[?2026l"))
	waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone })
}

// A keypad key as a terminal with the kitty protocol sends it (KP_1 is
// ESC [ 5 7 4 0 0 u), which Codex's crossterm reads as its digit: a keypad 1
// answers the background-server question, so a 1 queued behind it naming
// that question is refused, nothing more written; a keypad 3 answers the
// update question and clears it. (Codex's review, twenty-third pass,
// finding 2.)
func TestAKeypadKeyAnswersAsItsDigit(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	release := blockFirstWrite(p)
	defer release()
	go s.Input(nater, []byte("\x1b[B"))
	writing(t, s)
	typed := make(chan error, 1)
	go func() { typed <- s.InputWaiting(t.Context(), nater, []byte("\x1b[57400u")) }()
	queued(t, s, 1)
	replied := make(chan error, 1)
	go func() { replied <- s.AnswerInput(t.Context(), jane, first.ID, []byte("1")) }()
	queued(t, s, 2)
	release()
	nextWrite(t, p)
	if got := nextWrite(t, p); got != "\x1b[57400u" {
		t.Fatalf("written %q", got)
	}
	if err := <-typed; err != nil {
		t.Fatal(err)
	}
	if err := <-replied; !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("the 1 after the keypad 1: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexUpdateScreen))
	waitAttention(t, s, func(a Attention) bool {
		return a.Source == SourceTrust && strings.Contains(a.Message, "Update available")
	})
	if err := s.InputWaiting(t.Context(), nater, []byte("\x1b[57402u")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	// Consumed: withdrawn once the screen shows the program moved on.
	if err := s.AnswerInput(t.Context(), jane, s.Info().Attention.ID, []byte("3")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply after the keypad 3: %v", err)
	}
	p.outW.Write([]byte("\x1b[2J\x1b[H\x1b[2;3H› Ask Codex to do anything"))
	waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone })
}

// A terminal made narrower clips what the screen showed: Codex's update
// question, held by an Escape, cut to "› 1. Update now" no longer reads as
// itself, but it is not gone (Codex redraws it at the new size): a look
// before that redraw takes nothing for gone, and a submission is still
// held back. (Codex's review, twenty-third pass, finding 1.)
func TestAClippedStartupQuestionIsNotGone(t *testing.T) {
	// After the resize, before a look: nothing; the end of a synchronized
	// update begun before it, which draws nothing at the new size; an
	// empty frame; a redraw at the new size that cuts the question short
	// (Codex's review, twenty-fourth pass, finding 1; twenty-fifth); that
	// redraw, then another split Escape, then output (twenty-sixth,
	// finding 2); that redraw, then the terminal made as wide as before,
	// nothing drawn since (twenty-sixth, finding 1). Made as wide as it
	// was again, the screen showing Codex's prompt, the question goes.
	cut := "\x1b[?2026h\x1b[2J\x1b[H› 1. Update now\x1b[?2026l"
	for _, c := range []struct {
		name, before, after string
		again, widen        bool
	}{
		{"nothing", "", "", false, false},
		{"a frame begun before", "\x1b[?2026h", "\x1b[?2026l", false, false},
		{"an empty frame", "", "\x1b[?2026h\x1b[?2026l", false, false},
		{"a redraw that cuts it", "", cut, false, false},
		{"a redraw that cuts it, then another Escape", "", cut, true, false},
		{"a redraw that cuts it, then as wide again", "", cut, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			p.outW.Write([]byte(codexUpdateScreen))
			update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
			for _, part := range []string{"\x1b", "z"} {
				if err := s.Input(nater, []byte(part)); err != nil {
					t.Fatal(err)
				}
				nextWrite(t, p)
			}
			p.outW.Write([]byte(c.before + "\x1b[24;1Hstatus: ready"))
			fed := s.asked.Fed()
			deadline := time.Now().Add(3 * time.Second)
			for s.asked.Fed() == fed && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if err := s.ResizeWith(nater, 16, 24, true); err != nil {
				t.Fatal(err)
			}
			if c.after != "" {
				p.outW.Write([]byte(c.after))
			}
			if c.again {
				time.Sleep(100 * time.Millisecond)
				for _, part := range []string{"\x1b", "z"} {
					if err := s.Input(nater, []byte(part)); err != nil {
						t.Fatal(err)
					}
					nextWrite(t, p)
				}
				p.outW.Write([]byte("\x1b[24;1Hstatus: ready"))
			}
			if c.widen {
				time.Sleep(100 * time.Millisecond)
				if err := s.ResizeWith(nater, 80, 24, true); err != nil {
					t.Fatal(err)
				}
				p.outW.Write([]byte("\x1b[24;1Hstatus: ready"))
			}
			time.Sleep(2 * patternQuiet)
			if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
				t.Fatalf("a clipped question was taken for gone: %+v", a)
			}
			if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); !errors.Is(err, ErrTrustQuestion) {
				t.Fatalf("a submission at the question: %v", err)
			}
			noWrite(t, p, 100*time.Millisecond)
			if err := s.ResizeWith(nater, 80, 24, true); err != nil {
				t.Fatal(err)
			}
			p.outW.Write([]byte("\x1b[?2026h\x1b[2J\x1b[H\x1b[2;3H› Ask Codex to do anything\x1b[?2026l"))
			waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone })
		})
	}
}

// A resize in the middle of a synchronized frame that has erased the
// screen (blank for the moment): the frame's erase was made at the size
// before, and its end, drawing the question cut short at that size,
// settles nothing; the held question is not taken for gone. (Codex's
// review, twenty-seventh pass.)
func TestAResizeInsideAFrameSettlesNothing(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	for _, part := range []string{"\x1b", "z"} {
		if err := s.Input(nater, []byte(part)); err != nil {
			t.Fatal(err)
		}
		nextWrite(t, p)
	}
	fedNow := func() uint64 { return s.asked.Fed() }
	feed := func(out string) {
		t.Helper()
		fed := fedNow()
		p.outW.Write([]byte(out))
		deadline := time.Now().Add(3 * time.Second)
		for fedNow() == fed && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := s.ResizeWith(nater, 16, 24, true); err != nil {
		t.Fatal(err)
	}
	feed("\x1b[?2026h\x1b[2J\x1b[H")
	if err := s.ResizeWith(nater, 80, 24, true); err != nil {
		t.Fatal(err)
	}
	feed("› 1. Update now\x1b[?2026l")
	time.Sleep(2 * patternQuiet)
	if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
		t.Fatalf("a frame begun at another size settled the screen: %+v", a)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); !errors.Is(err, ErrTrustQuestion) {
		t.Fatalf("a submission at the question: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
}

// The terminal widened while Codex's update question shows, the question
// answered by an Escape, then Codex's frame that clears the screen from its
// first cell (ESC [ H, ESC [ J: erase to the end, as Codex clears) and
// draws its prompt: the screen is erased whole, so the question goes, and
// a submission is typed. (Codex's review, twenty-eighth pass.)
func TestAnEraseFromTheFirstCellSettlesAResizedScreen(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexUpdateScreen))
	waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.ResizeWith(nater, 100, 24, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Input(nater, []byte("\x1b")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	p.outW.Write([]byte("\x1b[?2026h\x1b[H\x1b[J\x1b[2;3H› Ask Codex to do anything\x1b[?2026l"))
	waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone })
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); err != nil {
		t.Fatalf("a submission once the question has gone: %v", err)
	}
}

// Codex draws its update question, then discards what was typed before it
// took input: a 3 typed as the question is drawn, before the session asks
// it, may never be read. The question is still asked, held answered (a
// reply to it refused), and holds a submission back, whose Enter would run
// the update; the screen showing Codex's prompt lets it go. (Codex's review,
// twenty-ninth pass, finding 1.)
func TestAKeyBeforeAStartupQuestionIsAskedClearsNothing(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(codexUpdateScreen))
	time.Sleep(50 * time.Millisecond) // read, not yet quiet
	if err := s.InputWaiting(t.Context(), nater, []byte("3")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply to the question held: %v", err)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: jane}); !errors.Is(err, ErrTrustQuestion) {
		t.Fatalf("a submission at the question: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
	p.outW.Write([]byte("\x1b[?2026h\x1b[2J\x1b[H\x1b[2;3H› Ask Codex to do anything\x1b[?2026l"))
	waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNone })
}

// Codex's background-server question asked, then its update question drawn
// and not asked yet: a 3 (one of the background-server question's choices)
// reaches the update question, which may discard it. It answers neither
// surely: the background-server question gives way to the update question,
// asked held answered, and a submission is held back all along. (Codex's
// review, thirtieth pass, finding 1.)
func TestAKeyAtANewerStartupQuestionAnswersNotTheOlder(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexUpdateScreen))
	time.Sleep(50 * time.Millisecond) // read, not yet quiet
	if err := s.InputWaiting(t.Context(), nater, []byte("3")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if a := s.Info().Attention; a.ID != first.ID || a.State != AttentionNeedsInput {
		t.Fatalf("the older question was cleared before the newer was asked: %+v", a)
	}
	update := waitAttention(t, s, func(a Attention) bool {
		return a.Source == SourceTrust && strings.Contains(a.Message, "Update available")
	})
	if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply to the update question held: %v", err)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: jane}); !errors.Is(err, ErrTrustQuestion) {
		t.Fatalf("a submission at the question: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
}

// A key held down at Codex's trust question: the protocol's repeat of Enter
// (ESC [ 1 3 ; 1 : 2 u), which Codex's trust question ignores, puts it
// behind (a program may take a repeat) but clears nothing: the question
// still holds a submission back, whose Enter would grant trust. (Codex's
// review, twenty-fourth pass, finding 2.)
func TestARepeatedKeyOnlyPutsTheQuestionBehind(t *testing.T) {
	trust := []Option{{Label: "Yes, trust this folder", Input: "\r"}, {Label: "No, quit", Input: "\x1b[B\r"}}
	s, p := newLocalWith(t, quiet(Options{TrustPattern: regexp.MustCompile(`Trust\s*this\s*folder\?`), TrustAnswers: trust}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte("Trust this folder?\r\n› 1. Yes, continue\r\n  2. No, quit\r\n"))
	q := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.InputWaiting(t.Context(), nater, []byte("\x1b[13;1:2u")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if a := s.Info().Attention; a.ID != q.ID || a.State != AttentionNeedsInput {
		t.Fatalf("a repeat cleared the question: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("\r")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply after the repeat: %v", err)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: jane}); !errors.Is(err, ErrTrustQuestion) {
		t.Fatalf("a submission at the question: %v", err)
	}
	noWrite(t, p, 100*time.Millisecond)
}

// An X10 mouse report (ESC [ M and three bytes: the button and the cell),
// a click in a terminal at Codex's update question, whole or cut anywhere:
// its bytes are no keys (a column of 1 is the byte '1', which would pick
// "Update now"), and the question still holds a submission back. (Codex's
// review, eighteenth pass, finding 2.)
func TestAMouseReportAnswersNoStartupQuestion(t *testing.T) {
	const click = "\x1b[M 1!"
	for cut := 0; cut < len(click); cut++ {
		t.Run(fmt.Sprintf("cut at %d", cut), func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			p.outW.Write([]byte(codexUpdateScreen))
			update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
			parts := []string{click}
			if cut > 0 {
				parts = []string{click[:cut], click[cut:]}
			}
			for _, part := range parts {
				if err := s.InputWaiting(t.Context(), nater, []byte(part)); err != nil {
					t.Fatal(err)
				}
				nextWrite(t, p)
			}
			if a := s.Info().Attention; a.ID != update.ID || a.State != AttentionNeedsInput {
				t.Fatalf("the click cleared the question: %+v", a)
			}
			// Cut right after its ESC, the ESC alone may have been the
			// Escape key, and puts the question behind; nothing else does.
			if err := s.questionWaits(update.ID); err != nil && cut != 1 {
				t.Fatalf("the click put the question behind: %v", err)
			}
			if _, err := s.Submit(t.Context(), Submission{Text: "fix the bug", By: nater}); !errors.Is(err, ErrTrustQuestion) {
				t.Fatalf("a submission at the question: %v", err)
			}
			noWrite(t, p, 100*time.Millisecond)
		})
	}
}

// A startup question the program left without an answer read as one (it
// moved on by itself, or took a key not read) gives way to the next drawn
// after it: the update question is asked with an id of its own and takes
// its own reply, and a reply naming the question left is refused.
// (Codex's review, thirteenth pass, finding 2.)
func TestAStartupQuestionTheScreenLeftGivesWayToTheNext(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	p.outW.Write([]byte("\x1b[2J\x1b[H" + codexUpdateScreen))
	update := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust && a.ID != first.ID })
	if !strings.Contains(update.Message, "Update available") {
		t.Fatalf("the question asked: %+v", update)
	}
	if err := s.AnswerInput(t.Context(), jane, first.ID, []byte("1")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply to the question left: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
	if err := s.AnswerInput(t.Context(), jane, update.ID, []byte("3")); err != nil {
		t.Fatalf("the update question's own answer: %v", err)
	}
	if got := nextWrite(t, p); got != "3" {
		t.Fatalf("written %q", got)
	}
}

// Two startup questions one pattern finds (a catalog's own, with no words
// pattern): the output the watcher keeps holds both once the second is
// drawn, and the question asked is the one drawn last, by its words, not
// the first. A reply to the first, gone by a key Conductor does not read
// as an answer, is refused, nothing written; the second takes its own.
func TestTwoStartupQuestionsOfOnePatternAreToldByTheLastDrawn(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: []StartupQuestion{{Pattern: regexp.MustCompile(`Confirm (?:read|delete)\?`), Answers: []Option{{Label: "Yes", Input: "y"}}}}}))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte("Confirm read?\r\n"))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if first.Message != "Confirm read?" {
		t.Fatalf("the first question asked: %+v", first)
	}
	p.outW.Write([]byte("\x1b[2J\x1b[HConfirm delete?\r\n"))
	second := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust && a.ID != first.ID })
	if second.Message != "Confirm delete?" {
		t.Fatalf("the second question asked: %+v", second)
	}
	if err := s.AnswerInput(t.Context(), jane, first.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply to the first: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
	if err := s.AnswerInput(t.Context(), jane, second.ID, []byte("y")); err != nil {
		t.Fatalf("the second's own answer: %v", err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
}

// A startup question a person dealt with by a key Conductor does not offer
// (an `n` its choices leave out) stays shown, for nothing told it gone; the
// program draws another screen, which shows no startup question, and the
// output the watcher keeps still holds the question. A reply to it then is
// refused, nothing written: it would reach the screen that took its place.
func TestAReplyToAStartupQuestionGoneFromTheScreenIsRefused(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: []StartupQuestion{{Pattern: regexp.MustCompile(`Confirm read\?`), Answers: []Option{{Label: "Yes", Input: "y"}}}}}))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane", Cols: 80, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte("Confirm read? [y/n]\r\n"))
	q := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if err := s.Input(jane, []byte("n")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "n" {
		t.Fatalf("written %q", got)
	}
	p.outW.Write([]byte("\x1b[2J\x1b[H$ ready\r\n"))
	deadline := time.Now().Add(3 * time.Second)
	for {
		text, settled, _, _ := s.asked.Whole()
		if settled && strings.Contains(text, "$ ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the screen never showed the prompt: %q", text)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a reply to the question the screen left: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// Two approvals in a row whose commands are alike for longer than a
// message can hold, and differ after it: each is a question of its own.
// The second, drawn over the first once that was answered (the queue
// advanced in place, no quiet screen between), is asked with an id of its
// own, and a click on it is taken; on the cut words alone it was the first
// still, answered, and every reply to it was refused.
func TestTwoLongApprovalsAlikeUpToTheCutAreTwoQuestions(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 500, Rows: 30}, newChanSink(false))
	head := "echo " + strings.Repeat("abcdefghij", 46) + " && rm -rf /tmp/"
	p.outW.Write([]byte(approvalDialog(head + "a")))
	first := waitAttention(t, s, asked)
	if len(first.Message) > MaxAttentionMessage || !strings.Contains(first.Message, "…") || strings.Contains(first.Message, "/tmp/") {
		t.Fatalf("the first approval's words are not cut before the commands differ (%d bytes): %q", len(first.Message), first.Message)
	}
	if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
	again := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Since.After(*first.Since) })
	p.outW.Write([]byte(approvalNext(head + "b")))
	// The message shown is the same (cut before the commands differ); the question is not.
	next := waitAttention(t, s, func(a Attention) bool {
		return asked(a) && a.ID != first.ID && a.ID != again.ID
	})
	if err := s.AnswerInput(t.Context(), nater, next.ID, []byte("y")); err != nil {
		t.Fatalf("a click on the second approval: %v", err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
}

// On a screen narrower than a startup question's heading, autowrap cuts a
// word of it at the screen's edge ("featu" and "re settings"): the question
// is still on the screen, and a reply to it is taken. Its rows are read as
// the one line the program wrote (Screen.Lines), never as two words.
func TestAReplyToAStartupQuestionAutowrapCutIsTaken(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane", Cols: 40, Rows: 24}, newChanSink(false))
	p.outW.Write([]byte(codexBackgroundScreen))
	first := waitAttention(t, s, func(a Attention) bool { return a.Source == SourceTrust })
	if text, settled, _, _ := s.asked.Whole(); !settled || !strings.Contains(text, "incompatible feature settings") {
		t.Fatalf("the screen as read for a reply: %q (settled %v)", text, settled)
	}
	if err := s.AnswerInput(t.Context(), jane, first.ID, []byte("1")); err != nil {
		t.Fatalf("a reply to the question on a narrow screen: %v", err)
	}
	if got := nextWrite(t, p); got != "1" {
		t.Fatalf("written %q", got)
	}
}

// An agent's question reported with a choice whose key is a digit (`1`),
// and a dialog or a startup question drawn on the screen before the
// session has asked it (a watcher asks once the screen is quiet): a reply
// to the report is refused, nothing written, for its `1` would reach what
// the screen shows now (Codex's approval, its first line "Yes, proceed").
func TestAReplyToAReportIsRefusedOnceTheScreenDrawsAQuestion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		questions []StartupQuestion
		screen    string
		drawn     string
	}{
		{"an approval", approvalQuestions(), approvalDialog("rm -rf build"), "Would you like to run"},
		{"a startup question", startupQuestions(), codexBackgroundScreen, "Background server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: tc.questions}))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane", Cols: 80, Rows: 24}, newChanSink(false))
			if err := s.TrySetAttentionFull(AttentionNeedsInput, "Which one?", SourceAPI, "", []Option{{Label: "One", Input: "1"}, {Label: "Two", Input: "2"}}); err != nil {
				t.Fatal(err)
			}
			q := s.Info().Attention
			p.outW.Write([]byte(tc.screen))
			deadline := time.Now().Add(3 * time.Second)
			for !strings.Contains(s.asked.Text(), tc.drawn) {
				if time.Now().After(deadline) {
					t.Fatalf("the screen never showed it: %q", s.asked.Text())
				}
				time.Sleep(2 * time.Millisecond)
			}
			// Read, not yet quiet (the watcher looks after patternQuiet): the report is still what the session shows.
			if a := s.Info().Attention; a.ID != q.ID {
				t.Fatalf("the watcher asked before the reply: %+v", a)
			}
			if err := s.AnswerInput(t.Context(), jane, q.ID, []byte("1")); !errors.Is(err, ErrQuestionAnswered) {
				t.Fatalf("a 1 for the report once the screen drew a question: %v", err)
			}
			noWrite(t, p, 200*time.Millisecond)
		})
	}
}

// A line that replies to an agent's report, with a startup question drawn
// on the screen (never asked: the report waiting keeps it from being): the
// line is refused, nothing written, for its `1` would pick the startup
// question's first answer; drawn in the line's pause, after its text, the
// line's Enter is left out.
func TestALineReplyingToAReportIsRefusedOnceTheScreenDrawsAStartupQuestion(t *testing.T) {
	t.Run("before its text", func(t *testing.T) {
		s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions()}))
		jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane", Cols: 80, Rows: 24}, newChanSink(false))
		if err := s.TrySetAttentionFull(AttentionNeedsInput, "Which name?", SourceAPI, "", nil); err != nil {
			t.Fatal(err)
		}
		q := s.Info().Attention
		p.outW.Write([]byte(codexBackgroundScreen))
		deadline := time.Now().Add(3 * time.Second)
		for !strings.Contains(s.asked.Text(), "Background server") {
			if time.Now().After(deadline) {
				t.Fatalf("the screen never showed it: %q", s.asked.Text())
			}
			time.Sleep(2 * time.Millisecond)
		}
		// The screen shows a startup question: nothing is typed into it.
		res, err := s.Submit(t.Context(), Submission{Text: "1", By: jane, Question: q.ID})
		if !errors.Is(err, ErrTrustQuestion) || res.Typed {
			t.Fatalf("a line naming the report: %+v, %v", res, err)
		}
		noWrite(t, p, 200*time.Millisecond)
		if a := s.Info().Attention; a.ID != q.ID {
			t.Fatalf("attention %+v, want the report still", a)
		}
	})
	t.Run("in its pause", func(t *testing.T) {
		s, p := newLocalWith(t, quiet(Options{Questions: startupQuestions(), SubmitPause: 300 * time.Millisecond}))
		jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane", Cols: 80, Rows: 24}, newChanSink(false))
		if err := s.TrySetAttentionFull(AttentionNeedsInput, "Which name?", SourceAPI, "", nil); err != nil {
			t.Fatal(err)
		}
		q := s.Info().Attention
		done := submitAsync(s, Submission{Text: "main", By: jane, Question: q.ID})
		if got := nextWrite(t, p); got != "main" {
			t.Fatalf("written %q", got)
		}
		p.outW.Write([]byte(codexBackgroundScreen))
		// Stranded: its claim let go, the turn held for a person.
		got := returnsWithin(t, 5*time.Second, "the line", func() submitted { return <-done })
		if !errors.Is(got.err, ErrLineStranded) || !got.res.Typed || got.res.Entered {
			t.Fatalf("the line: %+v, %v; want typed, its Enter left out, stranded", got.res, got.err)
		}
		noWrite(t, p, 200*time.Millisecond)
		if !s.Held() {
			t.Fatal("the turn is not held for the stranded line")
		}
	})
}

// approvalWrapped is approvalDialog with its command on rows of its own, as
// Codex lays out a long one at the screen's width: each row a cut of it, a
// word cut where it meets the edge.
func approvalWrapped(rows ...string) string {
	var b strings.Builder
	b.WriteString("\x1b[?2026h\x1b[12;1H\x1b[J\x1b[13;3H\x1b[1mWould you like to run the following command?\x1b[22m\x1b[15;3HEnvironment: local")
	r := 17
	for i, row := range rows {
		lead := "  "
		if i == 0 {
			lead = "$ "
		}
		fmt.Fprintf(&b, "\x1b[%d;3H%s%s", r, lead, row)
		r++
	}
	fmt.Fprintf(&b, "\x1b[%d;1H› 1. Yes, proceed (y)\x1b[%d;3H2. No, and tell Codex what to do differently (esc)\x1b[%d;3HPress enter to confirm or esc to cancel\x1b[?2026l", r+1, r+2, r+4)
	return b.String()
}

// An approval answered, drawn again at another width (the window resized):
// its command's rows break elsewhere, a long word cut at another place, and
// it is the same question still, answered: a click on it is refused,
// nothing written. Told by its words as the rows read, it was a question
// of its own, and took a second answer.
func TestAnApprovalDrawnAgainAtAnotherWidthIsTheSameQuestion(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	long := "curl https://example.com/" + strings.Repeat("a", 90)
	p.outW.Write([]byte(approvalWrapped(long[:70], long[70:])))
	first := waitAttention(t, s, asked)
	if !strings.Contains(first.Message, long[:70]+" "+long[70:]) {
		t.Fatalf("the approval's words: %q", first.Message)
	}
	if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
	waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Since.After(*first.Since) })
	// The window narrower: the program erases its screen and draws it again at 70 columns.
	if err := s.Resize(nater, 70, 24); err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte("\x1b[H\x1b[2J" + approvalWrapped(long[:60], long[60:])))
	time.Sleep(patternQuiet + 200*time.Millisecond)
	a := s.Info().Attention
	if !asked(a) {
		t.Fatalf("the approval drawn again is not asked: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, a.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a click on the approval drawn again at another width: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// Two approvals in a row at one width, one command on two rows (a line
// break of its own: `printf '%s' 'build` and `output'`) and the next on
// one (`printf '%s' 'buildoutput'`): the program broke the first's rows,
// not the width, and they are two questions. The second, drawn over the
// first, answered or not, is asked with an id of its own and takes a
// click; a reply naming the first is refused.
func TestARowBreakOfTheProgramsOwnTellsTwoApprovalsApart(t *testing.T) {
	for _, answerFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("the first answered %v", answerFirst), func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(approvalWrapped(`printf '%s' 'build`, `output'`)))
			first := waitAttention(t, s, asked)
			seen := first
			if answerFirst {
				if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("y")); err != nil {
					t.Fatal(err)
				}
				if got := nextWrite(t, p); got != "y" {
					t.Fatalf("written %q", got)
				}
				seen = waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Since.After(*first.Since) })
			}
			p.outW.Write([]byte(approvalWrapped(`printf '%s' 'buildoutput'`)))
			next := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.ID != first.ID && a.ID != seen.ID })
			if err := s.AnswerInput(t.Context(), jane, first.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
				t.Fatalf("a reply naming the first: %v", err)
			}
			noWrite(t, p, 200*time.Millisecond)
			if err := s.AnswerInput(t.Context(), jane, next.ID, []byte("y")); err != nil {
				t.Fatalf("a click on the second: %v", err)
			}
			if got := nextWrite(t, p); got != "y" {
				t.Fatalf("written %q", got)
			}
		})
	}
}

// An approval answered, then a synchronized redraw begun (its screen
// erased) and left unfinished past the watcher's quiet period: half a
// frame, which forgets no dialog answered. The frame finished with the same
// approval, it is still answered: a click on it is refused, nothing
// written.
func TestHalfAFrameForgetsNoApprovalAnswered(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	first := waitAttention(t, s, asked)
	if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
	waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Since.After(*first.Since) })
	p.outW.Write([]byte("\x1b[?2026h\x1b[2J"))
	time.Sleep(patternQuiet + 300*time.Millisecond)
	// The rest of the frame: the same approval (approvalDialog begins a synchronized update of its own and ends it).
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com") + "\x1b[?2026l"))
	time.Sleep(patternQuiet + 300*time.Millisecond)
	a := s.Info().Attention
	if !asked(a) {
		t.Fatalf("the approval is not asked: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, a.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a click on the approval the frame finished with: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
}

// Two approvals in a row whose commands differ by white space within a row
// (`"./build output"`, and `"./buildoutput"` or `"./build  output"`) are
// two questions: the second, drawn over the first, is asked with an id of
// its own, which a click on it takes, answered first or not, and a reply
// naming the first is refused, nothing written. (The message shown may be
// the same: every run of white space shows as one space.)
func TestTwoApprovalsThatDifferByASpaceAreTwoQuestions(t *testing.T) {
	for _, tc := range []struct {
		second      string
		answerFirst bool
	}{
		{`rm -rf "./buildoutput"`, false},
		{`rm -rf "./buildoutput"`, true},
		{`rm -rf "./build  output"`, false},
		{`rm -rf "./build  output"`, true},
	} {
		t.Run(fmt.Sprintf("%s, the first answered %v", tc.second, tc.answerFirst), func(t *testing.T) {
			answerFirst := tc.answerFirst
			s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(approvalDialog(`rm -rf "./build output"`)))
			first := waitAttention(t, s, asked)
			seen := first
			if answerFirst {
				if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("y")); err != nil {
					t.Fatal(err)
				}
				if got := nextWrite(t, p); got != "y" {
					t.Fatalf("written %q", got)
				}
				seen = waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Since.After(*first.Since) })
			}
			p.outW.Write([]byte(approvalNext(tc.second)))
			next := waitAttention(t, s, func(a Attention) bool {
				return asked(a) && a.ID != first.ID && a.ID != seen.ID
			})
			if err := s.AnswerInput(t.Context(), jane, first.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
				t.Fatalf("a reply naming the first: %v", err)
			}
			noWrite(t, p, 200*time.Millisecond)
			if err := s.AnswerInput(t.Context(), jane, next.ID, []byte("y")); err != nil {
				t.Fatalf("a click on the second: %v", err)
			}
			if got := nextWrite(t, p); got != "y" {
				t.Fatalf("written %q", got)
			}
		})
	}
}

// A reply's keys (AnswerInput) waiting behind another's write are
// withdrawn once the link's credential no longer stands, asked again as
// the writer takes them (AttachOptions.Authorize: a revoke, or on a host
// the server not heard from lately), the connection still open: as a
// submission's text is, and a keystroke's is not. Nothing of them is
// written when the program reads again, the question still waits with its
// id, and another controller's reply to it is written.
func TestAReplyWaitingBehindAnothersWriteIsWithdrawnOnceItsLinkNoLongerStands(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 10 * time.Millisecond})
	takeAgain := stopTaking(t, p)
	other := ownerOf(t, s)
	go s.Input(other, []byte("other's"))
	untilWaiting(t, s, 0)
	s.SetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "3"}})
	q := s.Info().Attention.ID
	var linkRevoked atomic.Bool
	guest := linkedController(t, s, newChanSink(false), &linkRevoked)
	done := make(chan error, 1)
	go func() { done <- s.AnswerInput(t.Context(), guest, q, []byte("1")) }()
	untilWaiting(t, s, 1)
	linkRevoked.Store(true)
	takeAgain()
	if got := written(t, p); got != "other's" {
		t.Fatalf("written %q", got)
	}
	if err := returnsWithin(t, 5*time.Second, "the reply", func() error { return <-done }); !errors.Is(err, ErrRevoked) {
		t.Fatalf("the reply of a link that no longer stands: %v", err)
	}
	nothingWritten(t, p, 300*time.Millisecond)
	if a := s.Info().Attention; a.ID != q || a.State != AttentionNeedsInput {
		t.Fatalf("the question after the withdrawal: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), other, q, []byte("1")); err != nil {
		t.Fatalf("the owner's reply: %v", err)
	}
	if got := written(t, p); got != "1" {
		t.Fatalf("written %q", got)
	}
}

// An approval whose command the program broke on two rows (`printf '%s'
// 'build` and `output'`), the window then made wider and the approval
// drawn again at that width (the same question, its id kept), answered
// first or not; then the next approval, `printf '%s' 'buildoutput'`, at
// the new width. The first is told by its rows at the new width from the
// redraw on, so the row break is the program's own against the second:
// two questions, the second asked with an id of its own, a reply naming
// the first refused, a click on the second written. Before the redraw
// refreshed the rows kept, they stayed at the old width, the two were
// compared across widths (a row break read as nothing), and the second
// kept the first's id: a stale reply to the first answered it, or,
// answered first, it was never asked with an id of its own.
func TestAnApprovalDrawnAgainAtAnotherWidthIsToldByItsRowsThere(t *testing.T) {
	for _, answerFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("the first answered %v", answerFirst), func(t *testing.T) {
			s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
			nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
			jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
			p.outW.Write([]byte(approvalWrapped(`printf '%s' 'build`, `output'`)))
			first := waitAttention(t, s, asked)
			seen := first
			if answerFirst {
				if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("y")); err != nil {
					t.Fatal(err)
				}
				if got := nextWrite(t, p); got != "y" {
					t.Fatalf("written %q", got)
				}
				seen = waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Since.After(*first.Since) })
			}
			// Wider: the program erases its screen and draws the same rows at 100 columns.
			if err := s.Resize(nater, 100, 24); err != nil {
				t.Fatal(err)
			}
			p.outW.Write([]byte("\x1b[H\x1b[2J" + approvalWrapped(`printf '%s' 'build`, `output'`)))
			time.Sleep(patternQuiet + 200*time.Millisecond)
			if a := s.Info().Attention; !asked(a) || a.ID != seen.ID {
				t.Fatalf("drawn again at another width: %+v, want the id %s", a, seen.ID)
			}
			p.outW.Write([]byte(approvalWrapped(`printf '%s' 'buildoutput'`)))
			next := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.ID != first.ID && a.ID != seen.ID })
			if err := s.AnswerInput(t.Context(), jane, first.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
				t.Fatalf("a reply naming the first: %v", err)
			}
			noWrite(t, p, 200*time.Millisecond)
			if err := s.AnswerInput(t.Context(), jane, next.ID, []byte("y")); err != nil {
				t.Fatalf("a click on the second: %v", err)
			}
			if got := nextWrite(t, p); got != "y" {
				t.Fatalf("written %q", got)
			}
		})
	}
}

// An approval answered, then a synchronized redraw begun (its screen
// erased) and left unfinished past the watcher's quiet period, so that a
// look takes the blank half frame; the rest of the frame, the same
// approval and the frame's end, lands after that look took its text and
// before its callback ran (the watcher's afterLook). The callback decides
// on the half frame it saw, not on the whole one the screen shows by
// then: the dialog answered is kept, the approval is asked answered once
// the screen is looked at again, and a click on it is refused, nothing
// written. Before the look carried its own frame state, the callback read
// the screen's, took its blank text for a whole frame, and forgot the
// dialog answered.
func TestAFrameFinishedAfterTheLookForgetsNoApprovalAnswered(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	nater, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater", Cols: 80, Rows: 24}, newChanSink(false))
	jane, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "jane"}, newChanSink(false))
	dialog := approvalDialog("curl -sI https://example.com")
	p.outW.Write([]byte(dialog))
	first := waitAttention(t, s, asked)
	if err := s.AnswerInput(t.Context(), nater, first.ID, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "y" {
		t.Fatalf("written %q", got)
	}
	waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Since.After(*first.Since) })
	// The rest of the frame lands between the next look's text and its
	// callback (approvalDialog begins a synchronized update of its own and
	// ends it).
	var once sync.Once
	s.asked.mu.Lock()
	s.asked.afterLook = func() {
		once.Do(func() { s.asked.Feed([]byte(dialog + "\x1b[?2026l")) })
	}
	s.asked.mu.Unlock()
	p.outW.Write([]byte("\x1b[?2026h\x1b[2J"))
	time.Sleep(2 * (patternQuiet + 300*time.Millisecond))
	a := s.Info().Attention
	if !asked(a) {
		t.Fatalf("the approval is not asked: %+v", a)
	}
	if err := s.AnswerInput(t.Context(), jane, a.ID, []byte("y")); !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("a click on the approval the frame finished with: %v", err)
	}
	noWrite(t, p, 200*time.Millisecond)
}
