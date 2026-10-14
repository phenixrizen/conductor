package session

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// The agent's own question (#193, docs/plans/codex-needs-you.md, Part B):
// a needs_input report that says the agent asks it (Ask: the MCP tool ask,
// conductor notify --ask) shows until it is answered, also once the turn
// it was asked in has ended. The P numbers are the plan's orderings.

// ownLog keeps the attention entries a session hands OnActivity, each as
// "<the state it records> <its words>".
type ownLog struct {
	mu  sync.Mutex
	got []string
}

func (l *ownLog) hook(_ string, e ActivityEntry, st AttentionState) {
	if e.Type != ActivityAttention {
		return
	}
	l.mu.Lock()
	l.got = append(l.got, string(st)+" "+e.Message)
	l.mu.Unlock()
}

// since returns the entries from the n-th on.
func (l *ownLog) since(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.got[min(n, len(l.got)):])
}

func (l *ownLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.got)
}

// asking is a session under test for the agent's own question, with o's
// options, and the log of its attention entries.
func asking(t *testing.T, o Options) (*Local, *fakeProc, *ownLog) {
	t.Helper()
	log := &ownLog{}
	o.OnActivity = log.hook
	if o.InputWait == 0 {
		o.InputWait = 5 * time.Second
	}
	s, p := newLocalWith(t, quiet(o))
	return s, p, log
}

// dbChoices are the choices of the question the tests ask.
var dbChoices = []Option{{Label: "Postgres", Input: "Postgres\r"}, {Label: "SQLite", Input: "SQLite\r"}}

// askOwn asks words as the agent's own question, with choices, and returns
// it as it shows.
func askOwn(t *testing.T, s *Local, words string, choices ...Option) Attention {
	t.Helper()
	kind := ""
	if len(choices) > 0 {
		kind = KindPrompt
	}
	if err := s.TryAsk(words, SourceAPI, kind, choices); err != nil {
		t.Fatal(err)
	}
	a := s.Info().Attention
	if a.State != AttentionNeedsInput || a.Message != words || !a.Asked() || a.ID == "" || a.Since == nil {
		t.Fatalf("asked %q: %+v", words, a)
	}
	return a
}

// reportState reports state with words, as the agent's hook does.
func reportState(t *testing.T, s *Local, state AttentionState, words string) {
	t.Helper()
	kind := ""
	if state == AttentionDone {
		kind = KindDone
	}
	if err := s.TrySetAttentionFull(state, words, SourceAPI, kind, nil); err != nil {
		t.Fatal(err)
	}
}

// showsOwn fails unless the session shows q, the same Attention: its id,
// its Since (the stamp itself, which a reply under way compares), its
// words and choices, marked as the agent's.
func showsOwn(t *testing.T, s *Local, q Attention, what string) {
	t.Helper()
	a := s.Info().Attention
	if a.State != AttentionNeedsInput || a.ID != q.ID || a.Since != q.Since || a.Message != q.Message || !sameOptions(a.Options, q.Options) || !a.Asked() {
		t.Fatalf("%s: attention %+v, want the question %+v", what, a, q)
	}
}

// shows fails unless the session shows state (source too, when given).
func shows(t *testing.T, s *Local, state AttentionState, what string, source ...string) {
	t.Helper()
	a := s.Info().Attention
	if a.State != state || len(source) > 0 && a.Source != source[0] {
		t.Fatalf("%s: attention %+v, want %q %v", what, a, state, source)
	}
}

// keeps reports whether the session keeps an own question, and whether it
// is set aside.
func keeps(s *Local) (kept, aside bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.own.ID != "", s.ownAside
}

// questionLines counts the chat's lines for the question id.
func questionLines(s *Local, id string) int {
	n := 0
	for _, m := range s.ChatHistory() {
		if m.Kind == proto.ChatKindQuestion && m.ID == id {
			n++
		}
	}
	return n
}

