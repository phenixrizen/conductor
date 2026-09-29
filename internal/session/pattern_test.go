package session

import (
	"bytes"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

func TestStripANSIAndLineTracker(t *testing.T) {
	var lt LineTracker
	lt.Write([]byte("\x1b[32mhello\x1b[0m world\n\x1b]0;title\x07> "))
	if got := lt.Last(); got != "> " {
		t.Fatalf("last %q", got)
	}
	lt.Write([]byte("\rcodex› "))
	if got := lt.Last(); got != "codex› " {
		t.Fatalf("after CR %q", got)
	}
	lt.Write([]byte("\b\bx"))
	if got := lt.Last(); got != "codexx" {
		t.Fatalf("after BS %q", got)
	}
	lt.Write(bytes.Repeat([]byte("a"), 10000))
	if len(lt.Last()) > 4096 {
		t.Fatal("line not capped")
	}
}

func TestStripANSI(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain text", "hello world", "hello world"},
		{"multibyte text", "codex› ", "codex› "},
		{"csi colour", "\x1b[32mhello\x1b[0m world", "hello world"},
		{"csi with parameters and a private marker", "a\x1b[1;38;5;208mb\x1b[?25lc", "abc"},
		{"csi erase and cursor moves", "\x1b[2K\x1b[1G> \x1b[3A", "> "},
		{"osc ended by BEL", "\x1b]0;window title\x07> ", "> "},
		{"osc ended by ST", "\x1b]8;;http://example.test\x1b\\link\x1b]8;;\x1b\\", "link"},
		{"two byte escapes", "\x1b7saved\x1b8 \x1b=\x1b>", "saved "},
		{"charset designation has an intermediate byte", "\x1b(B\x1b[m> ", "> "},
		{"dcs ended by ST", "a\x1bPq#0;2;0;0;0\x1b\\b", "ab"},
		{"apc ended by ST", "a\x1b_Gf=100;AAAA\x1b\\b", "ab"},
		{"a bare BEL is not an escape sequence", "a\x07b", "a\x07b"},
		{"an escape restarts an unfinished csi", "\x1b[31\x1b[32mgreen", "green"},
		{"an escape in an osc that is not ST starts a sequence", "\x1b]0;t\x1b[31mred", "red"},
		{"an unfinished sequence at the end is dropped", "text\x1b[3", "text"},
		{"a lone escape at the end is dropped", "text\x1b", "text"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := string(StripANSI([]byte(c.in))); got != c.want {
				t.Fatalf("StripANSI(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLineTrackerLines(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"nothing written", nil, ""},
		{"text", []string{"hello"}, "hello"},
		{"a newline starts a fresh line", []string{"one\ntwo"}, "two"},
		{"a trailing newline leaves an empty line", []string{"one\n"}, ""},
		{"crlf line ends", []string{"one\r\n> "}, "> "},
		{"a carriage return rewrites from column 0", []string{"| working\r/ working"}, "/ working"},
		{"a rewrite drops what was there even when it is longer", []string{"a long status line\rok"}, "ok"},
		{"backspace deletes the last character", []string{"abc\b"}, "ab"},
		{"backspace deletes a whole multibyte character", []string{"a›\b"}, "a"},
		{"backspace on an empty line does nothing", []string{"\b\bx"}, "x"},
		{"backspace does not cross a newline", []string{"ab\n\bc"}, "c"},
		{"control characters are not text", []string{"a\x07b\x00c\x7f"}, "abc"},
		{"a tab is text", []string{"a\tb"}, "a\tb"},
		{"sequences split across writes", []string{"\x1b", "[3", "2mhel", "lo\x1b]0;ti", "tle\x07 wor", "ld\x1b", "\\!"}, "hello world!"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var lt LineTracker
			for _, ch := range c.chunks {
				lt.Write([]byte(ch))
			}
			if got := lt.Last(); got != c.want {
				t.Fatalf("last %q, want %q", got, c.want)
			}
		})
	}
}

// A line past the cap keeps its end, the part a prompt is at, however the
// stream is cut into writes.
func TestLineTrackerKeepsTheEndOfALongLine(t *testing.T) {
	var lt LineTracker
	var all []byte
	for i := 0; i < 60; i++ {
		chunk := bytes.Repeat([]byte{byte('a' + i%26)}, 777)
		all = append(all, chunk...)
		lt.Write(chunk)
		want := all
		if len(want) > 4096 {
			want = want[len(want)-4096:]
		}
		if got := lt.Last(); got != string(want) {
			t.Fatalf("after %d bytes: len %d, want the last %d bytes", len(all), len(got), len(want))
		}
		if len(lt.line) > 2*maxLine {
			t.Fatalf("after %d bytes the tracker holds %d", len(all), len(lt.line))
		}
	}
	lt.Write([]byte("(y/n) "))
	if got := lt.Last(); len(got) != 4096 || !strings.HasSuffix(got, "(y/n) ") {
		t.Fatalf("prompt at the end of a long line lost: len %d", len(got))
	}
	lt.Write([]byte("\nnext"))
	if got := lt.Last(); got != "next" {
		t.Fatalf("after newline %q", got)
	}
}

func TestPatternWatcherFiresAfterQuiet(t *testing.T) {
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`^> $`), 50*time.Millisecond, func(l string) { fired <- l })
	defer w.Stop()
	w.Feed([]byte("working…\n"))
	w.Feed([]byte("> "))
	select {
	case l := <-fired:
		if l != "> " {
			t.Fatalf("fired with %q", l)
		}
	case <-time.After(time.Second):
		t.Fatal("did not fire")
	}
	// Continuous output (spinner) never goes quiet: no second fire.
	for i := 0; i < 10; i++ {
		w.Feed([]byte("\r| working"))
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case l := <-fired:
		t.Fatalf("fired during spinner: %q", l)
	case <-time.After(120 * time.Millisecond):
	}
}

