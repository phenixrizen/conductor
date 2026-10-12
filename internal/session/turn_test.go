package session

import (
	"testing"
	"time"
)

// bellAfter writes a bell past the burst limit of the one before and
// returns the attention once the session has read it.
func bellAfter(t *testing.T, s *Local, p *fakeProc, what string) Attention {
	t.Helper()
	time.Sleep(600 * time.Millisecond)
	p.outW.Write([]byte(what))
	time.Sleep(200 * time.Millisecond)
	return s.Info().Attention
}

// Codex reports its turn done (notify) and then rings the bell Conductor
// asks it for (rc.7 app, 2026-10-11): the bell is the ended turn's own, and
// the member stays done instead of waiting on nothing as "terminal bell".
// So is an OSC notification; so is a bell after keys that start nothing.
// The same done twice (a turn ending with the same words) ends its turn as
// well.
func TestADoneIsNotUndoneByItsTurnsBell(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	sub, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TrySetAttentionFull(AttentionDone, "HTTP/2 200", SourceAPI, KindDone, nil); err != nil {
		t.Fatal(err)
	}
	if a := bellAfter(t, s, p, "\a"); a.State != AttentionDone || a.Message != "HTTP/2 200" {
		t.Fatalf("the turn's bell undid its done: %+v", a)
	}
	if a := bellAfter(t, s, p, "\x1b]9;Agent turn complete\a"); a.State != AttentionDone {
		t.Fatalf("the turn's notification undid its done: %+v", a)
	}
	s.Input(sub, []byte("x")) // a key, no Enter: no new turn
	nextWrite(t, p)
	if a := bellAfter(t, s, p, "\a"); a.State != AttentionDone {
		t.Fatalf("a bell after a key undid the done: %+v", a)
	}
	// The next turn ends with the same words: its done shows nothing new, and still ends it.
	s.Input(sub, []byte("\r"))
	nextWrite(t, p)
	if err := s.TrySetAttentionFull(AttentionDone, "HTTP/2 200", SourceAPI, KindDone, nil); err != nil {
		t.Fatal(err)
	}
	if a := bellAfter(t, s, p, "\a"); a.State != AttentionDone {
		t.Fatalf("the second turn's bell undid its done: %+v", a)
	}
}

// The other order: the bell first, then the done; the done stands.
func TestABellThenTheDoneEndsDone(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	p.outW.Write([]byte("\a"))
	waitAttention(t, s, func(a Attention) bool { return a.Source == SourceBell })
	if err := s.TrySetAttentionFull(AttentionDone, "HTTP/2 200", SourceAPI, KindDone, nil); err != nil {
		t.Fatal(err)
	}
	if a := bellAfter(t, s, p, ""); a.State != AttentionDone {
		t.Fatalf("attention %+v", a)
	}
}

// The allowance ends with the turn: a submission's Enter (a crew's next
// prompt) or a person's starts the next one, and a bell then is a question,
// also at once (Codex asking for approval at the start of a turn, the
// only sign of it without its hooks); its dialog, once quiet, is asked
// with its choices. A working report ends it too.
func TestANewTurnsBellIsAQuestion(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{Questions: approvalQuestions()}))
	if err := s.TrySetAttentionFull(AttentionDone, "HTTP/2 200", SourceAPI, KindDone, nil); err != nil {
		t.Fatal(err)
	}
	if res, err := s.Submit(t.Context(), Submission{Text: "now fetch it again", ByName: "crew"}); err != nil || !res.Entered {
		t.Fatalf("the next prompt: %+v %v", res, err)
	}
	nextWrite(t, p)
	nextWrite(t, p)
	p.outW.Write([]byte("\a" + approvalDialog("curl -sI https://example.com")))
	// The bell asks at once; the dialog's question takes its place once the screen is quiet (500 ms).
	if a := waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNeedsInput }); a.Source != SourceBell {
		t.Fatalf("the new turn's bell: %+v", a)
	}
	if a := waitAttention(t, s, asked); a.Message != approvalWords || len(a.Options) != 3 {
		t.Fatalf("the new turn's approval: %+v", a)
	}

	s2, p2 := newLocalWith(t, quiet(Options{}))
	sub, err := s2.Attach("", RoleControl, "", 80, 24, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.TrySetAttentionFull(AttentionDone, "done", SourceAPI, KindDone, nil); err != nil {
		t.Fatal(err)
	}
	s2.Input(sub, []byte("\r"))
	nextWrite(t, p2)
	if a := bellAfter(t, s2, p2, "\a"); a.State != AttentionNeedsInput || a.Source != SourceBell {
		t.Fatalf("a bell after a person's Enter: %+v", a)
	}

	s3, p3 := newLocalWith(t, quiet(Options{}))
	if err := s3.TrySetAttentionFull(AttentionDone, "done", SourceAPI, KindDone, nil); err != nil {
		t.Fatal(err)
	}
	if err := s3.TrySetAttentionFull(AttentionWorking, "", SourceAPI, "", nil); err != nil {
		t.Fatal(err)
	}
	if a := bellAfter(t, s3, p3, "\a"); a.State != AttentionNeedsInput || a.Source != SourceBell {
		t.Fatalf("a bell after a working report: %+v", a)
	}
}

