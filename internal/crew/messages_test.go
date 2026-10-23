package crew

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/crew/ledger"
	"github.com/phenixrizen/conductor/internal/session"
)

// waitMessages waits for the run's messages to satisfy ok.
func waitMessages(t *testing.T, e *Engine, runID, what string, ok func([]Message) bool) []Message {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		msgs, err := e.MessagesOf(runID)
		if err != nil {
			t.Fatal(err)
		}
		if ok(msgs) {
			return msgs
		}
		if time.Now().After(deadline) {
			t.Fatalf("the messages are not %s after 10 s: %+v", what, msgs)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A message to a member that has no session yet is kept, typed into the
// member's session after its task line once its attempt starts, and
// recorded delivered; the member reads its inbox and acknowledges (row 9
// of #334).
func TestAMessageIsTypedIntoTheRecipientOnceItHasItsTask(t *testing.T) {
	dir := newRepo(t)
	e, fl, l := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), work("docs", "writer", "build")))
	ctx := context.Background()
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	m, err := e.SendMessage(ctx, b.Attempt.SessionID, "writer", "the API is final: internal/api/users.go")
	if err != nil || m.Seq != 1 || m.From != "lead" || m.To != "writer" || m.DeliveredAt != nil || m.AckedAt != nil {
		t.Fatalf("send: %+v %v", m, err)
	}
	for _, tc := range []struct {
		to, text string
		err      error
	}{{"lead", "to myself", ErrBadMessage}, {"nobody", "x", ErrMemberNotFound}, {"writer", "   ", ErrBadMessage}, {"writer", strings.Repeat("x", maxMessageChars+1), ErrBadMessage}} {
		if _, err := e.SendMessage(ctx, b.Attempt.SessionID, tc.to, tc.text); !errors.Is(err, tc.err) {
			t.Fatalf("to %q: %v, want %v", tc.to, err, tc.err)
		}
	}
	if msgs, _ := e.MessagesOf(run.ID); len(msgs) != 1 {
		t.Fatalf("messages: %+v", msgs)
	}
	// build's result starts docs: the message follows docs' task line into its session.
	commitResult(t, e, b, "built.txt", "yes\n", "built")
	d := waitAttempt(t, e, run.ID, "docs", ledger.AttemptRunning)
	_, p := fl.attemptSession(t, d)
	want := "Message from lead (#1): the API is final: internal/api/users.go"
	deadline := time.Now().Add(10 * time.Second)
	for {
		lines := typed(p)
		if slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, want) }) {
			if !strings.Contains(lines[0], "Conductor task") {
				t.Fatalf("the message came before the task line: %q", lines)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the message was not typed: %q", lines)
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitMessages(t, e, run.ID, "delivered", func(ms []Message) bool { return len(ms) == 1 && ms[0].DeliveredAt != nil })
	// docs reads and acknowledges.
	inbox, err := e.Inbox(d.Attempt.SessionID, 0)
	if err != nil || len(inbox) != 1 || inbox[0].Seq != 1 || inbox[0].DeliveredAt == nil || inbox[0].AckedAt != nil {
		t.Fatalf("docs' inbox: %+v %v", inbox, err)
	}
	if later, _ := e.Inbox(d.Attempt.SessionID, 1); len(later) != 0 {
		t.Fatalf("after 1: %+v", later)
	}
	if n, err := e.Ack(ctx, d.Attempt.SessionID, 1); err != nil || n != 1 {
		t.Fatalf("ack: %d %v", n, err)
	}
	if n, err := e.Ack(ctx, d.Attempt.SessionID, 1); err != nil || n != 0 {
		t.Fatalf("ack again: %d %v", n, err)
	}
	if inbox, _ := e.Inbox(d.Attempt.SessionID, 0); inbox[0].AckedAt == nil {
		t.Fatalf("not acknowledged: %+v", inbox)
	}
	// lead's inbox holds nothing: the message was to writer.
	if mine, _ := e.Inbox(b.Attempt.SessionID, 0); len(mine) != 0 {
		t.Fatalf("lead's inbox: %+v", mine)
	}
	// The ledger has the message, delivered and acknowledged; the events say so.
	from, _, _ := l.Load(ctx, run.ID)
	if len(from.Messages) != 1 || from.Messages[0].Text != "the API is final: internal/api/users.go" || from.Messages[0].DeliveredAt.IsZero() || from.Messages[0].AckedAt.IsZero() {
		t.Fatalf("the ledger's messages: %+v", from.Messages)
	}
	kinds := l.kinds(run.ID)
	for _, want := range []string{"message.sent", "message.delivered", "message.acked"} {
		if !slices.Contains(kinds, want) {
			t.Errorf("no %s in %v", want, kinds)
		}
	}
	stopRun(t, e, run.ID)
}

// A member keeps at most fifty messages not acknowledged.
func TestAnInboxHoldsFiftyUnacknowledgedMessages(t *testing.T) {
	dir := newRepo(t)
	e, _, _ := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), work("docs", "writer", "build")))
	ctx := context.Background()
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	for i := 1; i <= maxInbox; i++ {
		if m, err := e.SendMessage(ctx, b.Attempt.SessionID, "writer", fmt.Sprintf("m%d", i)); err != nil || m.Seq != int64(i) {
			t.Fatalf("message %d: %+v %v", i, m, err)
		}
	}
	if _, err := e.SendMessage(ctx, b.Attempt.SessionID, "writer", "one too many"); !errors.Is(err, ErrInboxFull) {
		t.Fatalf("the 51st: %v", err)
	}
	if msgs, _ := e.MessagesOf(run.ID); len(msgs) != maxInbox {
		t.Fatalf("%d messages kept", len(msgs))
	}
	stopRun(t, e, run.ID)
}