// The prompt is what the line looked like when the output stopped, not when it
// last matched: a spinner row that matches while output continues never fires.
func TestPatternWatcherWaitsForSilenceBeforeMatching(t *testing.T) {
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`^> $`), 150*time.Millisecond, func(l string) { fired <- l })
	defer w.Stop()
	for i := 0; i < 8; i++ {
		w.Feed([]byte("\r> "))
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case l := <-fired:
		t.Fatalf("fired while output was still arriving: %q", l)
	default:
	}
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("did not fire once the output stopped")
	}
}

// The timer can go off just as a Feed arms it again; the callback then finds
// the stream has not been quiet for long and leaves it to the timer that Feed
// armed.
func TestPatternWatcherIgnoresATimerThatWentOffJustBeforeAFeed(t *testing.T) {
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`> $`), 150*time.Millisecond, func(l string) { fired <- l })
	defer w.Stop()
	w.Feed([]byte("> "))
	w.expire() // the timer's callback, with the last Feed a moment ago
	select {
	case l := <-fired:
		t.Fatalf("fired %q before the quiet period was over", l)
	default:
	}
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("did not fire when the quiet period was over")
	}
}

func TestPatternWatcherIgnoresALastLineThatDoesNotMatch(t *testing.T) {
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`^> $`), 30*time.Millisecond, func(l string) { fired <- l })
	defer w.Stop()
	w.Feed([]byte("> \nthinking"))
	select {
	case l := <-fired:
		t.Fatalf("fired with %q", l)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestPatternWatcherFiresOncePerQuietPeriod(t *testing.T) {
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`\(y/n\) $`), 30*time.Millisecond, func(l string) { fired <- l })
	defer w.Stop()
	w.Feed([]byte("Proceed? (y/n) "))
	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("did not fire")
	}
	select {
	case l := <-fired:
		t.Fatalf("fired again without new output: %q", l)
	case <-time.After(150 * time.Millisecond):
	}
	// New output starts a new quiet period.
	w.Feed([]byte("\rProceed again? (y/n) "))
	select {
	case l := <-fired:
		if l != "Proceed again? (y/n) " {
			t.Fatalf("second fire with %q", l)
		}
	case <-time.After(time.Second):
		t.Fatal("did not fire for the second quiet period")
	}
}

func TestPatternWatcherStopPreventsFires(t *testing.T) {
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`> $`), 30*time.Millisecond, func(l string) { fired <- l })
	w.Feed([]byte("> "))
	w.Stop()
	w.Stop() // more than once is fine
	w.Feed([]byte("> "))
	select {
	case l := <-fired:
		t.Fatalf("fired after Stop: %q", l)
	case <-time.After(150 * time.Millisecond):
	}
}

// Stop returns only once no fire is running, so a caller that stops the watcher
// and then changes state knows no fire will come after; Feed, on the other
// hand, is never held up by a fire that is slow.
func TestPatternWatcherStopWaitsForAFireInFlight(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	w := NewPatternWatcher(regexp.MustCompile(`> $`), 10*time.Millisecond, func(string) {
		entered <- struct{}{}
		<-release
	})
	w.Feed([]byte("> "))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("did not fire")
	}
	fed := make(chan struct{})
	go func() { w.Feed([]byte("more")); close(fed) }()
	select {
	case <-fed:
	case <-time.After(time.Second):
		t.Fatal("Feed waited for the fire in flight")
	}
	stopped := make(chan struct{})
	go func() { w.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a fire was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return once the fire had")
	}
}

