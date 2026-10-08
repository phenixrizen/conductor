package session

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// noWrite fails when p is written to within d.
func noWrite(t *testing.T, p *fakeProc, d time.Duration) {
	t.Helper()
	select {
	case b := <-p.input:
		t.Fatalf("wrote %q", b)
	case <-time.After(d):
	}
}

// waitPaste waits until s has seen the program set bracketed paste to on.
func waitPaste(t *testing.T, s *Local, on bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if seen, now := s.BracketedPaste(); seen && now == on {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("bracketed paste never turned %v", on)
}

// The text and its Enter are two writes, the pause apart; the text is
// recorded once, by the name given.
func TestSubmitWritesTheTextThenItsEnter(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{SubmitPause: 80 * time.Millisecond}))
	done := make(chan SubmitResult, 1)
	go func() {
		res, err := s.Submit(t.Context(), Submission{Text: "Own the plan.", ByName: "crew"})
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	if got := nextWrite(t, p); got != "Own the plan." {
		t.Fatalf("text %q", got)
	}
	start := time.Now()
	if got := nextWrite(t, p); got != "\r" {
		t.Fatalf("enter %q", got)
	}
	if d := time.Since(start); d < 60*time.Millisecond {
		t.Fatalf("the Enter came %v after the text", d)
	}
	if res := <-done; !res.Typed || !res.Entered || res.Reentered {
		t.Fatalf("result %+v", res)
	}
	if e := inputEntries(s); len(e) != 1 || e[0].ByName != "crew" || e[0].Message != "Own the plan." {
		t.Fatalf("input entries %+v", e)
	}
}

// While the program has bracketed paste on, the text goes in its markers,
// with no escape of its own: the text cannot end the paste early.
func TestSubmitPastesWhileTheProgramAsksForIt(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	p.outW.Write([]byte("\x1b[?20"))
	p.outW.Write([]byte("04h> ")) // split between two reads
	waitPaste(t, s, true)
	go s.Submit(t.Context(), Submission{Text: "fix \x1b[201~it\nnow", ByName: "crew"})
	if got := nextWrite(t, p); got != "\x1b[200~fix [201~it now\x1b[201~" {
		t.Fatalf("paste %q", got)
	}
	if got := nextWrite(t, p); got != "\r" {
		t.Fatalf("enter %q", got)
	}
	p.outW.Write([]byte("\x1b[?2004l"))
	waitPaste(t, s, false)
	go s.Submit(t.Context(), Submission{Text: "raw", ByName: "crew"})
	if got := nextWrite(t, p); got != "raw" {
		t.Fatalf("raw %q", got)
	}
	nextWrite(t, p)
}

// The Enter answers the prompt that showed when the submission began, as a
// person's typing does.
func TestSubmitAnswersThePromptItWasTypedInto(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	s.SetAttention(AttentionNeedsInput, "Name the branch?", SourceAPI)
	go s.Submit(t.Context(), Submission{Text: "main", ByName: "crew"})
	nextWrite(t, p)
	if st := s.Info().Attention.State; st != AttentionNeedsInput {
		t.Fatalf("the text alone answered the prompt: %q", st)
	}
	nextWrite(t, p)
	deadline := time.Now().Add(3 * time.Second)
	for s.Info().Attention.State != AttentionNone && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	info := s.Info()
	if info.Attention.State != AttentionNone || info.LastAnswer == nil || info.LastAnswer.ByName != "crew" || info.LastAnswer.Message != "Name the branch?" {
		t.Fatalf("after the Enter: %+v %+v", info.Attention, info.LastAnswer)
	}
}

// UnlessWaiting writes and records nothing while a prompt shows.
func TestSubmitUnlessWaitingLeavesAPromptAlone(t *testing.T) {
	var changes atomic.Int32
	s, p := newLocalWith(t, quiet(Options{OnChange: func(Info) { changes.Add(1) }}))
	s.SetAttention(AttentionNeedsInput, "Allow edit?", SourceAPI)
	before, logged := changes.Load(), len(s.Activity())
	res, err := s.Submit(t.Context(), Submission{Text: "Handoff from lead: go", ByName: "crew", UnlessWaiting: true})
	if err != nil || res != (SubmitResult{}) {
		t.Fatalf("waiting: %+v %v", res, err)
	}
	noWrite(t, p, 50*time.Millisecond)
	if info := s.Info(); info.Attention.State != AttentionNeedsInput || info.LastAnswer != nil || changes.Load() != before || len(s.Activity()) != logged {
		t.Fatalf("waiting: %+v", info)
	}
}

