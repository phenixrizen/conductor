package crew

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

// handoffEntry is the entry of a handoff an agent reports.
func handoffEntry(to, message string) session.ActivityEntry {
	return session.ActivityEntry{Type: session.ActivityHandoff, ByName: "agent", To: to, Message: message}
}

// logCount counts the entries of a run's log whose message holds text.
func logCount(t *testing.T, e *Engine, runID, text string) int {
	t.Helper()
	r, ok := e.Get(runID)
	if !ok {
		t.Fatalf("run %s not found", runID)
	}
	n := 0
	for _, entry := range r.Log {
		if strings.Contains(entry.Message, text) {
			n++
		}
	}
	return n
}

// waitLogged waits up to d until a run's log holds n entries with text.
func waitLogged(t *testing.T, e *Engine, runID, text string, n int, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		got := logCount(t, e, runID, text)
		if got == n {
			return
		}
		if time.Now().After(deadline) {
			r, _ := e.Get(runID)
			t.Fatalf("%d log entries with %q after %v, want %d: %+v", got, text, d, n, r.Log)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runningPair launches lead and tests, both ready at once, and returns the
// run once both are running, with their prompts read.
func runningPair(t *testing.T, e *Engine, fl *fakeLauncher, more ...Member) Run {
	t.Helper()
	fl.onLaunch = askAtOnce
	run, err := e.Launch(t.Context(), testCrew(append([]Member{immediate("lead", "Plan it."), immediate("tests", "Test it.")}, more...)...))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lead", "tests"} {
		waitStatus(t, e, run.ID, name, MemberRunning, 5*time.Second)
		_, p := fl.member(name)
		typed(p)
	}
	return *run
}

// A handoff one member reports is typed into the member it names, as its own
// line, when that member is not waiting for input; while it waits, the
// handoffs queue and are typed in order once it no longer waits.
func TestHandoffTypedOrQueued(t *testing.T) {
	e, fl := newEngine(t)
	run := runningPair(t, e, fl)
	lead, _ := fl.member("lead")
	tests, p := fl.member("tests")

	lead.Record(handoffEntry("tests", "the handlers are in"))
	if got := waitTyped(t, p, 5*time.Second); got != "Handoff from lead: the handlers are in\r" {
		t.Fatalf("typed %q", got)
	}
	waitLogged(t, e, run.ID, "handoff delivered from lead to tests", 1, 5*time.Second)
	if entries := crewEntries(tests); len(entries) != 2 || entries[1].Message != "Handoff from lead: the handlers are in" {
		t.Fatalf("tests records %+v", entries)
	}

	// Waiting on a permission prompt: both wait, noted as they are found
	// waiting.
	tests.SetAttention(session.AttentionNeedsInput, "Allow edit?", session.SourceAPI)
	lead.Record(handoffEntry("tests", "first"))
	lead.Record(handoffEntry("tests", "second"))
	waitLogged(t, e, run.ID, "handoff queued from lead to tests: tests is waiting for input", 2, 5*time.Second)
	// Another prompt is still a prompt.
	tests.SetAttention(session.AttentionNeedsInput, "Allow write?", session.SourceAPI)
	if got := typed(p); len(got) != 0 {
		t.Fatalf("typed %q into a prompt", got)
	}
	if att := tests.Info().Attention; att.State != session.AttentionNeedsInput || att.Message != "Allow write?" {
		t.Fatalf("tests shows %+v", att)
	}

	// Cleared, which records no entry: both are typed, in order.
	tests.SetAttention(session.AttentionNone, "", session.SourceAPI)
	for _, want := range []string{"Handoff from lead: first\r", "Handoff from lead: second\r"} {
		if got := waitTyped(t, p, 5*time.Second); got != want {
			t.Fatalf("typed %q, want %q", got, want)
		}
	}
	waitLogged(t, e, run.ID, "handoff delivered from lead to tests", 3, 5*time.Second)
	if got := typed(p); len(got) != 0 {
		t.Fatalf("typed %q more", got)
	}
}

// A prompt cleared records no entry, only a change: the change alone lets the
// handoffs waiting for the member go, at once.
func TestHandoffDrainsOnAChangeWithoutAnEntry(t *testing.T) {
	e, fl := newEngine(t)
	run := runningPair(t, e, fl)
	lead, _ := fl.member("lead")
	tests, p := fl.member("tests")
	tests.SetAttention(session.AttentionNeedsInput, "Allow edit?", session.SourceAPI)
	lead.Record(handoffEntry("tests", "over to you"))
	// Noted once the handoff was found waiting: from then on only a wake
	// makes it look again.
	waitLogged(t, e, run.ID, "handoff queued from lead to tests: tests is waiting for input", 1, 5*time.Second)
	before := len(tests.Activity())
	tests.SetAttention(session.AttentionNone, "", session.SourceAPI)
	if got := waitTyped(t, p, 2*time.Second); got != "Handoff from lead: over to you\r" {
		t.Fatalf("typed %q", got)
	}
	waitLogged(t, e, run.ID, "handoff delivered from lead to tests", 1, 5*time.Second)
	if after := tests.Activity(); len(after) != before+1 || after[before].Type != session.ActivityInput || after[before].ByName != "crew" {
		t.Fatalf("tests recorded %+v after the clear", after[before:])
	}
}

// At most maxHandoffs wait for a member: another drops the oldest, and the
// run log says so.
func TestHandoffQueueBounded(t *testing.T) {
	e, fl := newEngine(t)
	run := runningPair(t, e, fl)
	lead, _ := fl.member("lead")
	tests, p := fl.member("tests")
	tests.SetAttention(session.AttentionNeedsInput, "Allow edit?", session.SourceAPI)
	for i := 1; i <= 12; i++ {
		lead.Record(handoffEntry("tests", fmt.Sprintf("part %d", i)))
	}
	waitLogged(t, e, run.ID, "handoff dropped from lead to tests: 10 already waiting", 2, 5*time.Second)
	if got := typed(p); len(got) != 0 {
		t.Fatalf("typed %q into a prompt", got)
	}
	tests.SetAttention(session.AttentionNone, "", session.SourceAPI)
	for i := 3; i <= 12; i++ {
		if got, want := waitTyped(t, p, 5*time.Second), fmt.Sprintf("Handoff from lead: part %d\r", i); got != want {
			t.Fatalf("typed %q, want %q", got, want)
		}
	}
	waitLogged(t, e, run.ID, "handoff delivered from lead to tests", 10, 5*time.Second)
	if got := typed(p); len(got) != 0 || logCount(t, e, run.ID, "handoff dropped") != 2 {
		t.Fatalf("typed %q more", got)
	}
}

// A handoff to a name no member has is noted in the run log and typed
// nowhere; one from a session outside any run is ignored.
func TestHandoffUnknownMemberLogged(t *testing.T) {
	e, fl := newEngine(t)
	run := runningPair(t, e, fl)
	lead, pl := fl.member("lead")
	_, pt := fl.member("tests")
	lead.Record(handoffEntry("ghost", "over to you"))
	if n := logCount(t, e, run.ID, `handoff to unknown member "ghost" from lead`); n != 1 {
		t.Fatalf("%d notes", n)
	}
	before, _ := e.Get(run.ID)
	e.OnActivity("not-a-member", handoffEntry("tests", "hello"), "")
	if after, _ := e.Get(run.ID); len(after.Log) != len(before.Log) {
		t.Fatalf("a stranger's handoff was noted: %+v", after.Log[len(before.Log):])
	}
	// The next thing typed into tests is a handoff meant for it.
	lead.Record(handoffEntry("tests", "for you"))
	if got := waitTyped(t, pt, 5*time.Second); got != "Handoff from lead: for you\r" {
		t.Fatalf("typed %q", got)
	}
	if got := typed(pl); len(got) != 0 {
		t.Fatalf("lead was typed %q", got)
	}
}

// A handoff a member addresses to itself is noted and typed nowhere.
func TestHandoffToItselfLogged(t *testing.T) {
	e, fl := newEngine(t)
	run := runningPair(t, e, fl)
	lead, p := fl.member("lead")
	lead.Record(handoffEntry("lead", "note to self"))
	if n := logCount(t, e, run.ID, "handoff to itself from lead"); n != 1 {
		t.Fatalf("%d notes", n)
	}
	if got := typed(p); len(got) != 0 {
		t.Fatalf("lead was typed %q", got)
	}
}

// A handoff is typed as one line: the line breaks and tabs its message keeps
// become spaces.
func TestHandoffIsOneLine(t *testing.T) {
	e, fl := newEngine(t)
	runningPair(t, e, fl)
	lead, _ := fl.member("lead")
	_, p := fl.member("tests")
	lead.Record(handoffEntry("tests", "a\nb\tc"))
	if got := waitTyped(t, p, 5*time.Second); got != "Handoff from lead: a b c\r" {
		t.Fatalf("typed %q", got)
	}
}

// A handoff to a member with no session yet, or whose session has ended, is
// noted as to a member that is not running; so are the handoffs waiting for a
// member whose session ends.
func TestHandoffToAMemberThatIsNotRunning(t *testing.T) {
	e, fl := newEngine(t)
	run := runningPair(t, e, fl, manual("docs", ""))
	lead, _ := fl.member("lead")
	tests, p := fl.member("tests")
	lead.Record(handoffEntry("docs", "write it up"))
	if n := logCount(t, e, run.ID, "handoff to a member that is not running, from lead to docs"); n != 1 {
		t.Fatalf("%d notes", n)
	}

	tests.SetAttention(session.AttentionNeedsInput, "Allow edit?", session.SourceAPI)
	lead.Record(handoffEntry("tests", "one"))
	lead.Record(handoffEntry("tests", "two"))
	waitLogged(t, e, run.ID, "handoff queued from lead to tests", 2, 5*time.Second)
	p.End(0)
	waitLogged(t, e, run.ID, "handoff dropped from lead to tests: tests is not running", 2, 5*time.Second)
	lead.Record(handoffEntry("tests", "three"))
	if n := logCount(t, e, run.ID, "handoff to a member that is not running, from lead to tests"); n != 1 {
		t.Fatalf("%d notes", n)
	}
	if n := logCount(t, e, run.ID, "handoff delivered"); n != 0 {
		t.Fatalf("%d handoffs delivered", n)
	}
}

// A member still waiting for its prompt gets its handoffs after it: the
// prompt is typed first.
func TestHandoffWaitsForThePrompt(t *testing.T) {
	e, fl := newEngine(t)
	release := make(chan struct{})
	e.await = func(ctx context.Context, l *session.Local) error {
		if l.Info().Crew.Member == "tests" {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	run, err := e.Launch(t.Context(), testCrew(immediate("lead", "Plan it."), immediate("tests", "Test it.")))
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, "lead", MemberRunning, 5*time.Second)
	lead, _ := fl.member("lead")
	_, p := fl.member("tests")
	lead.Record(handoffEntry("tests", "the handlers are in"))
	waitLogged(t, e, run.ID, "handoff queued from lead to tests: tests has not had its prompt yet", 1, 5*time.Second)
	if got := typed(p); len(got) != 0 {
		t.Fatalf("typed %q before the prompt", got)
	}
	close(release)
	for _, want := range []string{"Test it.\r", "Handoff from lead: the handlers are in\r"} {
		if got := waitTyped(t, p, 5*time.Second); got != want {
			t.Fatalf("typed %q, want %q", got, want)
		}
	}
}

// Stopping a run drops the handoffs waiting in it, each noted before the run
// is noted stopped, and nothing is typed afterwards.
func TestHandoffsDropWhenTheRunStops(t *testing.T) {
	e, fl := newEngine(t)
	run := runningPair(t, e, fl)
	lead, _ := fl.member("lead")
	tests, p := fl.member("tests")
	tests.SetAttention(session.AttentionNeedsInput, "Allow edit?", session.SourceAPI)
	lead.Record(handoffEntry("tests", "one"))
	lead.Record(handoffEntry("tests", "two"))
	waitLogged(t, e, run.ID, "handoff queued from lead to tests", 2, 5*time.Second)
	if err := e.Stop(t.Context(), run.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Get(run.ID)
	if last := got.Log[len(got.Log)-1]; last.Message != "stopped" || logCount(t, e, run.ID, "handoff dropped from lead to tests: the run is stopped") != 2 {
		t.Fatalf("log %+v", got.Log)
	}
	if n := len(typed(p)); n != 0 {
		t.Fatalf("%d writes after the stop", n)
	}
	// A handoff reported as the run stops is ignored.
	e.OnActivity(lead.Info().ID, handoffEntry("tests", "late"), "")
	if after, _ := e.Get(run.ID); len(after.Log) != len(got.Log) {
		t.Fatalf("noted after the stop: %+v", after.Log[len(got.Log):])
	}
	if !slices.ContainsFunc(got.Log, func(e session.ActivityEntry) bool { return e.Type == session.ActivityError }) {
		t.Fatalf("the drops are not errors: %+v", got.Log)
	}
}