// pauseEnter holds the next submission at its Enter, its text typed: at is
// closed once it is there, and release lets it go on.
func pauseEnter(t *testing.T, s *Local) (at <-chan struct{}, release func()) {
	t.Helper()
	reached, gate := make(chan struct{}), make(chan struct{})
	var once, rel sync.Once
	hook := func() { once.Do(func() { close(reached); <-gate }) }
	s.beforeEnter.Store(&hook)
	release = func() { rel.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return reached, release
}

// atEnter waits until the submission paused by pauseEnter is at its Enter.
func atEnter(t *testing.T, at <-chan struct{}) {
	t.Helper()
	select {
	case <-at:
	case <-time.After(5 * time.Second):
		t.Fatal("the submission never came to its Enter")
	}
}

// P1, P7: the question shows through the turn's done, with its id, words
// and choices; the done is recorded with its state (the Events page, a
// webhook, a crew's start-after rule); the turn's own bell after it asks
// nothing; Claude Code's idle_prompt, another done a minute later, changes
// nothing shown and is recorded too. It is in the chat once.
func TestAnAgentsQuestionOutlastsItsTurnsDone(t *testing.T) {
	s, p, log := asking(t, Options{})
	q := askOwn(t, s, "Which database?", dbChoices...)
	reportState(t, s, AttentionDone, "asked about the database")
	showsOwn(t, s, q, "after the turn's done")
	out(p, "\a", "\x1b]9;Codex: turn complete\x07")
	showsOwn(t, s, q, "after the turn's bell and notification")
	reportState(t, s, AttentionDone, "idle")
	showsOwn(t, s, q, "after idle_prompt's done")
	if got, want := log.since(0), []string{"needs_input Which database?", "done asked about the database", "done idle"}; !slices.Equal(got, want) {
		t.Fatalf("entries %q, want %q", got, want)
	}
	if n := questionLines(s, q.ID); n != 1 {
		t.Fatalf("%d chat lines for the question", n)
	}
	// Turns ended: a reply naming it is taken.
	if err := s.questionWaits(q.ID); err != nil {
		t.Fatalf("a reply to it: %v", err)
	}
}

// P2, P3: the agent works on after asking (Claude Code calls a tool,
// Codex's title says Working): working shows and the question is set
// aside; the turn's done shows it again, the same Attention, recorded as
// it shows (needs_input, working, done, needs_input), in the chat once. A
// bell over working (Codex rings its turn-end bell before notify reports
// done) shows, and the done shows the question, not the bell's wait.
func TestAQuestionSetAsideByWorkShowsAgainAtTheDone(t *testing.T) {
	for _, bell := range []bool{false, true} {
		t.Run(fmt.Sprintf("bell %v", bell), func(t *testing.T) {
			s, p, log := asking(t, Options{})
			q := askOwn(t, s, "Which database?", dbChoices...)
			reportState(t, s, AttentionWorking, "")
			shows(t, s, AttentionWorking, "at work after asking")
			if _, aside := keeps(s); !aside {
				t.Fatal("the question is not set aside")
			}
			want := []string{"needs_input Which database?", "working working"}
			if bell {
				out(p, "\a")
				shows(t, s, AttentionNeedsInput, "the bell over working", SourceBell)
				want = append(want, "needs_input terminal bell")
			}
			reportState(t, s, AttentionDone, "done for now")
			showsOwn(t, s, q, "at the turn's done")
			want = append(want, "done done for now", "needs_input Which database?")
			if got := log.since(0); !slices.Equal(got, want) {
				t.Fatalf("entries %q, want %q", got, want)
			}
			if n := questionLines(s, q.ID); n != 1 {
				t.Fatalf("%d chat lines for the question", n)
			}
			// Its answer refers to its own line in the chat.
			owner := ownerOf(t, s)
			if err := s.AnswerInput(context.Background(), owner, q.ID, []byte("Postgres\r")); err != nil {
				t.Fatal(err)
			}
			if got := written(t, p); got != "Postgres\r" {
				t.Fatalf("written %q", got)
			}
			shows(t, s, AttentionNone, "answered")
			var ref string
			for _, m := range s.ChatHistory() {
				if m.Event == "answered" {
					ref = m.Ref
				}
			}
			if ref != q.ID {
				t.Fatalf("the answered line refers to %q, the question is %q", ref, q.ID)
			}
		})
	}
}

// P4, P5: after the done, the question is answered as any: a person's
// keys clear it, with the last answer and the chat's answered line, and
// the next done shows done. Or the agent works again, in a new turn: its
// working report takes the question's place for good, and the next done
// shows done.
func TestAnAnsweredOrWorkedQuestionGoesForGood(t *testing.T) {
	t.Run("answered", func(t *testing.T) {
		s, p, _ := asking(t, Options{})
		owner := ownerOf(t, s)
		askOwn(t, s, "Which database?", dbChoices...)
		reportState(t, s, AttentionDone, "asked")
		if err := s.Input(owner, []byte("Postgres\r")); err != nil {
			t.Fatal(err)
		}
		written(t, p)
		info := s.Info()
		if info.Attention.State != AttentionNone || info.LastAnswer == nil || info.LastAnswer.Message != "Which database?" {
			t.Fatalf("answered: %+v %+v", info.Attention, info.LastAnswer)
		}
		if kept, _ := keeps(s); kept {
			t.Fatal("the answered question is kept")
		}
		reportState(t, s, AttentionWorking, "")
		reportState(t, s, AttentionDone, "chose Postgres")
		shows(t, s, AttentionDone, "the next turn's done")
	})
	t.Run("worked", func(t *testing.T) {
		s, _, _ := asking(t, Options{})
		askOwn(t, s, "Which database?")
		reportState(t, s, AttentionDone, "asked")
		reportState(t, s, AttentionWorking, "")
		shows(t, s, AttentionWorking, "at work in a new turn")
		if kept, _ := keeps(s); kept {
			t.Fatal("the question is kept after the agent worked in a new turn")
		}
		reportState(t, s, AttentionDone, "went on")
		shows(t, s, AttentionDone, "the next done")
	})
}

// P6, P26: Codex's working title read after the turn's done (drawn before
// it) changes nothing: the question stands until an Enter, after which the
// title shows working. A question Codex asks while its title says Working,
// the title written once, gives way to working when its second has passed
// (the settle timer), set aside, and the turn's done shows it.
func TestCodexsTitleAndTheQuestion(t *testing.T) {
	t.Run("after the done", func(t *testing.T) {
		s, p, _ := asking(t, titled(time.Minute, Options{}))
		owner := ownerOf(t, s)
		q := askOwn(t, s, "Which database?")
		reportState(t, s, AttentionDone, "asked")
		out(p, codexWorking(1), codexRedraw, codexWorking(2))
		showsOwn(t, s, q, "a title after the done")
		if err := s.Input(owner, []byte("\r")); err != nil {
			t.Fatal(err)
		}
		written(t, p)
		shows(t, s, AttentionNone, "answered by the Enter")
		out(p, codexWorking(3))
		shows(t, s, AttentionWorking, "the title once the next turn began", SourceTitle)
	})
	t.Run("settled in its turn", func(t *testing.T) {
		s, p, _ := asking(t, titled(150*time.Millisecond, Options{}))
		out(p, codexStill)
		shows(t, s, AttentionWorking, "the turn's title", SourceTitle)
		q := askOwn(t, s, "Which database?")
		waitAttention(t, s, titleWorks)
		if _, aside := keeps(s); !aside {
			t.Fatal("the settled question is not set aside")
		}
		reportState(t, s, AttentionDone, "asked")
		showsOwn(t, s, q, "at the done")
		// The title kept from the turn arms nothing after the done.
		time.Sleep(300 * time.Millisecond)
		showsOwn(t, s, q, "a second after the done")
	})
}

// P8: the bell, an OSC notification and the screen pattern never take the
// question's place, shown or set aside.
func TestBellsAndThePatternLeaveTheQuestion(t *testing.T) {
	s, p, _ := asking(t, Options{Pattern: regexp.MustCompile(`\(Y\)es/\(N\)o\s*$`)})
	q := askOwn(t, s, "Which database?")
	out(p, "\a", "\x1b]9;look here\x07", "\r\nProceed? (Y)es/(N)o ")
	time.Sleep(patternQuiet + 200*time.Millisecond)
	showsOwn(t, s, q, "shown: the bell, a notification, the pattern")
	reportState(t, s, AttentionWorking, "")
	out(p, "\r\nworking\r\nAgain? (Y)es/(N)o ")
	waitAttention(t, s, func(a Attention) bool { return a.Source == SourcePattern })
	if _, aside := keeps(s); !aside {
		t.Fatal("the pattern's prompt took the question set aside away")
	}
	reportState(t, s, AttentionDone, "asked")
	showsOwn(t, s, q, "the done after the pattern's prompt")
}

// P9: the agent's next question takes the first's place; the done keeps
// the second.
func TestTheAgentsNextQuestionReplacesTheFirst(t *testing.T) {
	s, _, _ := asking(t, Options{})
	askOwn(t, s, "Which database?")
	reportState(t, s, AttentionWorking, "")
	second := askOwn(t, s, "Which port?")
	reportState(t, s, AttentionWorking, "")
	reportState(t, s, AttentionDone, "asked twice")
	showsOwn(t, s, second, "the second question at the done")
}

// P10, P11: another wait in the question's turn (a hook's permission
// prompt, a dialog on the screen) shows and sets the question aside: a
// person's keys that answer the wait remove the question too; a wait that
// goes without keys leaves it for the done. After the turn's done, another
// wait takes its place for good.
func TestAnotherWaitAndTheQuestion(t *testing.T) {
	permission := func(t *testing.T, s *Local) {
		t.Helper()
		if err := s.TrySetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, []Option{{Label: "Yes", Input: "1"}}); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("a permission prompt in its turn, gone without keys", func(t *testing.T) {
		s, _, _ := asking(t, Options{})
		q := askOwn(t, s, "Which database?")
		permission(t, s)
		shows(t, s, AttentionNeedsInput, "the permission prompt")
		if s.Info().Attention.Message != "Allow Bash?" {
			t.Fatalf("attention %+v", s.Info().Attention)
		}
		reportState(t, s, AttentionWorking, "") // the agent's reviewer answered it
		reportState(t, s, AttentionDone, "asked")
		showsOwn(t, s, q, "the done")
	})
	t.Run("a permission prompt in its turn, answered by a person", func(t *testing.T) {
		s, p, _ := asking(t, Options{})
		owner := ownerOf(t, s)
		askOwn(t, s, "Which database?")
		permission(t, s)
		if err := s.Input(owner, []byte("1")); err != nil {
			t.Fatal(err)
		}
		written(t, p)
		reportState(t, s, AttentionDone, "asked")
		shows(t, s, AttentionDone, "the done after the person answered the permission")
	})
	t.Run("a dialog on the screen in its turn", func(t *testing.T) {
		s, p, _ := asking(t, Options{Questions: approvalQuestions()})
		q := askOwn(t, s, "Which database?")
		p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
		waitAttention(t, s, asked)
		if _, aside := keeps(s); !aside {
			t.Fatal("the dialog did not set the question aside")
		}
		reportState(t, s, AttentionDone, "asked")
		showsOwn(t, s, q, "the done after a dialog that went without keys")
	})
	t.Run("a dialog in its turn answered by a person", func(t *testing.T) {
		s, p, _ := asking(t, Options{Questions: approvalQuestions()})
		owner := ownerOf(t, s)
		askOwn(t, s, "Which database?")
		p.outW.Write([]byte(approvalDialog("curl -sI https://example.com")))
		waitAttention(t, s, asked)
		if err := s.Input(owner, []byte("y")); err != nil {
			t.Fatal(err)
		}
		written(t, p)
		reportState(t, s, AttentionDone, "asked")
		shows(t, s, AttentionDone, "the done after the person answered the dialog")
	})
	t.Run("a wait after the done", func(t *testing.T) {
		s, p, _ := asking(t, Options{})
		owner := ownerOf(t, s)
		askOwn(t, s, "Which database?")
		reportState(t, s, AttentionDone, "asked")
		permission(t, s)
		if kept, _ := keeps(s); kept {
			t.Fatal("a wait after the done left the question kept")
		}
		if err := s.Input(owner, []byte("1")); err != nil {
			t.Fatal(err)
		}
		written(t, p)
		reportState(t, s, AttentionDone, "went on")
		shows(t, s, AttentionDone, "the next done")
	})
}

// P12, P25: a person's keys while the question is set aside (the agent at
// work, the person typing into the terminal) remove it, as the keys are
// written, though the program takes them late: the done shows done.
func TestAPersonsKeysWhileTheQuestionIsAsideRemoveIt(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(fmt.Sprintf("late %v", late), func(t *testing.T) {
			s, p, _ := asking(t, Options{})
			hold, takeAgain := gateWrites(t, p)
			owner := ownerOf(t, s)
			askOwn(t, s, "Which database?")
			reportState(t, s, AttentionWorking, "")
			if late {
				hold()
			}
			typed := make(chan error, 1)
			go func() { typed <- s.Input(owner, []byte("x")) }()
			if late {
				untilWaiting(t, s, 0)
			} else if err := <-typed; err != nil {
				t.Fatal(err)
			}
			reportState(t, s, AttentionDone, "done")
			shows(t, s, AttentionDone, "the done after the person's keys")
			takeAgain()
			if got := written(t, p); got != "x" {
				t.Fatalf("written %q", got)
			}
			if late {
				if err := <-typed; err != nil {
					t.Fatal(err)
				}
			}
			shows(t, s, AttentionDone, "once the keys landed")
		})
	}
}