// A prompt raised during the pause is never answered: the Enter is left out,
// and the text waits in the program's input.
func TestSubmitLeavesTheEnterOutForAPromptRaisedDuringThePause(t *testing.T) {
	for _, unless := range []bool{false, true} {
		s, p := newLocalWith(t, quiet(Options{}))
		p.onWrite = func() { s.SetAttention(AttentionNeedsInput, "Allow write?", SourceAPI) }
		res, err := s.Submit(t.Context(), Submission{Text: "Handoff from lead: go", ByName: "crew", UnlessWaiting: unless})
		if err != nil || !res.Typed || res.Entered {
			t.Fatalf("unless %v: %+v %v", unless, res, err)
		}
		nextWrite(t, p)
		noWrite(t, p, 50*time.Millisecond)
		if info := s.Info(); info.Attention.Message != "Allow write?" || info.LastAnswer != nil {
			t.Fatalf("unless %v: the prompt raised in the pause was answered: %+v", unless, info)
		}
	}
}

// Two submissions never interleave: each text is followed by its own Enter.
func TestSubmitIsOneAtATime(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	var wg sync.WaitGroup
	for _, text := range []string{"one", "two", "three"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Submit(t.Context(), Submission{Text: text, ByName: "crew"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for range 3 {
		if text, enter := nextWrite(t, p), nextWrite(t, p); text == "\r" || enter != "\r" {
			t.Fatalf("interleaved: %q then %q", text, enter)
		}
	}
}

// A submission cut short in its pause returns with the text typed and never
// writes the Enter.
func TestSubmitIsCancellable(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{SubmitPause: time.Second}))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	var res SubmitResult
	go func() {
		var err error
		res, err = s.Submit(ctx, Submission{Text: "slow", ByName: "crew"})
		done <- err
	}()
	nextWrite(t, p)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || !res.Typed || res.Entered {
		t.Fatalf("%+v %v", res, err)
	}
	noWrite(t, p, 50*time.Millisecond)
}

// BeforeEnter runs after the text and before the Enter.
func TestSubmitCallsBeforeEnterJustBeforeTheEnter(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	var order []string
	var mu sync.Mutex
	p.onWrite = func() {
		mu.Lock()
		order = append(order, "write")
		mu.Unlock()
	}
	go s.Submit(t.Context(), Submission{Text: "go", ByName: "crew", BeforeEnter: func() {
		mu.Lock()
		order = append(order, "before")
		mu.Unlock()
	}})
	nextWrite(t, p)
	nextWrite(t, p)
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "write,before,write" {
		t.Fatalf("order %v", order)
	}
}

// An agent that reports taking a prompt and has not within ConfirmWait gets
// one more Enter; one that reports, or that shows a question, gets none.
func TestSubmitPressesEnterAgainWithoutConfirmation(t *testing.T) {
	opts := quiet(Options{ConfirmSubmit: true, ConfirmWait: 150 * time.Millisecond})
	s, p := newLocalWith(t, opts)
	res, err := s.Submit(t.Context(), Submission{Text: "Plan it.", ByName: "crew", Confirm: true})
	if err != nil || !res.Entered || !res.Reentered {
		t.Fatalf("no confirmation: %+v %v", res, err)
	}
	for _, want := range []string{"Plan it.", "\r", "\r"} {
		if got := nextWrite(t, p); got != want {
			t.Fatalf("wrote %q, want %q", got, want)
		}
	}

	s, p = newLocalWith(t, opts)
	p.onWrite = func() {
		if len(p.input) == 1 { // the Enter: the agent takes the prompt
			go s.SetAttention(AttentionWorking, "", SourceAPI)
		}
	}
	if res, err := s.Submit(t.Context(), Submission{Text: "Plan it.", ByName: "crew", Confirm: true}); err != nil || res.Reentered {
		t.Fatalf("confirmed: %+v %v", res, err)
	}

	s, p = newLocalWith(t, opts)
	p.onWrite = func() {
		if len(p.input) == 1 {
			go s.SetAttention(AttentionNeedsInput, "Allow?", SourceAPI)
		}
	}
	if res, err := s.Submit(t.Context(), Submission{Text: "Plan it.", ByName: "crew", Confirm: true}); err != nil || res.Reentered {
		t.Fatalf("a question came up: %+v %v", res, err)
	}

	// Without ConfirmSubmit, or without Confirm, never.
	s, _ = newLocalWith(t, quiet(Options{ConfirmWait: 50 * time.Millisecond}))
	if res, _ := s.Submit(t.Context(), Submission{Text: "x", ByName: "crew", Confirm: true}); res.Reentered {
		t.Fatal("re-entered for an agent that does not confirm")
	}
	s, _ = newLocalWith(t, opts)
	if res, _ := s.Submit(t.Context(), Submission{Text: "x", ByName: "crew"}); res.Reentered {
		t.Fatal("re-entered a submission that did not ask")
	}
}

// A viewer cannot submit, a text over MaxSubmitText is refused before
// anything is written, and an ended session takes nothing.
func TestSubmitRefusals(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	viewer := &Subscription{ID: "v", Role: RoleView}
	if _, err := s.Submit(t.Context(), Submission{Text: "x", By: viewer}); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("viewer: %v", err)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: strings.Repeat("x", MaxSubmitText+1), ByName: "crew"}); !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("long: %v", err)
	}
	noWrite(t, p, 20*time.Millisecond)
	go s.Submit(t.Context(), Submission{Text: strings.Repeat("x", MaxSubmitText), ByName: "crew"})
	if got := nextWrite(t, p); len(got) != MaxSubmitText {
		t.Fatalf("wrote %d bytes", len(got))
	}
	nextWrite(t, p)
	p.exit()
	<-s.Ended()
	if res, err := s.Submit(t.Context(), Submission{Text: "late", ByName: "crew"}); !errors.Is(err, ErrSessionEnded) || res.Typed {
		t.Fatalf("ended: %+v %v", res, err)
	}
}

