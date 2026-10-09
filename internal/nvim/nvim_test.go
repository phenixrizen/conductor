package nvim

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// recorder keeps what the editor reported, in order.
type recorder struct {
	mu     sync.Mutex
	events []string
	lines  [][]string
	swaps  []Swap
	done   chan struct{}
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.events = append(r.events, s)
	r.mu.Unlock()
}
func (r *recorder) Lines(first, last int, lines []string) {
	r.mu.Lock()
	r.lines = append(r.lines, lines)
	r.mu.Unlock()
	r.add("lines")
}
func (r *recorder) Cursor(line, col int, mode string, vl, vc int) { r.add("cursor " + mode) }
func (r *recorder) Mode(mode string)                              { r.add("mode " + mode) }
func (r *recorder) Cmdline(show bool, content string, pos int, prompt string) {
	if show {
		r.add("cmdline " + prompt + content)
	} else {
		r.add("cmdline hidden")
	}
}
func (r *recorder) Message(text, kind string) { r.add("message " + kind + ":" + text) }
func (r *recorder) Written(path string)       { r.add("written " + filepath.Base(path)) }
func (r *recorder) Buffer(path string)        { r.add("buffer " + filepath.Base(path)) }
func (r *recorder) Swap(sw Swap) {
	r.mu.Lock()
	r.swaps = append(r.swaps, sw)
	r.mu.Unlock()
	r.add("swap " + filepath.Base(sw.File))
}
func (r *recorder) Exited(reason string) {
	r.add("exited " + reason)
	close(r.done)
}
func (r *recorder) has(prefix string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}
func (r *recorder) wait(t *testing.T, prefix string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if r.has(prefix) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	t.Fatalf("no %q among %q", prefix, r.events)
}