// waitAttention returns the session's attention as soon as ok accepts it, or
// fails the test after 3 s.
func waitAttention(t *testing.T, s *Local, ok func(Attention) bool) Attention {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		att := s.Info().Attention
		if ok(att) {
			return att
		}
		if time.Now().After(deadline) {
			t.Fatalf("attention never matched, last %+v", att)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLocalPatternSetsNeedsInputOnce(t *testing.T) {
	t.Parallel()
	var changes []Attention
	var mu sync.Mutex
	p := newFakeProc()
	NewLocal(Info{ID: "p", Cwd: t.TempDir(), Cols: 80, Rows: 24}, p, Options{Pattern: regexp.MustCompile(`\(Y\)es/\(N\)o`), OnChange: func(i Info) { mu.Lock(); changes = append(changes, i.Attention); mu.Unlock() }})
	t.Cleanup(p.exit)
	p.outW.Write([]byte("Add file.go to the chat? (Y)es/(N)o "))
	time.Sleep(700 * time.Millisecond)
	p.outW.Write([]byte("still (Y)es/(N)o "))
	time.Sleep(700 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	n := 0
	for _, a := range changes {
		if a.State == AttentionNeedsInput && a.Source == SourcePattern {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("needs_input from pattern fired %d times, want 1: %+v", n, changes)
	}
}

func TestLocalPatternReportsThePrompt(t *testing.T) {
	t.Parallel()
	s, p := newLocalWith(t, Options{Pattern: regexp.MustCompile(`\(Y\)es/\(N\)o\s*$`)})
	sink := newChanSink(false)
	if _, err := s.Attach("", RoleView, "", 80, 24, sink); err != nil {
		t.Fatal(err)
	}
	// Colour codes around the question do not get in the way of the match.
	p.outW.Write([]byte("\x1b[1mAdd file.go to the chat?\x1b[0m (Y)es/(N)o "))
	att := waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNeedsInput })
	if att.Source != SourcePattern || att.Kind != KindPrompt || att.Message != "prompt: Add file.go to the chat? (Y)es/(N)o" || att.Since == nil {
		t.Fatalf("attention %+v", att)
	}
	m := waitControl(sink, func(m map[string]any) bool { return m["t"] == proto.CtlAttention && m["state"] == "needs_input" })
	if m == nil || m["source"] != "pattern" || m["kind"] != "prompt" {
		t.Fatalf("attached client saw %v", m)
	}
}

func TestLocalPatternFiresAgainForTheNextPrompt(t *testing.T) {
	t.Parallel()
	s, p := newLocalWith(t, Options{Pattern: regexp.MustCompile(`\? $`)})
	sub, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false))
	if err != nil {
		t.Fatal(err)
	}
	p.outW.Write([]byte("First question? "))
	waitAttention(t, s, func(a Attention) bool {
		return a.State == AttentionNeedsInput && a.Message == "prompt: First question?"
	})
	// The answer clears it; the next question raises it again.
	if err := s.Input(sub, []byte("y\n")); err != nil {
		t.Fatal(err)
	}
	if att := s.Info().Attention; att.State != AttentionNone {
		t.Fatalf("typing left %+v", att)
	}
	p.outW.Write([]byte("y\r\nSecond question? "))
	waitAttention(t, s, func(a Attention) bool {
		return a.State == AttentionNeedsInput && a.Message == "prompt: Second question?"
	})
}

func TestLocalPatternDoesNotFireAfterTheSessionEnds(t *testing.T) {
	t.Parallel()
	s, p := newLocalWith(t, Options{Pattern: regexp.MustCompile(`\? $`)})
	p.outW.Write([]byte("Any last words? "))
	p.exit()
	select {
	case <-s.Ended():
	case <-time.After(3 * time.Second):
		t.Fatal("session did not end")
	}
	time.Sleep(800 * time.Millisecond)
	if att := s.Info().Attention; att.State != AttentionNone {
		t.Fatalf("attention %+v on an ended session", att)
	}
}

// A prompt a hook or a bell has raised keeps its own message and source.
func TestLocalPatternLeavesAnExistingNeedsInputAlone(t *testing.T) {
	t.Parallel()
	s, p := newLocalWith(t, Options{Pattern: regexp.MustCompile(`\? $`)})
	s.SetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, nil)
	p.outW.Write([]byte("Allow Bash? "))
	time.Sleep(800 * time.Millisecond)
	if att := s.Info().Attention; att.Source != SourceAPI || att.Message != "Allow Bash?" || att.Kind != KindPermission {
		t.Fatalf("attention %+v", att)
	}
}

func TestLocalWithoutAPatternIgnoresPrompts(t *testing.T) {
	t.Parallel()
	s, p := newLocalWith(t, Options{})
	p.outW.Write([]byte("Add file.go to the chat? (Y)es/(N)o "))
	time.Sleep(800 * time.Millisecond)
	if att := s.Info().Attention; att.State != AttentionNone {
		t.Fatalf("attention %+v without a pattern", att)
	}
}
