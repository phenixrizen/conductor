package session

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// submitted is what a Submit returned.
type submitted struct {
	res SubmitResult
	err error
}

// submitAsync runs Submit on a goroutine of its own and hands its result
// over.
func submitAsync(s *Local, sub Submission) <-chan submitted {
	done := make(chan submitted, 1)
	go func() {
		res, err := s.Submit(context.Background(), sub)
		done <- submitted{res, err}
	}()
	return done
}

// heldShows fails unless the session shows the hold of its turn.
func heldShows(t *testing.T, s *Local) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		att := s.Info().Attention
		if att.State == AttentionNeedsInput && att.Source == SourceSubmit && att.Message == HeldWords && s.Held() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("attention %+v (held %v), want the hold's", att, s.Held())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// heldCleared fails unless the hold is over and shows no more.
func heldCleared(t *testing.T, s *Local) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		att := s.Info().Attention
		if att.State == AttentionNone && !s.Held() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("attention %+v (held %v), want none", att, s.Held())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A submission whose link is revoked, or expires, once its text is typed
// (in its pause, or while its Enter waits behind another's write) has no
// Enter: the text stays in the program's input, and the session's turn is
// held. The session needs input with the hold's words; the next
// submission, a person's or Conductor's own, waits; a terminal's own
// report does not end the hold, and keys typed before it began do not
// either. A person's keys end it: they are written, the words clear, and
// the next submission is typed after them, a line of its own.
func TestALineTypedBeforeItsLinkIsLetGoHoldsTheTurnUntilAPersonTypes(t *testing.T) {
	for _, c := range []struct {
		name    string
		expired bool
		queue   bool
		next    Submission
	}{
		{"revoked in its pause", false, false, Submission{Text: "second", ByName: "run"}},
		{"revoked behind another's write", false, true, Submission{Text: "second"}},
		{"expired in its pause", true, false, Submission{Text: "second"}},
		{"expired behind another's write", true, true, Submission{Text: "second", ByName: "run"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 300 * time.Millisecond})
			hold, takeAgain := gateWrites(t, p)
			var linkRevoked atomic.Bool
			guest := linkedController(t, s, newChanSink(false), &linkRevoked)
			other := ownerOf(t, s)
			done := submitAsync(s, Submission{Text: "first", By: guest})
			if got := written(t, p); got != "first" {
				t.Fatalf("written %q", got)
			}
			// In its pause: the writer has reported the text's write.
			untilIdle(t, s)
			if c.queue {
				// Another's keys take the writer in the pause, and wait.
				hold()
				go s.Input(other, []byte("k"))
				untilWaiting(t, s, 0)
			}
			want := ErrRevoked
			if c.expired {
				want = ErrExpired
				guest.closeWith(ErrExpired) // as the link's lapse does
			} else {
				linkRevoked.Store(true)
				s.DisconnectLink(guest.LinkID)
			}
			got := returnsWithin(t, 5*time.Second, "the submission", func() submitted { return <-done })
			if !errors.Is(got.err, want) || !got.res.Typed || got.res.Entered {
				t.Fatalf("result %+v, %v; want typed, not entered, %v", got.res, got.err, want)
			}
			if c.queue {
				takeAgain()
				if got := written(t, p); got != "k" {
					t.Fatalf("written %q", got)
				}
			}
			heldShows(t, s)
			next := c.next
			if next.ByName == "" {
				next.By = other
			}
			nextDone := submitAsync(s, next)
			nothingWritten(t, p, 300*time.Millisecond)
			// A terminal's own report is written and ends nothing.
			if err := s.Input(other, []byte("\x1b[3;1R")); err != nil {
				t.Fatal(err)
			}
			if got := written(t, p); got != "\x1b[3;1R" {
				t.Fatalf("written %q", got)
			}
			nothingWritten(t, p, 300*time.Millisecond)
			heldShows(t, s)

			// A person types into the terminal (Ctrl+U, the line killed).
			if err := s.Input(other, []byte{0x15}); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"\x15", "second", "\r"} {
				if got := written(t, p); got != want {
					t.Fatalf("written %q, want %q", got, want)
				}
			}
			if got := returnsWithin(t, 5*time.Second, "the next submission", func() submitted { return <-nextDone }); got.err != nil || !got.res.Entered {
				t.Fatalf("the next submission: %+v, %v", got.res, got.err)
			}
			heldCleared(t, s)
		})
	}
}