// The real nvim: a file loads, keys change the buffer and come back as line
// events, the mode and the command line show, :w writes the file and says
// so, :q ends the editor.
func TestEditorAgainstTheRealNeovim(t *testing.T) {
	if !Available() {
		t.Skip("nvim is not on PATH; the bridge's test needs the real Neovim")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "sample.txt")
	os.WriteFile(file, []byte("line one\nline two\nline three\n"), 0o644)
	r := &recorder{done: make(chan struct{})}
	e, err := Open(context.Background(), dir, "sample.txt", r)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if lines, err := e.Lines(); err != nil || len(lines) != 3 || lines[0] != "line one" {
		t.Fatalf("lines: %q %v", lines, err)
	}
	if err := e.Input("dd"); err != nil {
		t.Fatal(err)
	}
	r.wait(t, "lines")
	r.mu.Lock()
	first := r.lines[0]
	r.mu.Unlock()
	if len(first) != 0 {
		t.Fatalf("dd should report a deletion, got %q", first)
	}
	e.Input("i")
	r.wait(t, "mode insert")
	e.Input("hello <lt>world<Esc>")
	r.wait(t, "cursor n")
	e.Input(":w")
	r.wait(t, "cmdline :w")
	e.Input("<CR>")
	r.wait(t, "written sample.txt")
	r.wait(t, "message")
	b, _ := os.ReadFile(file)
	if string(b) != "hello <worldline two\nline three\n" {
		t.Fatalf("written file: %q", b)
	}
	// Two-key commands sent a key at a time, as a browser sends them: gg, then f and its character.
	e.Input("ggOone two<Esc>G")
	for _, k := range []string{"g", "g", "0", "f", "t"} {
		if err := e.Input(k); err != nil {
			t.Fatal(err)
		}
	}
	var col int
	for i := 0; i < 50; i++ {
		var pos []int
		if err := e.v.Eval("[line('.'), col('.')]", &pos); err == nil && len(pos) == 2 && pos[0] == 1 && pos[1] == 5 {
			col = pos[1]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if col != 5 {
		t.Fatal("gg then ft, a key at a time, did not land on the t of two")
	}
	e.Input(":q!<CR>")
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("no exit after :q")
	}
	if err := e.Input("x"); err != ErrClosed {
		t.Fatalf("input after exit: %v", err)
	}
}

// Another editor's swap file (round 13, G3): the file opens read-only and
// the swap file is reported with its writer; while that runs only "edit
// anyway" applies; once it is gone (killed, the swap file left behind) the
// swap file's unsaved text can be recovered and the swap file deleted.
// Neovim's state and config go to temporary folders, so the person's own
// swap folder is never touched.
func TestASwapFileOpensReadOnlyWithItsChoices(t *testing.T) {
	if !Available() {
		t.Skip("nvim is not on PATH; the bridge's test needs the real Neovim")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	file := filepath.Join(dir, "sample.txt")
	os.WriteFile(file, []byte("one\n"), 0o644)
	readonly := func(e *Editor) bool {
		var ro int
		if err := e.v.Eval("&readonly", &ro); err != nil {
			t.Fatal(err)
		}
		return ro == 1
	}
	// The first editor holds the file with an unsaved change.
	r1 := &recorder{done: make(chan struct{})}
	e1, err := Open(context.Background(), dir, "sample.txt", r1)
	if err != nil {
		t.Fatal(err)
	}
	defer e1.Close()
	e1.Input("ixx<Esc>")
	r1.wait(t, "cursor n")
	var pid int
	if err := e1.v.Eval("getpid()", &pid); err != nil {
		t.Fatal(err)
	}
	// A second opens it read-only and says whose swap file it is.
	r2 := &recorder{done: make(chan struct{})}
	e2, err := Open(context.Background(), dir, "sample.txt", r2)
	if err != nil {
		t.Fatalf("the open was refused: %v", err)
	}
	if lines, _ := e2.Lines(); len(lines) != 1 || lines[0] != "one" {
		t.Fatalf("lines %q", lines)
	}
	sw := e2.FoundSwap()
	if sw == nil || sw.Pid != pid || !sw.Running || !strings.HasPrefix(filepath.Ext(sw.File), ".sw") || !readonly(e2) {
		t.Fatalf("swap %+v, read-only %v", sw, readonly(e2))
	}
	if err := e2.SwapChoice(SwapDelete); err != ErrSwap {
		t.Fatalf("delete while its writer runs: %v", err)
	}
	if err := e2.SwapChoice(SwapEdit); err != nil || readonly(e2) {
		t.Fatalf("edit anyway: %v, read-only %v", err, readonly(e2))
	}
	e2.Close()
	// The first editor writes its change into the swap file (Neovim does that
	// lazily, after a pause or some typing), then dies without cleaning up:
	// the swap file stays, stale.
	if err := e1.v.Command("preserve"); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	e1.Close()
	for i := 0; i < 100 && processRuns(pid); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	r3 := &recorder{done: make(chan struct{})}
	e3, err := Open(context.Background(), dir, "sample.txt", r3)
	if err != nil {
		t.Fatal(err)
	}
	defer e3.Close()
	sw = e3.FoundSwap()
	if sw == nil || sw.Running || !sw.Modified {
		t.Fatalf("stale swap %+v", sw)
	}
	if err := e3.SwapChoice(SwapRecover); err != nil {
		t.Fatal(err)
	}
	if lines, _ := e3.Lines(); len(lines) != 1 || lines[0] != "xxone" || readonly(e3) {
		t.Fatalf("recovered %q, read-only %v", lines, readonly(e3))
	}
	// The recovery reloads the buffer: the bridge follows it again and
	// reports the recovered text, so the editor shows it.
	deadline := time.Now().Add(5 * time.Second)
	for {
		r3.mu.Lock()
		got := len(r3.lines) > 0 && len(r3.lines[len(r3.lines)-1]) == 1 && r3.lines[len(r3.lines)-1][0] == "xxone"
		r3.mu.Unlock()
		if got {
			break
		}
		if time.Now().After(deadline) {
			r3.mu.Lock()
			t.Fatalf("no lines event with the recovered text: %q (events %q)", r3.lines, r3.events)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := e3.SwapChoice(SwapDelete); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sw.File); !os.IsNotExist(err) {
		t.Fatalf("the swap file is still there: %v", err)
	}
	if e3.FoundSwap() != nil {
		t.Fatal("answered, the swap is still pending")
	}
}

// Neovim's own questions (:confirm q with changes not written) come as a
// message of kind "confirm" carrying the choices' words, which the editor
// shows as buttons (round 13, G3); Cancel (c) leaves the editor open.
func TestAConfirmQuestionIsAConfirmMessage(t *testing.T) {
	if !Available() {
		t.Skip("nvim is not on PATH; the bridge's test needs the real Neovim")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("one\n"), 0o644)
	r := &recorder{done: make(chan struct{})}
	e, err := Open(context.Background(), dir, "sample.txt", r)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Input("ix<Esc>")
	r.wait(t, "cursor n")
	e.Input(":confirm q<CR>")
	r.wait(t, "message confirm:")
	r.mu.Lock()
	var text string
	for _, ev := range r.events {
		if strings.HasPrefix(ev, "message confirm:") {
			text = ev
		}
	}
	r.mu.Unlock()
	if !strings.Contains(text, "[Y]es") || !strings.Contains(text, "(C)ancel") {
		t.Fatalf("confirm %q", text)
	}
	e.Input("c")
	time.Sleep(300 * time.Millisecond)
	if r.has("exited") {
		t.Fatal("Cancel ended the editor")
	}
}