// P13, P29: Conductor's own submission (a crew's handoff) typed and
// entered while the question is set aside is no person's answer: the
// question stays aside and the done shows it. An agent that takes the
// handoff after its turn (Claude Code's queued message: working after the
// done) removes it; one that takes it into the same turn (Codex steering)
// shows it at its done until a person answers.
func TestAHandoffEnteredWhileTheQuestionIsAsideLeavesIt(t *testing.T) {
	for _, after := range []bool{true, false} {
		t.Run(fmt.Sprintf("taken after the turn %v", after), func(t *testing.T) {
			s, p, _ := asking(t, Options{})
			q := askOwn(t, s, "Which database?")
			reportState(t, s, AttentionWorking, "")
			res, err := s.Submit(context.Background(), Submission{Text: "Handoff from core: over to you", ByName: "run", UnlessWaiting: true})
			if err != nil || !res.Entered {
				t.Fatalf("the handoff: %+v %v", res, err)
			}
			written(t, p)
			written(t, p)
			if _, aside := keeps(s); !aside {
				t.Fatal("the handoff took the question set aside away")
			}
			reportState(t, s, AttentionDone, "asked")
			showsOwn(t, s, q, "the done after the handoff")
			if after {
				reportState(t, s, AttentionWorking, "")
				reportState(t, s, AttentionDone, "took the handoff")
				shows(t, s, AttentionDone, "the handoff's turn's done")
			} else {
				reportState(t, s, AttentionDone, "idle")
				showsOwn(t, s, q, "until a person answers")
			}
		})
	}
}

