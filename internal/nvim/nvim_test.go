package nvim

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder keeps what the editor reported, in order.
type recorder struct {
	mu     sync.Mutex
	events []string
	lines  [][]string
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