// The pointer, moved or clicked over a program with a mouse mode on, types
// nothing: its reports, in each encoding, alone, mixed with the terminal's
// own, by the dozen in one write or one cut between two writes, are written,
// and the hold, its words and the next submission's wait stay; a key then
// ends it.
func TestThePointerDoesNotEndTheHold(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 300 * time.Millisecond})
	var linkRevoked atomic.Bool
	guest := linkedController(t, s, newChanSink(false), &linkRevoked)
	owner := ownerOf(t, s)
	done := submitAsync(s, Submission{Text: "first", By: guest})
	if got := written(t, p); got != "first" {
		t.Fatalf("written %q", got)
	}
	untilIdle(t, s)
	linkRevoked.Store(true)
	s.DisconnectLink(guest.LinkID)
	<-done
	heldShows(t, s)
	next := submitAsync(s, Submission{Text: "second", ByName: "run"})
	for _, report := range []string{"\x1b[<35;10;10M", "\x1b[<0;3;4M\x1b[<0;3;4m", "\x1b[35;10;10M", "\x1b[M#**",
		"\x1b[I\x1b[<35;10;10M", strings.Repeat("\x1b[<35;10;10M", 30), "\x1b[<35;1", "0;10M\x1b[O",
		"\x1b]11;rg", "b:ffff/ffff/ffff\x1b", "\\"} {
		if err := s.Input(owner, []byte(report)); err != nil {
			t.Fatal(err)
		}
		if got := written(t, p); got != report {
			t.Fatalf("written %q", got)
		}
	}
	nothingWritten(t, p, 300*time.Millisecond)
	heldShows(t, s)
	if err := s.Input(owner, []byte{0x15}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\x15", "second", "\r"} {
		if got := written(t, p); got != want {
			t.Fatalf("written %q, want %q", got, want)
		}
	}
	<-next
	heldCleared(t, s)
}

// A submission whose sender only goes once its text is typed (a reply box
// closes its connection once it has sent) is finished: its Enter, and no
// hold.
func TestALineTypedBeforeItsSenderLeavesIsEntered(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 300 * time.Millisecond})
	var linkRevoked atomic.Bool
	guest := linkedController(t, s, newChanSink(false), &linkRevoked)
	done := submitAsync(s, Submission{Text: "first", By: guest})
	if got := written(t, p); got != "first" {
		t.Fatalf("written %q", got)
	}
	untilIdle(t, s)
	s.Detach(guest)
	if got := written(t, p); got != "\r" {
		t.Fatalf("written %q, want the line's Enter", got)
	}
	if got := returnsWithin(t, 5*time.Second, "the submission", func() submitted { return <-done }); got.err != nil || !got.res.Entered {
		t.Fatalf("result %+v, %v", got.res, got.err)
	}
	if s.Held() {
		t.Fatal("the turn is held")
	}
}

// A text whose write was begun, the program not taking it, when its link
// is revoked lands when the program reads again; its Enter is not written,
// and the turn is held from then on.
func TestATextBeingTakenWhenItsLinkIsRevokedHoldsTheTurnOnceItLands(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 50 * time.Millisecond})
	takeAgain := stopTaking(t, p)
	var linkRevoked atomic.Bool
	guest := linkedController(t, s, newChanSink(false), &linkRevoked)
	done := submitAsync(s, Submission{Text: "first", By: guest})
	untilWaiting(t, s, 0) // the text's write is begun, and waits
	linkRevoked.Store(true)
	s.DisconnectLink(guest.LinkID)
	got := returnsWithin(t, 5*time.Second, "the submission", func() submitted { return <-done })
	if !errors.Is(got.err, ErrRevoked) || !got.res.Typed || got.res.Entered {
		t.Fatalf("result %+v, %v", got.res, got.err)
	}
	takeAgain()
	if got := written(t, p); got != "first" {
		t.Fatalf("written %q", got)
	}
	heldShows(t, s)
	nothingWritten(t, p, 300*time.Millisecond)
}

// While the turn is held, the hold is what shows: another report is
// recorded, as the agent's report it is, and does not replace it.
func TestAHeldTurnKeepsShowingOverOtherReports(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 300 * time.Millisecond})
	var linkRevoked atomic.Bool
	guest := linkedController(t, s, newChanSink(false), &linkRevoked)
	done := submitAsync(s, Submission{Text: "first", By: guest})
	if got := written(t, p); got != "first" {
		t.Fatalf("written %q", got)
	}
	untilIdle(t, s)
	linkRevoked.Store(true)
	s.DisconnectLink(guest.LinkID)
	<-done
	heldShows(t, s)
	if err := s.TrySetAttentionFull(AttentionDone, "finished the task", SourceAPI, KindDone, nil); err != nil {
		t.Fatal(err)
	}
	s.SetAttention(AttentionNone, "", SourceAdmin)
	heldShows(t, s)
	found := false
	for _, e := range s.Activity() {
		if e.Type == ActivityAttention && e.Message == "finished the task" {
			found = true
		}
	}
	if !found {
		t.Fatal("the report made during the hold was not recorded")
	}
}