// P14: a handoff waits while the question shows after the turn's done
// (UnlessWaiting); once a person answers, it is typed.
func TestAHandoffWaitsForTheQuestionsAnswer(t *testing.T) {
	s, p, _ := asking(t, Options{})
	owner := ownerOf(t, s)
	askOwn(t, s, "Which database?")
	reportState(t, s, AttentionDone, "asked")
	res, err := s.Submit(context.Background(), Submission{Text: "Handoff from core: over to you", ByName: "run", UnlessWaiting: true})
	if err != nil || res.Typed {
		t.Fatalf("the handoff typed into the question: %+v %v", res, err)
	}
	nothingWritten(t, p, 100*time.Millisecond)
	if err := s.Input(owner, []byte("Postgres\r")); err != nil {
		t.Fatal(err)
	}
	written(t, p)
	res, err = s.Submit(context.Background(), Submission{Text: "Handoff from core: over to you", ByName: "run", UnlessWaiting: true})
	if err != nil || !res.Entered {
		t.Fatalf("the handoff once answered: %+v %v", res, err)
	}
}

// P15: the agent's clear (or an admin's) removes the question, shown or
// set aside: the empty state, then the done shows done.
func TestAClearRemovesTheQuestion(t *testing.T) {
	for _, aside := range []bool{false, true} {
		t.Run(fmt.Sprintf("aside %v", aside), func(t *testing.T) {
			s, _, _ := asking(t, Options{})
			askOwn(t, s, "Which database?")
			if aside {
				reportState(t, s, AttentionWorking, "")
			}
			reportState(t, s, AttentionNone, "")
			shows(t, s, AttentionNone, "cleared")
			reportState(t, s, AttentionDone, "found it myself")
			shows(t, s, AttentionDone, "the done after the clear")
		})
	}
}