func TestSubmitLine(t *testing.T) {
	for in, want := range map[string]string{
		"  a\nb\tc\r ":          "a b c",
		"bell\a and esc\x1b[2J": "bell and esc[2J",
		"bad \xff utf8":         "bad  utf8",
		"\x00":                  "",
	} {
		if got := SubmitLine(in); got != want {
			t.Errorf("SubmitLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// Output that only sets the window title does not move LastOutputAt.
func TestATitleUpdateIsNotOutput(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	p.outW.Write([]byte("\x1b]0;⠋ codex\a\x1b]2;⠙ codex\x1b\\"))
	time.Sleep(50 * time.Millisecond)
	if at := s.LastOutputAt(); !at.IsZero() {
		t.Fatalf("a title update moved LastOutputAt to %v", at)
	}
	p.outW.Write([]byte("\x1b]0;x\a> "))
	deadline := time.Now().Add(3 * time.Second)
	for s.LastOutputAt().IsZero() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.LastOutputAt().IsZero() {
		t.Fatal("text with a title update did not count")
	}
}

// The agent's trust question, drawn with cursor moves between its words,
// marks the session needs_input with its words once the screen is quiet;
// the first submission's Enter ends the watching.
func TestATrustQuestionNeedsInput(t *testing.T) {
	re := regexp.MustCompile(`Trust\s*this\s*folder\?`)
	s, p := newLocalWith(t, quiet(Options{TrustPattern: re}))
	p.outW.Write([]byte("\x1b[2;3HTrust\x1b[2;9Hthis\x1b[1Cfolder?\r\n\x1b[38;5;2m› 1. Trust and continue\x1b[0m"))
	p.outW.Write([]byte("\x1b]0;⠋\a")) // a title update does not restart the quiet
	deadline := time.Now().Add(3 * time.Second)
	for s.Info().Attention.State != AttentionNeedsInput && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	att := s.Info().Attention
	if att.State != AttentionNeedsInput || att.Source != SourceTrust || att.Message != "Trust this folder?" {
		t.Fatalf("attention %+v", att)
	}
}

func TestScreenTail(t *testing.T) {
	var st ScreenTail
	st.Write([]byte("\x1b[1;1HDo\x1b[1;4Hyou"))
	st.Write([]byte("\x1b[3"))
	st.Write([]byte("Ctrust\r\n\tit?"))
	if got := st.Last(); got != "Do you trust it?" {
		t.Fatalf("%q", got)
	}
	st.Write([]byte(strings.Repeat("x", 3000)))
	if got := st.Last(); len(got) != maxScreenTail {
		t.Fatalf("kept %d bytes", len(got))
	}
}

// A trust question is answered by Enter alone: an arrow key that moves its
// selection leaves it showing. Once answered, only the question drawn anew
// raises it again.
func TestATrustQuestionIsAnsweredByEnter(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{TrustPattern: regexp.MustCompile(`Trust\s*this\s*folder\?`)}))
	sub, err := s.Attach("", RoleControl, "", 0, 0, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	waitTrust := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for s.Info().Attention.Source != SourceTrust && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if s.Info().Attention.Source != SourceTrust {
			t.Fatalf("no trust question: %+v", s.Info().Attention)
		}
	}
	p.outW.Write([]byte("Trust this folder?\r\n› 1. Trust and continue"))
	waitTrust()
	s.Input(sub, []byte("\x1b[B"))
	nextWrite(t, p)
	if st := s.Info().Attention; st.State != AttentionNeedsInput || st.Source != SourceTrust {
		t.Fatalf("an arrow key answered the trust question: %+v", st)
	}
	s.Input(sub, []byte("\r"))
	nextWrite(t, p)
	if st := s.Info().Attention.State; st != AttentionNone {
		t.Fatalf("Enter did not answer: %q", st)
	}
	p.outW.Write([]byte("\x1b[2J> ")) // the agent's own screen: the old question does not count again
	time.Sleep(700 * time.Millisecond)
	if st := s.Info().Attention.State; st != AttentionNone {
		t.Fatalf("the answered question came back: %+v", s.Info().Attention)
	}
	p.outW.Write([]byte("Trust this folder?"))
	waitTrust()
}

// A trust question drawn during the pause, which the screen watcher finds
// only once the screen is quiet, leaves the Enter out: the text waits, the
// question shows with its words, and nothing accepts it.
func TestSubmitLeavesTheEnterOutForATrustQuestionDrawnDuringThePause(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{TrustPattern: regexp.MustCompile(`Trust\s*this\s*folder\?`)}))
	p.outW.Write([]byte("\x1b[?2004h› Ask anything "))
	waitPaste(t, s, true)
	var once sync.Once
	p.onWrite = func() {
		// The text arrives: the dialog is drawn before the Enter would be.
		once.Do(func() {
			go p.outW.Write([]byte("\x1b[2;3HFolder access\r\nTrust this folder?\r\n› 1. Trust and continue"))
		})
	}
	res, err := s.Submit(t.Context(), Submission{Text: "Reply READY", ByName: "crew"})
	if err != nil || !res.Typed || res.Entered {
		t.Fatalf("result %+v %v", res, err)
	}
	nextWrite(t, p)
	noWrite(t, p, 100*time.Millisecond)
	if att := s.Info().Attention; att.State != AttentionNeedsInput || att.Source != SourceTrust || att.Message != "Trust this folder?" {
		t.Fatalf("attention %+v", att)
	}
}

