package nvim

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The command line through the bridge (the owner, 2026-10-13): Ex command
// lines typed a key at a time, as the page sends them, each key numbered,
// work as in Neovim. A substitute once lost Neovim at its replacement's
// first key: Neovim's preview ('inccommand') reports lines with no
// changedtick, which the client could not read, and it ended its
// connection.

// cmdlineEditor opens the real Neovim on a file holding text, its state in
// temporary folders, with initLua (when not empty) as the person's config.
func cmdlineEditor(t *testing.T, text, initLua string) (*Editor, *recorder) {
	t.Helper()
	if !Available() {
		t.Skip("nvim is not on PATH; the bridge's test needs the real Neovim")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := t.TempDir()
	if initLua != "" {
		if err := os.MkdirAll(filepath.Join(cfg, "nvim"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cfg, "nvim", "init.lua"), []byte(initLua), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", cfg)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &recorder{done: make(chan struct{})}
	e, err := Open(context.Background(), dir, "sample.txt", r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e, r
}

// typist sends keys one at a time with their numbers, as the page does.
type typist struct {
	t   *testing.T
	e   *Editor
	r   *recorder
	seq uint32
}

// key sends one key and returns its number.
func (k *typist) key(key string) uint32 {
	k.t.Helper()
	k.seq++
	if err := k.e.InputSeq(key, k.seq); err != nil {
		k.r.mu.Lock()
		defer k.r.mu.Unlock()
		k.t.Fatalf("key %q: %v; the events: %q", key, err, k.r.events)
	}
	return k.seq
}

// cmdline types a command line a key at a time: each key shows on it, the
// line as typed so far.
func (k *typist) cmdline(keys ...string) {
	k.t.Helper()
	shown := ""
	for _, key := range keys {
		k.key(key)
		shown += key
		k.r.waitExact(k.t, "cmdline "+shown)
	}
}

// run presses Enter on the command line and waits for Neovim to have run
// it: its acknowledgement, with the cursor where it then is (at, "line:col",
// or "" for anywhere).
func (k *typist) run(at string) {
	k.t.Helper()
	enter := k.key("<CR>")
	k.r.wait(k.t, fmt.Sprintf("ack %d %s", enter, at))
}

func (r *recorder) waitExact(t *testing.T, ev string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		r.mu.Lock()
		for _, e := range r.events {
			if e == ev {
				r.mu.Unlock()
				return
			}
		}
		r.mu.Unlock()
		time.Sleep(25 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("no %q among %q", ev, r.events)
}

func (r *recorder) changesNow() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.changes...)
}

func bufferIs(t *testing.T, e *Editor, want ...string) {
	t.Helper()
	got, err := e.Lines()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the buffer %q, want %q", got, want)
	}
}

func chars(s string) []string {
	out := make([]string, 0, len(s))
	for _, c := range s {
		out = append(out, string(c))
	}
	return out
}

// The owner's command line: :%s/foo/bar/g typed a key at a time shows every
// key as typed, runs on Enter, and the page is told of that change alone
// (the preview Neovim would show on the way is not one); u undoes it.
func TestTheCommandLineTakesASubstitute(t *testing.T) {
	e, r := cmdlineEditor(t, "foo\n", "")
	k := &typist{t: t, e: e, r: r}
	k.cmdline(chars(":%s/foo/bar/g")...)
	k.run("1:1 n")
	r.waitExact(t, "cmdline hidden")
	bufferIs(t, e, "bar")
	if got, want := r.changesNow(), []string{`0:1 ["bar"]`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the page was told of %q, want %q", got, want)
	}
	undo := k.key("u")
	r.wait(t, fmt.Sprintf("ack %d ", undo))
	bufferIs(t, e, "foo")
	if got, want := r.changesNow(), []string{`0:1 ["bar"]`, `0:1 ["foo"]`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the page was told of %q, want %q", got, want)
	}
	if r.has("exited") {
		t.Fatal("the editor ended")
	}
}

// A substitute's preview is never the page's text: the person's config asks
// for one ('inccommand' split) and the editor has none; one turned on again
// later (as a plugin would) shows in Neovim alone, never as a change. Esc on
// a half-typed substitute leaves the buffer as it was, not modified; Enter
// on a whole one reports its change once.
func TestASubstitutesPreviewIsNeverAChange(t *testing.T) {
	e, r := cmdlineEditor(t, "foo one\nfoo two foo\nbaz\n", "vim.o.inccommand = 'split'\n")
	var icm string
	if err := e.v.Eval("&inccommand", &icm); err != nil {
		t.Fatal(err)
	}
	if icm != "" {
		t.Fatalf("inccommand is %q in the editor's Neovim", icm)
	}
	k := &typist{t: t, e: e, r: r}
	k.cmdline(chars(":%s/foo/x")...)
	esc := k.key("<Esc>")
	r.wait(t, fmt.Sprintf("ack %d 1:1 n", esc))
	bufferIs(t, e, "foo one", "foo two foo", "baz")
	if got := r.changesNow(); len(got) != 0 {
		t.Fatalf("a half-typed substitute told the page of %q", got)
	}

	// A preview turned on again: Neovim shows it, the page is told nothing.
	if err := e.v.Command("set inccommand=nosplit"); err != nil {
		t.Fatal(err)
	}
	k.cmdline(chars(":%s/foo/x")...)
	esc = k.key("<Esc>")
	r.wait(t, fmt.Sprintf("ack %d 1:1 n", esc))
	bufferIs(t, e, "foo one", "foo two foo", "baz")
	if got := r.changesNow(); len(got) != 0 {
		t.Fatalf("a preview told the page of %q", got)
	}
	if r.has("modified true") {
		t.Fatal("a preview marked the buffer modified")
	}
	if e.previews.Load() == 0 {
		t.Fatal("Neovim showed no preview: the test proves nothing")
	}
	k.cmdline(chars(":%s/foo/bar/g")...)
	k.run("")
	bufferIs(t, e, "bar one", "bar two bar", "baz")
	if got, want := r.changesNow(), []string{`0:2 ["bar one" "bar two bar"]`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the page was told of %q, want %q", got, want)
	}
	undo := k.key("u")
	r.wait(t, fmt.Sprintf("ack %d ", undo))
	bufferIs(t, e, "foo one", "foo two foo", "baz")
}

// :%s/a/b/gc asks at each match; the answers (y, n, y), keys like any
// other, are taken; the question shows while it is asked and goes once it
// is answered (Neovim leaves its last one shown with nothing saying so); u
// undoes the answers.
func TestASubstitutesConfirmPromptTakesItsAnswersAndGoes(t *testing.T) {
	e, r := cmdlineEditor(t, "a a\na\n", "")
	k := &typist{t: t, e: e, r: r}
	k.cmdline(chars(":%s/a/b/gc")...)
	k.key("<CR>")
	r.wait(t, "message confirm_sub:replace with b (y/n/a/q/l/^E/^Y)?")
	k.key("y")
	k.key("n")
	last := k.key("y")
	r.wait(t, fmt.Sprintf("ack %d 2:1 n", last))
	bufferIs(t, e, "b a", "b")
	r.mu.Lock()
	var message string
	for _, ev := range r.events {
		if strings.HasPrefix(ev, "message ") {
			message = ev
		}
		if strings.HasPrefix(ev, fmt.Sprintf("ack %d ", last)) {
			break
		}
	}
	r.mu.Unlock()
	if message != "message clear:" {
		t.Fatalf("the last message as the answers were taken: %q", message)
	}
	// Each answer is a change of its own in Neovim's undo, as in its own
	// window: u undoes the last, a second u the first.
	undo := k.key("u")
	r.wait(t, fmt.Sprintf("ack %d ", undo))
	bufferIs(t, e, "b a", "a")
	undo = k.key("u")
	r.wait(t, fmt.Sprintf("ack %d ", undo))
	bufferIs(t, e, "a a", "a")
}

// The other command lines: :s with a \/ in its pattern, / and ? searches
// (and n), :g/foo/d, each typed a key at a time and shown as typed.
func TestCommandLinesWorkAsInNeovim(t *testing.T) {
	e, r := cmdlineEditor(t, "a/b\nfoo\nbar\nfoo\n", "")
	k := &typist{t: t, e: e, r: r}
	k.cmdline(chars(`:s/a\/b/c/`)...)
	k.run("1:1 n")
	bufferIs(t, e, "c", "foo", "bar", "foo")
	k.cmdline(chars("/foo")...)
	k.run("2:1 n")
	next := k.key("n")
	r.wait(t, fmt.Sprintf("ack %d 4:1 n", next))
	k.cmdline(chars("?foo")...)
	k.run("2:1 n")
	k.cmdline(chars(":g/foo/d")...)
	k.run("")
	bufferIs(t, e, "c", "bar")
	undo := k.key("u")
	r.wait(t, fmt.Sprintf("ack %d ", undo))
	bufferIs(t, e, "c", "foo", "bar", "foo")
	if r.has("exited") {
		t.Fatal("the editor ended")
	}
}

// A Neovim that dies is told at once, with no key needed, and the changes it
// sent before still reach the handler, though the client may deliver them
// after the end (slowly here: its notifications run on a goroutine of their
// own, which says nothing once it has run them all). The page keeps the text
// a lost Neovim last held, so none may be dropped. A key after the end finds
// it gone and tells nothing more.
func TestANeovimThatDiesIsToldAtOnceAndItsChangesStillCome(t *testing.T) {
	for _, key := range []bool{false, true} {
		t.Run(fmt.Sprintf("a key after the end %v", key), func(t *testing.T) {
			e, r := cmdlineEditor(t, "one\n", "")
			var pid int
			if err := e.v.Eval("getpid()", &pid); err != nil {
				t.Fatal(err)
			}
			k := &typist{t: t, e: e, r: r}
			k.key("A")
			r.wait(t, "ack 1 ")
			r.mu.Lock()
			r.linesDelay = 300 * time.Millisecond
			r.mu.Unlock()
			k.key("x")
			k.key("y")
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
				t.Fatal(err)
			}
			if key {
				if err := e.InputSeq("z", 99); err != ErrClosed {
					t.Fatalf("a key after the end: %v", err)
				}
			}
			select {
			case <-r.done:
			case <-time.After(10 * time.Second):
				r.mu.Lock()
				defer r.mu.Unlock()
				t.Fatalf("no end told; the events: %q", r.events)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				r.mu.Lock()
				n := len(r.changes)
				last := ""
				if n > 0 {
					last = r.changes[n-1]
				}
				r.mu.Unlock()
				if last == `0:1 ["onexy"]` {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("the last change never came: %q", last)
				}
				time.Sleep(20 * time.Millisecond)
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			n := 0
			for _, ev := range r.events {
				if strings.HasPrefix(ev, "exited") {
					n++
					if !strings.HasPrefix(ev, "exited error: ") {
						t.Fatalf("the end: %q", ev)
					}
				}
			}
			if n != 1 {
				t.Fatalf("%d ends told: %q", n, r.events)
			}
		})
	}
}

// Keys are told settled once Neovim has done them, with nothing pending,
// after the changes they made; their acknowledgement can come before those
// changes (here a plugin's vim.wait in InsertCharPre lets Neovim answer in
// the middle of the key), so the page waits for this word before it takes a
// tab's Neovim for clean.
func TestKeysAreSettledAfterTheirChangesThoughAcknowledgedBefore(t *testing.T) {
	e, r := cmdlineEditor(t, "one\n", "vim.api.nvim_create_autocmd('InsertCharPre', { callback = function() vim.wait(800) end })\n")
	k := &typist{t: t, e: e, r: r}
	k.key("A")
	r.waitExact(t, "settled 1")
	x := k.key("x")
	r.waitExact(t, fmt.Sprintf("settled %d", x))
	r.mu.Lock()
	defer r.mu.Unlock()
	ack, lines, settled := -1, -1, -1
	for i, ev := range r.events {
		switch {
		case strings.HasPrefix(ev, fmt.Sprintf("ack %d ", x)):
			ack = i
		case ev == "lines":
			lines = i
		case ev == fmt.Sprintf("settled %d", x):
			settled = i
		}
	}
	if !(ack >= 0 && ack < lines && lines < settled) {
		t.Fatalf("want the acknowledgement before the change and settled after it: %q", r.events)
	}
	if got := r.changes[len(r.changes)-1]; got != `0:1 ["onex"]` {
		t.Fatalf("the change: %q", got)
	}
}

// A key that leaves Neovim waiting for the next (j, the prefix of a jj
// mapping) is not acknowledged then, since asking would cancel the wait;
// once the mapping times out and j is done, with no key after it, it is
// acknowledged and settled all the same.
func TestAKeyThatWaitsForAMappingIsSettledOnceItTimesOut(t *testing.T) {
	e, r := cmdlineEditor(t, "one\ntwo\n", "vim.o.timeoutlen = 300\nvim.keymap.set('n', 'jj', 'j')\n")
	k := &typist{t: t, e: e, r: r}
	j := k.key("j")
	r.waitExact(t, fmt.Sprintf("settled %d", j))
	r.wait(t, fmt.Sprintf("ack %d 2:1 n", j))
}

// A numbered key that opens a question (:confirm q's Enter) waits with it;
// the page's own answer, sent with no number (its Cancel button), ends the
// wait and acknowledges that key, so it settles without another key.
func TestAQuestionAnsweredWithoutANumberSettlesTheKeyThatAskedIt(t *testing.T) {
	e, r := cmdlineEditor(t, "one\n", "")
	k := &typist{t: t, e: e, r: r}
	k.key("A")
	k.key("x")
	esc := k.key("<Esc>")
	r.waitExact(t, fmt.Sprintf("settled %d", esc))
	k.cmdline(chars(":confirm q")...)
	enter := k.key("<CR>")
	r.wait(t, "message confirm:")
	if err := e.Input("c"); err != nil {
		t.Fatal(err)
	}
	r.waitExact(t, fmt.Sprintf("settled %d", enter))
	if r.has("exited") {
		t.Fatal("Cancel ended the editor")
	}
}

// Neovim ended by a signal (SIGTERM, SIGHUP) runs VimLeavePre too, with
// v:dying set: it is told as a loss, not as the person's quit, so its tab is
// kept with what it held.
func TestNeovimEndedByASignalIsALossNotAQuit(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			e, r := cmdlineEditor(t, "one\n", "")
			var pid int
			if err := e.v.Eval("getpid()", &pid); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Kill(pid, sig); err != nil {
				t.Fatal(err)
			}
			select {
			case <-r.done:
			case <-time.After(10 * time.Second):
				t.Fatal("no end told")
			}
			r.mu.Lock()
			defer r.mu.Unlock()
			for _, ev := range r.events {
				if strings.HasPrefix(ev, "exited") && ev != "exited error: neovim was ended by a signal" {
					t.Fatalf("the end: %q", ev)
				}
			}
		})
	}
}

