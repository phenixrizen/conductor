package session

import (
	"bytes"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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
	t.Parallel()
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
	t.Parallel()
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`^> $`), 500*time.Millisecond, func(l string) { fired <- l })
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
	case <-time.After(5 * time.Second):
		t.Fatal("did not fire once the output stopped")
	}
}

// The timer can go off just as a Feed arms it again; the callback then finds
// the stream has not been quiet for long and leaves it to the timer that Feed
// armed.
func TestPatternWatcherIgnoresATimerThatWentOffJustBeforeAFeed(t *testing.T) {
	t.Parallel()
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`> $`), 500*time.Millisecond, func(l string) { fired <- l })
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
	case <-time.After(5 * time.Second):
		t.Fatal("did not fire when the quiet period was over")
	}
}

func TestPatternWatcherIgnoresALastLineThatDoesNotMatch(t *testing.T) {
	t.Parallel()
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`^> $`), 100*time.Millisecond, func(l string) { fired <- l })
	defer w.Stop()
	w.Feed([]byte("> \nthinking"))
	select {
	case l := <-fired:
		t.Fatalf("fired with %q", l)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestPatternWatcherFiresOncePerQuietPeriod(t *testing.T) {
	t.Parallel()
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`\(y/n\) $`), 100*time.Millisecond, func(l string) { fired <- l })
	defer w.Stop()
	w.Feed([]byte("Proceed? (y/n) "))
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("did not fire")
	}
	select {
	case l := <-fired:
		t.Fatalf("fired again without new output: %q", l)
	case <-time.After(500 * time.Millisecond):
	}
	// New output starts a new quiet period.
	w.Feed([]byte("\rProceed again? (y/n) "))
	select {
	case l := <-fired:
		if l != "Proceed again? (y/n) " {
			t.Fatalf("second fire with %q", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not fire for the second quiet period")
	}
}

func TestPatternWatcherStopPreventsFires(t *testing.T) {
	t.Parallel()
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`> $`), 300*time.Millisecond, func(l string) { fired <- l })
	w.Feed([]byte("> "))
	w.Stop()
	w.Stop() // more than once is fine
	w.Feed([]byte("> "))
	select {
	case l := <-fired:
		t.Fatalf("fired after Stop: %q", l)
	case <-time.After(700 * time.Millisecond):
	}
}

// Stop returns only once no fire is running, so a caller that stops the watcher
// and then changes state knows no fire will come after; Feed, on the other
// hand, is never held up by a fire that is slow.
func TestPatternWatcherStopWaitsForAFireInFlight(t *testing.T) {
	t.Parallel()
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

// A fire that takes longer than the quiet period leaves timers going off behind
// it. They all find the stream quiet since the same Feed, and only one of them
// may fire for it.
func TestPatternWatcherFiresOnceForAQuietPeriodEvenWhenAFireIsSlow(t *testing.T) {
	t.Parallel()
	const quiet = 50 * time.Millisecond
	var mu sync.Mutex
	var lines []string
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	w := NewPatternWatcher(regexp.MustCompile(`> $`), quiet, func(l string) {
		mu.Lock()
		lines = append(lines, l)
		first := len(lines) == 1
		mu.Unlock()
		if first {
			entered <- struct{}{}
			<-release
		}
	})
	defer w.Stop()
	w.Feed([]byte("A> "))
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("did not fire")
	}
	// While the first fire is stuck two more quiet periods begin and pass, and
	// their timers go off and wait behind it.
	w.Feed([]byte("\rB> "))
	time.Sleep(5 * quiet)
	w.Feed([]byte("\rC> "))
	time.Sleep(5 * quiet)
	close(release)
	time.Sleep(10 * quiet)
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(lines, []string{"A> ", "C> "}) {
		t.Fatalf("fired for %q, want the first prompt once and the last one once", lines)
	}
}