// P16: the hold of the turn takes the question's place and holds it over;
// the turn's done hidden by the hold is recorded and keeps it held over,
// and so does the agent's work in its turn; keys that end the hold without
// answering it (Ctrl+U) show it again, its id the same, and a reply to it
// waiting for the turn answers it; an Enter answers it. A question set
// aside when the hold began is held over by the done.
func TestAHeldTurnHoldsTheQuestionOver(t *testing.T) {
	for _, c := range []struct {
		name  string
		aside bool
		keys  string
	}{
		{"shown, the text killed", false, "\x15"},
		{"shown, the text entered", false, "\r"},
		{"set aside, the text killed", true, "\x15"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, p, log := asking(t, Options{SubmitPause: 300 * time.Millisecond})
			var linkRevoked atomic.Bool
			guest := linkedController(t, s, newChanSink(false), &linkRevoked)
			owner := ownerOf(t, s)
			q := askOwn(t, s, "Which name?")
			if c.aside {
				reportState(t, s, AttentionWorking, "")
			}
			sub := Submission{Text: "guest line", By: guest}
			if !c.aside {
				sub.Question = q.ID
			}
			done := submitAsync(s, sub)
			if got := written(t, p); got != "guest line" {
				t.Fatalf("written %q", got)
			}
			untilIdle(t, s)
			linkRevoked.Store(true)
			s.DisconnectLink(guest.LinkID)
			if got := <-done; !errors.Is(got.err, ErrRevoked) || !got.res.Typed || got.res.Entered {
				t.Fatalf("the guest's line: %+v, %v", got.res, got.err)
			}
			heldShows(t, s)
			if c.aside {
				// The guest's text, a person's, removed the question set
				// aside: nothing shows again.
				if kept, _ := keeps(s); kept {
					t.Fatal("a person's reply left the question set aside")
				}
				return
			}
			n := log.len()
			reportState(t, s, AttentionWorking, "")
			reportState(t, s, AttentionDone, "asked")
			heldShows(t, s)
			if got, want := log.since(n), []string{"working working", "done asked"}; !slices.Equal(got, want) {
				t.Fatalf("entries under the hold %q, want %q", got, want)
			}
			reply := submitAsync(s, Submission{Text: "Ada", By: owner, Question: q.ID})
			nothingWritten(t, p, 200*time.Millisecond)
			if err := s.Input(owner, []byte(c.keys)); err != nil {
				t.Fatal(err)
			}
			if got := written(t, p); got != c.keys {
				t.Fatalf("written %q", got)
			}
			got := returnsWithin(t, 5*time.Second, "the reply", func() submitted { return <-reply })
			if c.keys == "\r" {
				if !errors.Is(got.err, ErrQuestionAnswered) {
					t.Fatalf("the reply after the Enter: %+v, %v", got.res, got.err)
				}
				heldCleared(t, s)
				if kept, _ := keeps(s); kept {
					t.Fatal("the question answered by the Enter is kept")
				}
				return
			}
			for _, want := range []string{"Ada", "\r"} {
				if w := written(t, p); w != want {
					t.Fatalf("written %q, want %q", w, want)
				}
			}
			if got.err != nil || !got.res.Entered {
				t.Fatalf("the reply: %+v, %v", got.res, got.err)
			}
			shows(t, s, AttentionNone, "answered by the reply")
		})
	}
}

