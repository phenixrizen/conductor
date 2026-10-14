package crew

import (
	"slices"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

// A member that asks its own question (#193) and ends its turn waits on it:
// the members that start after it start at its done, as before; the run
// counts it as waiting after the done; a handoff to it waits until a person
// answers, then is typed.
func TestAMemberWaitsOnItsOwnQuestionAfterItsTurn(t *testing.T) {
	e, fl := newEngine(t)
	run := runningPair(t, e, fl, after("docs", "Doc it.", "tests"))
	lead, _ := fl.member("lead")
	tests, p := fl.member("tests")
	if err := tests.TryAsk("Keep the items in ~/.todo.json?", session.SourceAPI, "", nil); err != nil {
		t.Fatal(err)
	}
	if err := tests.TrySetAttentionFull(session.AttentionDone, "asked", session.SourceAPI, session.KindDone, nil); err != nil {
		t.Fatal(err)
	}
	waitTrue(t, "docs to start at tests' done", func() bool { return slices.Contains(fl.launched(), "docs") })
	waitTrue(t, "the run to count tests as waiting", func() bool {
		r, _ := e.Get(run.ID)
		return waitsIn(r, "tests")
	})
	lead.Record(handoffEntry("tests", "the handlers are in"))
	waitLogged(t, e, run.ID, "handoff queued from lead to tests: tests is waiting for input", 1, 5*time.Second)
	if got := typed(p); len(got) != 0 {
		t.Fatalf("typed %q into the member's question", got)
	}
	sub, err := tests.Attach("", session.RoleControl, "", 0, 0, nopSink{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tests.Input(sub, []byte("yes\r")); err != nil {
		t.Fatal(err)
	}
	if got := waitTyped(t, p, 5*time.Second); got != "yes\r" {
		t.Fatalf("typed %q", got)
	}
	if got := waitTyped(t, p, 5*time.Second); got != "Handoff from lead: the handlers are in\r" {
		t.Fatalf("typed %q", got)
	}
	waitLogged(t, e, run.ID, "handoff delivered from lead to tests", 1, 5*time.Second)
}

// A handoff typed while the member's question is set aside (the member at
// work), whose Enter follows the member's done, which shows the question
// again: the Enter is written all the same, the handoff noted delivered,
// never typed without its Enter; the question still waits, and a person's
// reply to it is typed on its own (P28).
func TestAHandoffTypedWhileTheQuestionIsAsideIsDelivered(t *testing.T) {
	e, fl := newEngine(t)
	fl.pause = 2 * time.Second
	run := runningPair(t, e, fl)
	lead, _ := fl.member("lead")
	tests, p := fl.member("tests")
	if err := tests.TryAsk("Keep the items in ~/.todo.json?", session.SourceAPI, "", nil); err != nil {
		t.Fatal(err)
	}
	q := tests.Info().Attention
	if err := tests.TrySetAttentionFull(session.AttentionWorking, "", session.SourceAPI, "", nil); err != nil {
		t.Fatal(err)
	}
	lead.Record(handoffEntry("tests", "the handlers are in"))
	select {
	case b := <-p.Input:
		if string(b) != "Handoff from lead: the handlers are in" {
			t.Fatalf("written %q", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handoff's text was not typed")
	}
	// In the handoff's pause: the member's turn ends.
	if err := tests.TrySetAttentionFull(session.AttentionDone, "asked", session.SourceAPI, session.KindDone, nil); err != nil {
		t.Fatal(err)
	}
	if att := tests.Info().Attention; att.ID != q.ID || att.State != session.AttentionNeedsInput {
		t.Fatalf("the done showed %+v, want the question", att)
	}
	if got := waitTyped(t, p, 5*time.Second); got != "\r" {
		t.Fatalf("typed %q, want the handoff's Enter", got)
	}
	waitLogged(t, e, run.ID, "handoff delivered from lead to tests", 1, 5*time.Second)
	if n := logCount(t, e, run.ID, "typed without its Enter"); n != 0 {
		t.Fatalf("%d notes of a handoff typed without its Enter", n)
	}
	if att := tests.Info().Attention; att.ID != q.ID {
		t.Fatalf("the handoff's Enter answered the question: %+v", att)
	}
	sub, err := tests.Attach("", session.RoleControl, "", 0, 0, nopSink{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := tests.Submit(t.Context(), session.Submission{Text: "yes", By: sub, Question: q.ID})
		done <- err
	}()
	if got := waitTyped(t, p, 10*time.Second); got != "yes\r" {
		t.Fatalf("typed %q, want the reply on its own", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if att := tests.Info().Attention; att.State != session.AttentionNone {
		t.Fatalf("the reply did not answer: %+v", att)
	}
}
