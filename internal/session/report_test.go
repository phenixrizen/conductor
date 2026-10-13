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

// Only the terminal's reports, of any kind, mixed and of any number, type
// nothing; a key among them, or after them, is typing; a report a write
// begins and the next finishes types nothing either, and what a write
// begins is carried to the next; the Escape key alone is told by the next
// write.
func TestTypesSomething(t *testing.T) {
	many := strings.Repeat("\x1b[<35;10;10M", 30) // 360 bytes
	for in, want := range map[string]bool{
		"\x1b[<35;10;10M":                 false, // SGR motion
		"\x1b[<0;3;4M\x1b[<0;3;4m":        false, // SGR press and release
		"\x1b[<64;1;1M":                   false, // SGR wheel
		"\x1b[35;10;10M":                  false, // urxvt
		"\x1b[M#**":                       false, // legacy motion
		"\x1b[M\x20\xf0\xf0\x1b[<35;1;1M": false,
		"\x1b[I\x1b[<35;10;10M":           false, // focus in, then the pointer
		"\x1b[3;1R\x1b[O":                 false, // a cursor position report, focus out
		many:                              false,
		many + "\x1b[I":                   false,
		"\x1b[<35;1":                      false, // begun, the rest to come
		"k":                               true,
		"\x15":                            true,
		"\x1b":                            false, // Escape, or a report begun: the next write tells
		"\x1b[A":                          true,  // an arrow key
		"\x1b[<35;10;10Mk":                true,
		many + "k":                        true,
		"\x1b[M\x01**":                    true,
		"\x1b[<35;10;10M\x1bk":            true,
	} {
		if got, _ := typesSomething(nil, []byte(in)); got != want {
			t.Errorf("typesSomething(%q) = %v, want %v", in, got, want)
		}
	}
	typed, rest := typesSomething(nil, []byte("\x1b[I\x1b[<35;1"))
	if typed || string(rest) != "\x1b[<35;1" {
		t.Fatalf("a begun report: %v %q", typed, rest)
	}
	if typed, rest := typesSomething(rest, []byte("0;10M\x1b[<35;11;10M")); typed || rest != nil {
		t.Fatalf("its rest: %v %q", typed, rest)
	}
	if typed, _ := typesSomething([]byte("\x1b[1;5"), []byte("A")); !typed {
		t.Fatal("a key the last write began (Ctrl+Up) types")
	}
	if typed, _ := typesSomething(nil, []byte("0;10M")); !typed {
		t.Fatal("what looks like a report's rest, with none begun, types")
	}
	if typed, _ := typesSomething([]byte("\x1b"), []byte("k")); !typed {
		t.Fatal("Escape, then a key, types")
	}
}

// Every report the terminal sends by itself, cut between two writes at any
// byte, types nothing in either: the first's end is carried, and the second
// finishes it.
func TestAReportCutAnywhereTypesNothing(t *testing.T) {
	for _, r := range []string{
		"\x1b[24;80R", "\x1b[?24;80R", "\x1b[0n", "\x1b[?62;22c", "\x1b[>0;276;0c", "\x1b[?2004;2$y",
		"\x1b[8;24;80t", "\x1b[I", "\x1b[O",
		"\x1b]11;rgb:ffff/ffff/ffff\x07", "\x1b]10;rgb:0000/0000/0000\x1b\\", "\x1b]4;1;rgb:cd00/0000/0000\x07",
		"\x1bP>|xterm(380)\x1b\\", "\x1bP1$r0m\x1b\\",
		"\x1b[<35;10;10M", "\x1b[<0;3;4m", "\x1b[35;10;10M", "\x1b[M#**",
		// Each kind at the longest its grammar takes.
		"\x1b[?62;1;2;4;6;9;15;18;21;22;28c",
		"\x1b[?" + strings.Repeat("6;", 32) + "c", "\x1b[>" + strings.Repeat("1", 64) + "c",
		"\x1b[?9999;9999R", "\x1b[?99999;99$y", "\x1b[99;99999;99999t",
		"\x1b]4;999;rgb:" + strings.Repeat("f/", 32) + "\x07", "\x1b]12;rgb:" + strings.Repeat("a", 64) + "\x1b\\",
		"\x1bP>|$+" + strings.Repeat("x", 128) + "\x1b\\",
		"\x1b[<9999;99999;99999M", "\x1b[9999;99999;99999M",
	} {
		if typed, rest := typesSomething(nil, []byte(r)); typed || rest != nil {
			t.Errorf("%q whole: typed %v, rest %q", r, typed, rest)
		}
		for k := 1; k < len(r); k++ {
			typed, rest := typesSomething(nil, []byte(r[:k]))
			if typed || string(rest) != r[:k] {
				t.Errorf("%q cut at %d: typed %v, rest %q", r, k, typed, rest)
				continue
			}
			if typed, rest := typesSomething(rest, []byte(r[k:])); typed || rest != nil {
				t.Errorf("%q cut at %d, its rest: typed %v, rest %q", r, k, typed, rest)
			}
		}
	}
}