// A trust question carries its answers as the prompt's choices; a person's
// typed text is refused while it shows, with nothing written (its Enter
// would pick whatever the dialog highlights); the choice's keys answer it.
func TestATrustQuestionCarriesItsAnswersAndRefusesTypedText(t *testing.T) {
	answers := []Option{{Label: "Yes, I trust this folder", Input: "\x1b[B\r"}, {Label: "No, exit", Input: "\r"}}
	s, p := newLocalWith(t, quiet(Options{TrustPattern: regexp.MustCompile(`Trust\s*this\s*folder\?`), TrustAnswers: answers}))
	sub, err := s.Attach("", RoleControl, "", 0, 0, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte("Trust this folder?\r\n› 1. No, exit"))
	deadline := time.Now().Add(3 * time.Second)
	for s.Info().Attention.Source != SourceTrust && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	att := s.Info().Attention
	if att.Source != SourceTrust || len(att.Options) != 2 || att.Options[0] != answers[0] || att.Options[1] != answers[1] {
		t.Fatalf("attention %+v", att)
	}
	res, err := s.Submit(t.Context(), Submission{Text: "yes", By: sub})
	if !errors.Is(err, ErrTrustQuestion) || res.Typed || res.Entered {
		t.Fatalf("typed text at the trust question: %+v %v", res, err)
	}
	select {
	case b := <-p.input:
		t.Fatalf("something was written: %q", b)
	case <-time.After(300 * time.Millisecond):
	}
	// Conductor's own typing (a prompt, a handoff) waits as before, refused by nothing.
	if res, err := s.Submit(t.Context(), Submission{Text: "handoff", ByName: "crew", UnlessWaiting: true}); err != nil || res.Typed {
		t.Fatalf("Conductor's own text: %+v %v", res, err)
	}
	// The trusting choice's keys, raw, as a page sends them.
	if err := s.Input(sub, []byte(answers[0].Input)); err != nil {
		t.Fatal(err)
	}
	if got := nextWrite(t, p); got != "\x1b[B\r" {
		t.Fatalf("written %q", got)
	}
	if st := s.Info().Attention.State; st != AttentionNone {
		t.Fatalf("the choice did not answer: %q", st)
	}
	if _, err := s.Submit(t.Context(), Submission{Text: "now fine", By: sub}); err != nil {
		t.Fatalf("after the answer: %v", err)
	}
}