// A question set aside when the hold began, Conductor's own text held
// (its link let go), is held over by the turn's done, and shows once a
// person's keys end the hold without answering it.
func TestAQuestionAsideIsHeldOverByTheDoneUnderTheHold(t *testing.T) {
	s, p, _ := asking(t, Options{})
	owner := ownerOf(t, s)
	q := askOwn(t, s, "Which name?")
	reportState(t, s, AttentionWorking, "")
	go s.holdTyped()
	heldShows(t, s)
	reportState(t, s, AttentionDone, "asked")
	heldShows(t, s)
	if err := s.Input(owner, []byte{0x15}); err != nil {
		t.Fatal(err)
	}
	written(t, p)
	showsOwn(t, s, q, "once the hold ended")
}

// P17: the session's end in any of the above: the empty state, nothing set
// aside, nothing shown again.
func TestTheSessionsEndDropsTheQuestion(t *testing.T) {
	for _, aside := range []bool{false, true} {
		t.Run(fmt.Sprintf("aside %v", aside), func(t *testing.T) {
			s, p, _ := asking(t, Options{})
			askOwn(t, s, "Which database?")
			if aside {
				reportState(t, s, AttentionWorking, "")
			} else {
				reportState(t, s, AttentionDone, "asked")
			}
			p.exit()
			<-s.Ended()
			shows(t, s, AttentionNone, "ended")
			if kept, _ := keeps(s); kept {
				t.Fatal("an ended session keeps the question")
			}
			s.SetAttentionFull(AttentionDone, "late", SourceAPI, KindDone, nil)
			if a := s.Info().Attention; a.State == AttentionNeedsInput {
				t.Fatalf("the question shows after the end: %+v", a)
			}
		})
	}
}

// P18: a report the event bucket refuses changes nothing: the question
// stays where it was, shown or set aside.
func TestARefusedReportLeavesTheQuestionWhereItWas(t *testing.T) {
	s, _, _ := asking(t, Options{})
	q := askOwn(t, s, "Which database?")
	drainBucket(s)
	if err := s.TrySetAttentionFull(AttentionWorking, "", SourceAPI, "", nil); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("working on an empty bucket: %v", err)
	}
	showsOwn(t, s, q, "a refused working")
	s.mu.Lock()
	s.events = EventBucket{}
	s.mu.Unlock()
	reportState(t, s, AttentionWorking, "")
	drainBucket(s)
	if err := s.TrySetAttentionFull(AttentionDone, "", SourceAPI, KindDone, nil); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("done on an empty bucket: %v", err)
	}
	shows(t, s, AttentionWorking, "a refused done")
	if _, aside := keeps(s); !aside {
		t.Fatal("a refused done moved the question")
	}
	s.mu.Lock()
	s.events = EventBucket{}
	s.mu.Unlock()
	reportState(t, s, AttentionDone, "asked")
	showsOwn(t, s, q, "the done once the bucket refilled")
}