// Holding says when the turn becomes held: a channel asked for before the
// hold closes as it begins, one asked for while it stands is closed
// already, and one asked for after it ended waits for the next.
func TestHoldingClosesAsTheTurnIsHeld(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 300 * time.Millisecond})
	var linkRevoked atomic.Bool
	guest := linkedController(t, s, newChanSink(false), &linkRevoked)
	owner := ownerOf(t, s)
	before := s.Holding()
	done := submitAsync(s, Submission{Text: "first", By: guest})
	if got := written(t, p); got != "first" {
		t.Fatalf("written %q", got)
	}
	select {
	case <-before:
		t.Fatal("closed before the hold")
	default:
	}
	untilIdle(t, s)
	linkRevoked.Store(true)
	s.DisconnectLink(guest.LinkID)
	<-done
	select {
	case <-before:
	case <-time.After(5 * time.Second):
		t.Fatal("not closed as the hold began")
	}
	select {
	case <-s.Holding():
	default:
		t.Fatal("not closed while the hold stands")
	}
	if err := s.Input(owner, []byte("k")); err != nil {
		t.Fatal(err)
	}
	written(t, p)
	heldCleared(t, s)
	select {
	case <-s.Holding():
		t.Fatal("closed once the hold ended")
	default:
	}
}

// While the turn is held, at most maxTurnWaiters submissions wait for it:
// one more is refused at once (ErrTooManyRequests), and once those waiting
// have given up, others may wait again.
func TestTheSubmissionsWaitingForAHeldTurnAreBounded(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 300 * time.Millisecond})
	var linkRevoked atomic.Bool
	guest := linkedController(t, s, newChanSink(false), &linkRevoked)
	done := submitAsync(s, Submission{Text: "first", By: guest})
	if got := written(t, p); got != "first" {
		t.Fatalf("written %q", got)
	}
	untilIdle(t, s)
	linkRevoked.Store(true)
	s.DisconnectLink(guest.LinkID)
	<-done
	heldShows(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan error, maxTurnWaiters)
	for range maxTurnWaiters {
		go func() {
			_, err := s.Submit(ctx, Submission{Text: "waits", ByName: "run"})
			waiting <- err
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.turnWaiters.Load() != maxTurnWaiters {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("%d waiting", s.turnWaiters.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := s.Submit(context.Background(), Submission{Text: "one more", ByName: "run"}); !errors.Is(err, ErrTooManyRequests) {
		cancel()
		t.Fatalf("one more: %v", err)
	}
	cancel()
	for range maxTurnWaiters {
		if err := <-waiting; !errors.Is(err, context.Canceled) {
			t.Fatalf("a waiting one: %v", err)
		}
	}
	short, cancelShort := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelShort()
	if _, err := s.Submit(short, Submission{Text: "again", ByName: "run"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("again: %v", err)
	}
	nothingWritten(t, p, 100*time.Millisecond)
}

// A startup question on the screen shows over the hold, whether it came
// before the hold began (drawn in the line's pause) or after: the program
// waits on it, and its choices are what a person needs. A key that does not
// answer it (Ctrl+U) ends the hold and leaves the question showing, so the
// next submission is refused, not typed into the dialog; the question's
// answer clears it.
func TestAStartupQuestionShowsOverTheHold(t *testing.T) {
	for _, first := range []string{"question", "hold"} {
		t.Run(first+" first", func(t *testing.T) {
			s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 300 * time.Millisecond,
				TrustPattern: regexp.MustCompile(`Trust\s*this\s*folder\?`), TrustAnswers: []Option{{Label: "Yes", Input: "\r"}}})
			var linkRevoked atomic.Bool
			guest := linkedController(t, s, newChanSink(false), &linkRevoked)
			owner := ownerOf(t, s)
			done := submitAsync(s, Submission{Text: "first", By: guest})
			if got := written(t, p); got != "first" {
				t.Fatalf("written %q", got)
			}
			untilIdle(t, s)
			question := func() { s.fireTrust("Trust this folder?") }
			if first == "question" {
				question()
			}
			linkRevoked.Store(true)
			s.DisconnectLink(guest.LinkID)
			got := returnsWithin(t, 5*time.Second, "the submission", func() submitted { return <-done })
			if !errors.Is(got.err, ErrRevoked) || got.res.Entered {
				t.Fatalf("result %+v, %v", got.res, got.err)
			}
			if first == "hold" {
				heldShows(t, s)
				question()
			}
			deadline := time.Now().Add(5 * time.Second)
			for att := s.Info().Attention; att.Source != SourceTrust || !s.Held(); att = s.Info().Attention {
				if time.Now().After(deadline) {
					t.Fatalf("attention %+v (held %v), want the question's while held", att, s.Held())
				}
				time.Sleep(5 * time.Millisecond)
			}
			next := submitAsync(s, Submission{Text: "second", ByName: "run"})
			nothingWritten(t, p, 300*time.Millisecond)

			// A key that answers nothing: the hold ends, the question stays.
			if err := s.Input(owner, []byte{0x15}); err != nil {
				t.Fatal(err)
			}
			if got := written(t, p); got != "\x15" {
				t.Fatalf("written %q", got)
			}
			if got := returnsWithin(t, 5*time.Second, "the next submission", func() submitted { return <-next }); !errors.Is(got.err, ErrTrustQuestion) {
				t.Fatalf("the next submission: %+v, %v", got.res, got.err)
			}
			nothingWritten(t, p, 200*time.Millisecond)
			if att := s.Info().Attention; att.Source != SourceTrust || s.Held() {
				t.Fatalf("attention %+v (held %v), want the question's alone", att, s.Held())
			}
			// Its answer clears it.
			if err := s.Input(owner, []byte("\r")); err != nil {
				t.Fatal(err)
			}
			written(t, p)
			heldCleared(t, s)
		})
	}
}

// A dialog drawn in a line's pause leaves its Enter out; its link revoked
// meanwhile, the turn is held all the same, so that once the dialog goes
// (answered by the pointer, say) no line joins the text.
func TestADialogInThePauseOfALineWhoseLinkWentHoldsTheTurn(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{InputWait: 5 * time.Second, SubmitPause: 300 * time.Millisecond, Questions: approvalQuestions()}))
	var linkRevoked atomic.Bool
	guest := linkedController(t, s, newChanSink(false), &linkRevoked)
	done := submitAsync(s, Submission{Text: "first", By: guest})
	if got := written(t, p); got != "first" {
		t.Fatalf("written %q", got)
	}
	p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
	linkRevoked.Store(true)
	s.DisconnectLink(guest.LinkID)
	got := returnsWithin(t, 10*time.Second, "the submission", func() submitted { return <-done })
	if !errors.Is(got.err, ErrRevoked) || !got.res.Typed || got.res.Entered {
		t.Fatalf("result %+v, %v; want typed, not entered, revoked", got.res, got.err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !s.Held() {
		if time.Now().After(deadline) {
			t.Fatal("the turn was never held")
		}
		time.Sleep(5 * time.Millisecond)
	}
	nothingWritten(t, p, 300*time.Millisecond)
}

// A question answered while the turn is still held (keys typed before the
// hold, taken late) gives the hold its words back.
func TestTheHoldShowsAgainOnceAQuestionIsAnswered(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 300 * time.Millisecond,
		TrustPattern: regexp.MustCompile(`Trust\s*this\s*folder\?`)})
	var linkRevoked atomic.Bool
	guest := linkedController(t, s, newChanSink(false), &linkRevoked)
	done := submitAsync(s, Submission{Text: "first", By: guest})
	if got := written(t, p); got != "first" {
		t.Fatalf("written %q", got)
	}
	untilIdle(t, s)
	s.fireTrust("Trust this folder?")
	linkRevoked.Store(true)
	s.DisconnectLink(guest.LinkID)
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for !s.Held() {
		if time.Now().After(deadline) {
			t.Fatal("the turn was never held")
		}
		time.Sleep(5 * time.Millisecond)
	}
	att := s.Info().Attention
	if att.Source != SourceTrust {
		t.Fatalf("attention %+v", att)
	}
	// As keys typed before the hold, written late, answer it.
	s.answer(att.Since, "someone", "someone", true, true)
	heldShows(t, s)
	nothingWritten(t, p, 100*time.Millisecond)
}

// The hold ends with the session: the next submission is told the session
// ended, the turn is given back and nothing shows.
func TestAHeldTurnEndsWithTheSession(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 300 * time.Millisecond})
	var linkRevoked atomic.Bool
	guest := linkedController(t, s, newChanSink(false), &linkRevoked)
	done := submitAsync(s, Submission{Text: "first", By: guest})
	if got := written(t, p); got != "first" {
		t.Fatalf("written %q", got)
	}
	untilIdle(t, s)
	linkRevoked.Store(true)
	s.DisconnectLink(guest.LinkID)
	<-done
	heldShows(t, s)
	next := submitAsync(s, Submission{Text: "second", ByName: "run"})
	p.exit()
	if got := returnsWithin(t, 5*time.Second, "the next submission", func() submitted { return <-next }); !errors.Is(got.err, ErrSessionEnded) {
		t.Fatalf("the next submission: %+v, %v", got.res, got.err)
	}
	heldCleared(t, s)
	deadline := time.Now().Add(5 * time.Second)
	for len(s.submitting) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the session's turn was never given back")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