// A handoff reported in a managed run is a message to the member named.
func TestAHandoffInAManagedRunIsAMessage(t *testing.T) {
	dir := newRepo(t)
	e, _, _ := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), work("docs", "writer", "build")))
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	e.OnActivity(b.Attempt.SessionID, session.ActivityEntry{Type: session.ActivityHandoff, To: "writer", Message: "over to you: the handlers are in", At: time.Now().UTC()}, session.AttentionDone)
	msgs := waitMessages(t, e, run.ID, "the handoff's", func(ms []Message) bool { return len(ms) == 1 })
	if msgs[0].From != "lead" || msgs[0].To != "writer" || msgs[0].Text != "over to you: the handlers are in" {
		t.Fatalf("the message: %+v", msgs[0])
	}
	stopRun(t, e, run.ID)
}

// A continuation carries the run's messages: one not delivered is typed
// into its recipient's session in the continuation.
func TestAContinuationCarriesTheMessages(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	run := launchManaged(t, e, repoCrew(dir, work("build", "lead"), work("docs", "writer", "build")))
	ctx := context.Background()
	b := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	if _, err := e.SendMessage(ctx, b.Attempt.SessionID, "writer", "carried over"); err != nil {
		t.Fatal(err)
	}
	interrupt(l, run.ID, map[string][2]string{"build/1": {ledger.AttemptInterrupted, "running: the owner stopped"}})
	e2, fl2 := newEngine(t)
	fl2.onLaunch = printOnce
	e2.UseLedger(l)
	next, err := e2.ContinueRun(ctx, run.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if msgs, _ := e2.MessagesOf(next.ID); len(msgs) != 1 || msgs[0].Seq != 1 || msgs[0].DeliveredAt != nil {
		t.Fatalf("the continuation's messages: %+v", msgs)
	}
	b2 := waitAttempt(t, e2, next.ID, "build", ledger.AttemptRunning)
	commitResult(t, e2, b2, "built.txt", "yes\n", "built")
	d := waitAttempt(t, e2, next.ID, "docs", ledger.AttemptRunning)
	_, p := fl2.attemptSession(t, d)
	deadline := time.Now().Add(10 * time.Second)
	for !slices.ContainsFunc(typed(p), func(l string) bool { return strings.Contains(l, "Message from lead (#1): carried over") }) {
		if time.Now().After(deadline) {
			t.Fatalf("the carried message was not typed: %q", typed(p))
		}
		time.Sleep(20 * time.Millisecond)
	}
	waitMessages(t, e2, next.ID, "delivered", func(ms []Message) bool { return len(ms) == 1 && ms[0].DeliveredAt != nil })
	stopRun(t, e2, next.ID)
}