// P19, P27: a needs_input report without the mark (a hook's permission
// prompt; OpenCode's and DeepSeek Harness's permission waits, aider's)
// gives way to done as before.
func TestAWaitWithoutTheMarkGivesWayToDone(t *testing.T) {
	s, _, _ := asking(t, Options{})
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "OpenCode asks for permission", SourceAPI, "", nil); err != nil {
		t.Fatal(err)
	}
	if s.Info().Attention.Asked() {
		t.Fatal("a report without the mark is marked")
	}
	reportState(t, s, AttentionDone, "finished")
	shows(t, s, AttentionDone, "the done over a wait without the mark")
	// Reported again with the mark, the same words are the agent's own.
	if err := s.TrySetAttentionFull(AttentionNeedsInput, "Which port?", SourceAPI, "", nil); err != nil {
		t.Fatal(err)
	}
	q := s.Info().Attention
	if err := s.TryAsk("Which port?", SourceAPI, "", nil); err != nil {
		t.Fatal(err)
	}
	if a := s.Info().Attention; a.ID != q.ID || !a.Asked() {
		t.Fatalf("the same words asked: %+v", a)
	}
	reportState(t, s, AttentionDone, "asked")
	if a := s.Info().Attention; a.ID != q.ID || a.State != AttentionNeedsInput {
		t.Fatalf("the done after the mark: %+v", a)
	}
}

// P20: an answer against the done, and working against the done, at once,
// many times: each run ends as the sequential rules say, and the question
// never shows after its answer.
func TestRacesWithTheDoneEndAsTheSequentialRulesSay(t *testing.T) {
	for i := range 40 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			s, p, _ := asking(t, Options{})
			owner := ownerOf(t, s)
			q := askOwn(t, s, "Which database?")
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				if err := s.Input(owner, []byte("x")); err != nil {
					t.Error(err)
				}
			}()
			go func() {
				defer wg.Done()
				<-start
				s.SetAttentionFull(AttentionDone, "asked", SourceAPI, KindDone, nil)
			}()
			close(start)
			wg.Wait()
			written(t, p)
			// Answered then done: done; done then answered: the empty state.
			if a := s.Info().Attention; a.State != AttentionDone && a.State != AttentionNone {
				t.Fatalf("an answer against the done: %+v", a)
			}
			if kept, _ := keeps(s); kept {
				t.Fatal("the answered question is kept")
			}
			_ = q

			s2, _, _ := asking(t, Options{})
			q2 := askOwn(t, s2, "Which port?")
			start2 := make(chan struct{})
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start2
				s2.SetAttentionFull(AttentionWorking, "", SourceAPI, "", nil)
			}()
			go func() {
				defer wg.Done()
				<-start2
				s2.SetAttentionFull(AttentionDone, "asked", SourceAPI, KindDone, nil)
			}()
			close(start2)
			wg.Wait()
			// Working then done: the question; done then working: working,
			// the question gone.
			a := s2.Info().Attention
			kept, _ := keeps(s2)
			switch {
			case a.State == AttentionNeedsInput && a.ID == q2.ID && a.Since == q2.Since && kept:
			case a.State == AttentionWorking && !kept:
			default:
				t.Fatalf("working against the done: %+v (kept %v)", a, kept)
			}
		})
	}
}

// P23: a reply under way (its text typed, the question claimed) when the
// turn's done comes: the done changes nothing shown, so the reply's Enter
// is written and answers the question, its Since unchanged.
func TestAReplyUnderWayAtTheDoneKeepsItsEnter(t *testing.T) {
	s, p, _ := asking(t, Options{})
	owner := ownerOf(t, s)
	q := askOwn(t, s, "Which database?", dbChoices...)
	at, release := pauseEnter(t, s)
	reply := submitAsync(s, Submission{Text: "Postgres", By: owner, Question: q.ID})
	if got := written(t, p); got != "Postgres" {
		t.Fatalf("written %q", got)
	}
	atEnter(t, at)
	reportState(t, s, AttentionDone, "asked")
	showsOwn(t, s, q, "the done while the reply is under way")
	release()
	if got := written(t, p); got != "\r" {
		t.Fatalf("written %q, want the reply's Enter", got)
	}
	if got := <-reply; got.err != nil || !got.res.Entered {
		t.Fatalf("the reply: %+v, %v", got.res, got.err)
	}
	info := s.Info()
	if info.Attention.State != AttentionNone || info.LastAnswer == nil || info.LastAnswer.Message != "Which database?" {
		t.Fatalf("the reply did not answer: %+v %+v", info.Attention, info.LastAnswer)
	}
}