// An empty line, or one of white space, is never a prompt, whatever the pattern
// matches.
func TestPatternWatcherSkipsALineOfWhiteSpace(t *testing.T) {
	t.Parallel()
	fired := make(chan string, 4)
	w := NewPatternWatcher(regexp.MustCompile(`\s*$`), 100*time.Millisecond, func(l string) { fired <- l })
	defer w.Stop()
	w.Feed([]byte("output\n  \t "))
	select {
	case l := <-fired:
		t.Fatalf("fired with %q", l)
	case <-time.After(500 * time.Millisecond):
	}
	w.Feed([]byte("\r> "))
	select {
	case l := <-fired:
		if l != "> " {
			t.Fatalf("fired with %q", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("did not fire for a line with text")
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

// A prompt a hook or a bell has raised keeps its own message, source, kind and
// options.
func TestLocalPatternLeavesAnExistingNeedsInputAlone(t *testing.T) {
	t.Parallel()
	s, p := newLocalWith(t, Options{Pattern: regexp.MustCompile(`\? $`)})
	options := []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "3"}}
	s.SetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, options)
	before := s.Info().Attention
	p.outW.Write([]byte("Allow Bash? "))
	time.Sleep(800 * time.Millisecond)
	att := s.Info().Attention
	if att.Source != SourceAPI || att.Message != "Allow Bash?" || att.Kind != KindPermission || !reflect.DeepEqual(att.Options, options) || att.Since != before.Since {
		t.Fatalf("attention %+v, was %+v", att, before)
	}
}

// Whether the session is waiting is checked and the mark that it is waiting is
// made in one step. A hook (the admin, a bell) that reports at the same moment,
// on whichever side of the fire, must be what is left, its message, kind and
// options with it. The window is a few instructions wide, so this asks 4000
// times.
func TestLocalPatternFireNeverOverwritesAReportRacingWithIt(t *testing.T) {
	s, _ := newLocalWith(t, Options{Pattern: regexp.MustCompile(`\? $`)})
	options := []Option{{Label: "Yes", Input: "1"}, {Label: "No", Input: "3"}}
	for round := 0; round < 4000; round++ {
		s.SetAttentionFull(AttentionNone, "", SourceInput, "", nil)
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); <-start; s.firePattern("Allow Bash? ") }()
		go func() {
			defer wg.Done()
			<-start
			s.SetAttentionFull(AttentionNeedsInput, "Allow Bash?", SourceAPI, KindPermission, options)
		}()
		close(start)
		wg.Wait()
		if att := s.Info().Attention; att.Source != SourceAPI || att.Kind != KindPermission || !reflect.DeepEqual(att.Options, options) {
			t.Fatalf("round %d: the report was overwritten by the pattern: %+v", round, att)
		}
	}
}

func TestPromptMessage(t *testing.T) {
	cases := []struct{ name, line, want string }{
		{"a short line", "Continue? (y/n) ", "prompt: Continue? (y/n) "},
		{"a line that just fits", strings.Repeat("x", 492), "prompt: " + strings.Repeat("x", 492)},
		{"a long line keeps its end, where the prompt is", strings.Repeat("x", 600) + "> ", "prompt: " + strings.Repeat("x", 490) + "> "},
		{"the cut does not split a character", strings.Repeat("é", 400) + ">", "prompt: " + strings.Repeat("é", 245) + ">"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := promptMessage(c.line)
			if got != c.want {
				t.Fatalf("promptMessage = %.40q...%q (%d bytes), want %.40q...%q (%d bytes)", got, got[max(0, len(got)-20):], len(got), c.want, c.want[max(0, len(c.want)-20):], len(c.want))
			}
			if !utf8.ValidString(got) || CleanMessage(got) != strings.TrimSpace(got) {
				t.Fatalf("the message is cut again by CleanMessage or is not valid UTF-8: %d bytes", len(got))
			}
		})
	}
}

// A line longer than a message can be, and it is the end of it that says what
// the agent waits for.
func TestLocalPatternReportsTheEndOfALongLine(t *testing.T) {
	t.Parallel()
	s, p := newLocalWith(t, Options{Pattern: regexp.MustCompile(`> $`)})
	p.outW.Write([]byte(strings.Repeat("x", 1000) + "> "))
	att := waitAttention(t, s, func(a Attention) bool { return a.State == AttentionNeedsInput })
	if len(att.Message) > MaxAttentionMessage || !strings.HasPrefix(att.Message, "prompt: xxx") || !strings.HasSuffix(att.Message, "xxx>") {
		t.Fatalf("message of %d bytes: %.30q...%q", len(att.Message), att.Message, att.Message[max(0, len(att.Message)-30):])
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
