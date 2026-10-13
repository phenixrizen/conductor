package session

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

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
	if a := s.Info(); a.Attention.State != AttentionNone || a.LastAnswer == nil || a.LastAnswer.ByName != "jane" {
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
// once it shows (it waited behind another write), is the question's answer
// as the program takes it: it puts the question behind as it is written,
// and a reply to the question queued after it is refused. (Codex's review
// of this change, finding 2.)
func TestAKeyQueuedBeforeAQuestionShowedPutsItBehind(t *testing.T) {
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
	for _, want := range []string{"x", "\r"} {
		if got := nextWrite(t, p); got != want {
			t.Fatalf("written %q, want %q", got, want)
		}
	}
	if err := <-typed; err != nil {
		t.Fatal(err)
	}
	if err := <-replied; !errors.Is(err, ErrQuestionAnswered) {
		t.Fatalf("the reply behind the Enter: %v", err)
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
			again := waitAttention(t, s, func(a Attention) bool { return asked(a) && a.Since.After(*first.Since) })
			err := s.AnswerInput(t.Context(), jane, again.ID, []byte("y"))
			if tc.answers {
				if !errors.Is(err, ErrQuestionAnswered) {
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

// A reply that names no question is written as keystrokes always were
// (an older page): it answers whatever shows.
func TestAReplyWithNoIDStillAnswers(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	sub, _ := s.AttachWith(AttachOptions{Role: RoleControl, Name: "nater"}, newChanSink(false))
	s.SetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "3"}})
	if err := s.Input(sub, []byte("1")); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "1" {
		t.Fatalf("written %q", got)
	}
	if a := s.Info(); a.Attention.State != AttentionNone || a.LastAnswer == nil || a.LastAnswer.Message != "Allow Bash?" {
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
	if m := decodeControl(t, AnswerResult("a2", ErrSubmitPending)); m["t"] != proto.CtlAnswerOK {
		t.Fatalf("a line accepted %v", m)
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