// Keys sent faster than Neovim reads them fill its input buffer, which then
// takes only part of what it is given: the rest is given again once it has
// room, and keys it still has no room for are refused (ErrInputFull), never
// taken for sent. Here a plugin's vim.wait in InsertCharPre lets Neovim
// answer between the keys it reads, slowly, so the buffer fills.
func TestKeysNeovimHasNoRoomForAreRefusedNotDropped(t *testing.T) {
	e, r := cmdlineEditor(t, "\n", "vim.api.nvim_create_autocmd('InsertCharPre', { callback = function() vim.wait(20) end })\n")
	k := &typist{t: t, e: e, r: r}
	k.key("i")
	r.waitExact(t, "settled 1")
	keys := strings.Repeat("x", 250)
	sent, full := 0, false
	for i := 0; i < 200 && !full; i++ {
		switch err := e.InputSeq(keys, uint32(i+2)); err {
		case nil:
			sent += len(keys)
		case ErrInputFull:
			full = true
		default:
			t.Fatal(err)
		}
	}
	if !full {
		t.Fatalf("Neovim took all %d keys: the test filled nothing", sent)
	}
}

// Neovim's own quit is told once, as a quit: the end of its output after it
// (the client's reading ends) tells nothing more.
func TestAQuitIsToldOnceAsAQuit(t *testing.T) {
	e, r := cmdlineEditor(t, "one\n", "")
	k := &typist{t: t, e: e, r: r}
	k.cmdline(":", "q")
	k.key("<CR>")
	select {
	case <-r.done:
	case <-time.After(10 * time.Second):
		t.Fatal("no exit after :q")
	}
	time.Sleep(300 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, ev := range r.events {
		if strings.HasPrefix(ev, "exited") {
			n++
			if ev != "exited quit" {
				t.Fatalf("the exit: %q", ev)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d exits told: %q", n, r.events)
	}
}

// A notification whose arguments are not of the shape the bridge expects
// (the preview's lines once were one, with a changedtick of v:null) is
// passed over: the client goes on reading, and the editor with it. Before,
// the client ended its connection on it and the editor lost Neovim.
func TestANotificationOfAnUnexpectedShapeIsPassedOver(t *testing.T) {
	e, r := cmdlineEditor(t, "one\n", "")
	for _, args := range []string{
		"'nvim_buf_changedtick_event', 'not a buffer', 'not a tick'",
		"'nvim_buf_lines_event', 'not a buffer', v:null, 'a', 'b', 'c', v:false",
		"'nvim_buf_lines_event', 1, 2, 'a', 'b', [1, 2], v:false",
		"'nvim_buf_detach_event', 'not a buffer'",
		"'redraw', 'not an update', ['msg_show']",
	} {
		if err := e.v.Command(fmt.Sprintf("call rpcnotify(%d, %s)", e.ch, args)); err != nil {
			t.Fatalf("after %s: %v", args, err)
		}
	}
	k := &typist{t: t, e: e, r: r}
	k.key("A")
	k.key("x")
	esc := k.key("<Esc>")
	r.wait(t, fmt.Sprintf("ack %d 1:4 n", esc))
	bufferIs(t, e, "onex")
	if got := r.changesNow(); len(got) == 0 || got[len(got)-1] != `0:1 ["onex"]` {
		t.Fatalf("the page was told of %q", got)
	}
	if r.has("exited") {
		t.Fatal("the editor ended")
	}
}
