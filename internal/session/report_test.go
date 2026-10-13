package session

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// quiet is Options for a session under test: no log and, unless set, a short
// pause before a submission's Enter.
func quiet(o Options) Options {
	o.Log = slog.New(slog.DiscardHandler)
	if o.SubmitPause == 0 {
		o.SubmitPause = 30 * time.Millisecond
	}
	return o
}

// nextWrite returns the next write to p, failing after 3 s.
func nextWrite(t *testing.T, p *fakeProc) string {
	t.Helper()
	select {
	case b := <-p.input:
		return string(b)
	case <-time.After(3 * time.Second):
		t.Fatal("nothing written within 3 s")
		return ""
	}
}

// A write that is only a terminal's automatic report reaches the program and
// answers nothing: xterm answering Codex's cursor position query does not
// clear the prompt Codex raised. Typing does.
func TestATerminalReportAnswersNothing(t *testing.T) {
	s, p := newLocalWith(t, quiet(Options{}))
	sub, err := s.Attach("", RoleControl, "", 0, 0, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	s.SetAttention(AttentionNeedsInput, "READY", SourceAPI)
	for _, report := range []string{"\x1b[12;40R", "\x1b[?1;2c", "\x1b[>0;276;0c", "\x1b[0n", "\x1b[I", "\x1b[O",
		"\x1b]11;rgb:1e1e/1e1e/2e2e\x1b\\", "\x1b]10;rgb:ffff/ffff/ffff\a", "\x1bP>|xterm.js(6.0.0)\x1b\\", "\x1b[12;40R\x1b[I"} {
		if err := s.Input(sub, []byte(report)); err != nil {
			t.Fatal(err)
		}
		if got := nextWrite(t, p); got != report {
			t.Fatalf("wrote %q", got)
		}
		if st := s.Info().Attention.State; st != AttentionNeedsInput {
			t.Fatalf("the report %q answered the prompt", report)
		}
	}
	if err := s.Input(sub, []byte("y")); err != nil {
		t.Fatal(err)
	}
	nextWrite(t, p)
	if st := s.Info().Attention.State; st != AttentionNone {
		t.Fatalf("typing did not answer: %q", st)
	}
	for in, want := range map[string]bool{"\x1b[A": false, "\x1b[12;40Ry": false, "y\x1b[12;40R": false, "": false,
		strings.Repeat("\x1b[1;1R", 50): false, strings.Repeat("\x1b[1;1R", 30): true} {
		if got := isTerminalReport([]byte(in)); got != want {
			t.Errorf("isTerminalReport(%q) = %v", in, got)
		}
	}
}

// An ended session needs nothing: the prompt it waited on goes as its status
// becomes exited, in one change, viewers are told, and the activity log keeps
// its history.
func TestAnEndedSessionNeedsNothing(t *testing.T) {
	var mu sync.Mutex
	var seen []Info
	s, p := newLocalWith(t, quiet(Options{OnChange: func(i Info) {
		mu.Lock()
		seen = append(seen, i)
		mu.Unlock()
	}}))
	sink := newChanSink(false)
	if _, err := s.Attach("", RoleView, "", 0, 0, sink); err != nil {
		t.Fatal(err)
	}
	s.SetAttention(AttentionNeedsInput, "Allow edit?", SourceAPI)
	p.exit()
	<-s.Ended()
	info := s.Info()
	if info.Status != StatusExited || info.Attention.State != AttentionNone || info.Attention.Message != "" {
		t.Fatalf("ended: %+v", info)
	}
	mu.Lock()
	for _, i := range seen {
		if i.Status.Ended() && i.Attention.State != AttentionNone {
			t.Fatalf("a change showed an ended session waiting: %+v", i)
		}
	}
	mu.Unlock()
	if m := waitControl(sink, func(m map[string]any) bool { return m["t"] == "attention" && m["state"] == "" }); m == nil {
		t.Fatal("viewers were not told the prompt went")
	}
	found := false
	for _, e := range s.Activity() {
		found = found || (e.Type == ActivityAttention && e.Message == "Allow edit?")
	}
	if !found {
		t.Fatal("the activity log lost the prompt")
	}
}

// A mouse report, alone or with others, in each of the three encodings is
// one; keys, a terminal's own report, or keys after a report are not.
func TestIsMouseReport(t *testing.T) {
	for in, want := range map[string]bool{
		"\x1b[<35;10;10M":                 true, // SGR motion
		"\x1b[<0;3;4M\x1b[<0;3;4m":        true, // SGR press and release
		"\x1b[<64;1;1M":                   true, // SGR wheel
		"\x1b[35;10;10M":                  true, // urxvt
		"\x1b[M#**":                       true, // legacy motion
		"\x1b[M\x20\xf0\xf0\x1b[<35;1;1M": true,
		"":                                false,
		"k":                               false,
		"\x1b[3;1R":                       false, // a cursor position report
		"\x1b[<35;10;10Mk":                false,
		"\x1b[M#*":                        false,
		"\x1b[M\x01**":                    false,
		"\x15":                            false,
	} {
		if got := isMouseReport([]byte(in)); got != want {
			t.Errorf("isMouseReport(%q) = %v, want %v", in, got, want)
		}
	}
}