// P24: a reply under way when the agent works on, then its done: the
// question the reply claimed is removed by the work, not set aside; the
// Enter is written; the done shows done. The question never shows after
// its reply.
func TestAQuestionAReplyClaimedIsNeverSetAside(t *testing.T) {
	s, p, _ := asking(t, Options{})
	owner := ownerOf(t, s)
	q := askOwn(t, s, "Which database?", dbChoices...)
	at, release := pauseEnter(t, s)
	reply := submitAsync(s, Submission{Text: "Postgres", By: owner, Question: q.ID})
	written(t, p)
	atEnter(t, at)
	reportState(t, s, AttentionWorking, "")
	if kept, _ := keeps(s); kept {
		t.Fatal("the claimed question was set aside")
	}
	release()
	if got := written(t, p); got != "\r" {
		t.Fatalf("written %q, want the reply's Enter", got)
	}
	if got := <-reply; got.err != nil || !got.res.Entered {
		t.Fatalf("the reply: %+v, %v", got.res, got.err)
	}
	reportState(t, s, AttentionDone, "chose Postgres")
	shows(t, s, AttentionDone, "the done after the reply")
}

// P28: a handoff typed while the question is set aside, its Enter after
// the turn's done, which shows the question again: the Enter is written
// all the same (the handoff entered, never left typed), it answers
// nothing, and it begins the next turn, in which the agent's work removes
// the question; without that work, a person's reply answers it, typed on
// its own.
func TestAHandoffTypedWhileTheQuestionIsAsideIsEntered(t *testing.T) {
	for _, works := range []bool{true, false} {
		t.Run(fmt.Sprintf("the agent works %v", works), func(t *testing.T) {
			s, p, _ := asking(t, Options{})
			owner := ownerOf(t, s)
			q := askOwn(t, s, "Which database?", dbChoices...)
			reportState(t, s, AttentionWorking, "")
			at, release := pauseEnter(t, s)
			handoff := submitAsync(s, Submission{Text: "Handoff from core: over to you", ByName: "run", UnlessWaiting: true})
			if got := written(t, p); got != "Handoff from core: over to you" {
				t.Fatalf("written %q", got)
			}
			atEnter(t, at)
			reportState(t, s, AttentionDone, "asked")
			showsOwn(t, s, q, "the done with the handoff's Enter to come")
			release()
			if got := written(t, p); got != "\r" {
				t.Fatalf("written %q, want the handoff's Enter", got)
			}
			if got := <-handoff; got.err != nil || !got.res.Entered {
				t.Fatalf("the handoff: %+v, %v", got.res, got.err)
			}
			showsOwn(t, s, q, "the handoff's Enter answered nothing")
			if s.Info().LastAnswer != nil {
				t.Fatalf("the handoff answered: %+v", s.Info().LastAnswer)
			}
			if works {
				reportState(t, s, AttentionWorking, "")
				if kept, _ := keeps(s); kept {
					t.Fatal("the work on the handoff left the question kept")
				}
				reportState(t, s, AttentionDone, "took the handoff")
				shows(t, s, AttentionDone, "the handoff's turn's done")
				return
			}
			res, err := s.Submit(context.Background(), Submission{Text: "Postgres", By: owner, Question: q.ID})
			if err != nil || !res.Entered {
				t.Fatalf("the reply: %+v %v", res, err)
			}
			for _, want := range []string{"Postgres", "\r"} {
				if got := written(t, p); got != want {
					t.Fatalf("written %q, want %q", got, want)
				}
			}
			shows(t, s, AttentionNone, "answered")
		})
	}
}

// A person's reply typed while the question is set aside (a reply box, the
// agent at work) removes it: the person has taken part, and the done shows
// done; its Enter is written.
func TestAPersonsReplyWhileTheQuestionIsAsideRemovesIt(t *testing.T) {
	s, p, _ := asking(t, Options{})
	owner := ownerOf(t, s)
	askOwn(t, s, "Which database?")
	reportState(t, s, AttentionWorking, "")
	at, release := pauseEnter(t, s)
	reply := submitAsync(s, Submission{Text: "use Postgres", By: owner})
	written(t, p)
	atEnter(t, at)
	if kept, _ := keeps(s); kept {
		t.Fatal("the person's reply left the question set aside")
	}
	reportState(t, s, AttentionDone, "asked")
	shows(t, s, AttentionDone, "the done after the person's reply")
	release()
	if got := written(t, p); got != "\r" {
		t.Fatalf("written %q", got)
	}
	if got := <-reply; got.err != nil || !got.res.Entered {
		t.Fatalf("the reply: %+v, %v", got.res, got.err)
	}
}