// flushed returns once the session has scanned everything written to p
// before it: the pipe hands a write over only as the pump reads it, and the
// pump reads again only once it has looked at the chunk before.
func flushed(p *fakeProc) { p.outW.Write([]byte{0}) }

// isBell reports whether the session waits on the terminal bell.
func isBell(a Attention) bool { return a.State == AttentionNeedsInput && a.Source == SourceBell }

// A new turn's bell asks however soon it rings after the last turn's
// (round 15's todo): the bell that ended a turn and changed nothing does
// not start the pace, and the Enter after a done begins a turn whose bells
// are paced on their own. Both orders of Codex's done and bell: the bell
// after the done (kept as the turn's own), and the bell before it (which
// asked, and which the done then replaced).
func TestANewTurnsBellSoonAfterTheLastTurnsAsks(t *testing.T) {
	for name, bellFirst := range map[string]bool{"the bell after the done": false, "the bell before the done": true} {
		t.Run(name, func(t *testing.T) { newTurnsBell(t, bellFirst) })
	}
}

// newTurnsBell is one order of TestANewTurnsBellSoonAfterTheLastTurnsAsks.
func newTurnsBell(t *testing.T, bellFirst bool) {
	s, p := newLocalWith(t, quiet(Options{}))
	sub, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	lastTurns := time.Now() // no later than the last turn's bell
	if bellFirst {
		p.outW.Write([]byte("\a"))
		waitAttention(t, s, isBell)
	}
	if err := s.TrySetAttentionFull(AttentionDone, "HTTP/2 200", SourceAPI, KindDone, nil); err != nil {
		t.Fatal(err)
	}
	if !bellFirst {
		p.outW.Write([]byte("\a"))
		flushed(p)
		if a := s.Info().Attention; a.State != AttentionDone {
			t.Fatalf("the turn's own bell undid its done: %+v", a)
		}
	}
	// The next prompt's Enter, and the new turn's bell at once.
	if err := s.Input(sub, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	p.outW.Write([]byte("\a"))
	if a := waitAttention(t, s, isBell); a.Message != "terminal bell" {
		t.Fatalf("the new turn's bell %+v", a)
	}
	if gap := time.Since(lastTurns); gap >= bellPace {
		t.Logf("the turns' bells were %v apart, not within the pace: the case was not tried", gap)
	}
}

// withinPace runs try until it reports that what it checked happened
// within bellPace of the change that started the pace: a machine slow
// enough to let the pace run out leaves nothing to check, and is tried
// again, three times at most.
func withinPace(t *testing.T, try func() bool) {
	t.Helper()
	for range 3 {
		if try() {
			return
		}
	}
	t.Fatal("never within the pace: the machine is too slow to tell")
}

// The pace's purpose stands: within one turn a burst of bells makes one
// change per bellPace, whatever comes between (a working report, an
// answer with no done before it), and the first bell past it asks again.
func TestABurstOfBellsMakesOneChange(t *testing.T) {
	withinPace(t, func() bool {
		s, p := newLocalWith(t, quiet(Options{}))
		p.outW.Write([]byte("\a"))
		first := waitAttention(t, s, isBell)
		if err := s.TrySetAttentionFull(AttentionWorking, "", SourceAPI, "", nil); err != nil {
			t.Fatal(err)
		}
		p.outW.Write([]byte("\a\a\x1b]9;and more\a"))
		flushed(p)
		a := s.Info().Attention
		if time.Since(*first.Since) >= bellPace {
			return false
		}
		if a.State != AttentionWorking {
			t.Fatalf("a bell within the pace changed the attention: %+v", a)
		}
		var bells int
		for _, e := range s.Activity() {
			if e.Type == ActivityAttention && e.Message == "terminal bell" {
				bells++
			}
		}
		if bells != 1 {
			t.Fatalf("the burst made %d changes", bells)
		}
		// Past the pace the next bell asks.
		time.Sleep(time.Until(first.Since.Add(bellPace)))
		p.outW.Write([]byte("\a"))
		waitAttention(t, s, isBell)
		return true
	})
	withinPace(t, func() bool {
		s, p := newLocalWith(t, quiet(Options{}))
		sub, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false))
		if err != nil {
			t.Fatal(err)
		}
		p.outW.Write([]byte("\a"))
		first := waitAttention(t, s, isBell)
		// An Enter that answers the bell, no done before it: the same turn.
		if err := s.Input(sub, []byte("\r")); err != nil {
			t.Fatal(err)
		}
		nextWrite(t, p)
		p.outW.Write([]byte("\a"))
		flushed(p)
		a := s.Info().Attention
		if time.Since(*first.Since) >= bellPace {
			return false
		}
		if a.State != AttentionNone {
			t.Fatalf("the burst's tail asked again after its answer: %+v", a)
		}
		return true
	})
}
