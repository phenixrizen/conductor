package session

import (
	"math/rand"
	"strings"
	"testing"
)

func screenOf(cols, rows int, chunks ...string) *Screen {
	s := NewScreen(cols, rows)
	for _, c := range chunks {
		s.Write([]byte(c))
	}
	return s
}

// What a TUI draws with: absolute moves, a run of changed cells over an
// older row, erasing to the end of a row and of the screen. The text is the
// rows without their trailing spaces, without the blank rows at the bottom.
func TestScreenDrawsAsATerminalDoes(t *testing.T) {
	s := screenOf(20, 5, "Hello world\r\nsecond row", "\x1b[1;7Hthere", "\x1b[3;3H\x1b[1mbold\x1b[0m   ")
	if got, want := s.Text(), "Hello there\nsecond row\n  bold"; got != want {
		t.Fatalf("text %q, want %q", got, want)
	}
	s.Write([]byte("\x1b[2;7H\x1b[K"))
	if got := s.Text(); got != "Hello there\nsecond\n  bold" {
		t.Fatalf("after EL %q", got)
	}
	s.Write([]byte("\x1b[1;6H\x1b[J"))
	if got := s.Text(); got != "Hello" {
		t.Fatalf("after ED %q", got)
	}
	s.Write([]byte("\x1b[2J"))
	if got := s.Text(); got != "" {
		t.Fatalf("after ED 2 %q", got)
	}
	// ED 3 erases the scrollback only.
	s.Write([]byte("\x1b[Hkept\x1b[3J"))
	if got := s.Text(); got != "kept" {
		t.Fatalf("after ED 3 %q", got)
	}
}

// Line feeds at the bottom scroll; a long row wraps; a scroll region scrolls
// alone, as Codex inserts its history above its viewport (DECSTBM, then
// \r\n at the region's bottom), and reverse index scrolls it down.
func TestScreenScrollsWrapsAndKeepsItsRegion(t *testing.T) {
	s := screenOf(10, 3, "one\r\ntwo\r\nthree\r\nfour")
	if got := s.Text(); got != "two\nthree\nfour" {
		t.Fatalf("scrolled %q", got)
	}
	s = screenOf(10, 3, "abcdefghijKL")
	if got := s.Text(); got != "abcdefghij\nKL" {
		t.Fatalf("wrapped %q", got)
	}
	s = screenOf(10, 5, "\x1b[4;1Hviewport\x1b[5;1Hbottom", "\x1b[1;3r\x1b[3;1H\r\nhist one\r\nhist two\x1b[r")
	if got := s.Text(); got != "\nhist one\nhist two\nviewport\nbottom" {
		t.Fatalf("history above the viewport %q", got)
	}
	s.Write([]byte("\x1b[4;5r\x1b[4;1H\x1bM"))
	if got := s.Text(); got != "\nhist one\nhist two\n\nviewport" {
		t.Fatalf("reverse index at the region's top %q", got)
	}
}

// The alternate screen is a screen of its own; leaving it shows the main
// one again, the cursor where it was for 1049.
func TestScreenAlternate(t *testing.T) {
	s := screenOf(20, 4, "main text", "\x1b[?1049h\x1b[Halt text")
	if got := s.Text(); got != "alt text" {
		t.Fatalf("alt %q", got)
	}
	s.Write([]byte("\x1b[?1049l more"))
	if got := s.Text(); got != "main text more" {
		t.Fatalf("back on main %q", got)
	}
}

// Wide characters take two cells; a character, an escape sequence or a
// UTF-8 sequence cut between writes is read whole.
func TestScreenWideCharactersAndCutWrites(t *testing.T) {
	s := screenOf(10, 2, "日本x")
	if got := s.Text(); got != "日本x" {
		t.Fatalf("wide %q", got)
	}
	s.Write([]byte("\x1b[1;2Hab"))
	if got := s.Text(); got != " ab x" {
		t.Fatalf("over a wide character (its halves go with it) %q", got)
	}
	whole := "\x1b[2;1H\x1b[1mUpdate · 0.161 → 0.162\x1b]8;;https://x\a link\x1b]8;;\a"
	cut := NewScreen(40, 3)
	for i := 0; i < len(whole); i++ {
		cut.Write([]byte{whole[i]})
	}
	if got, want := cut.Text(), screenOf(40, 3, whole).Text(); got != want || !strings.Contains(got, "Update · 0.161 → 0.162 link") {
		t.Fatalf("cut %q, whole %q", got, want)
	}
}

// Codex 0.161's update question, as it drew it, reads on a screen as it
// shows: the words its cursor moves put in place.
func TestScreenReadsCodexsUpdateQuestion(t *testing.T) {
	got := screenOf(100, 30, codexUpdateScreen).Text()
	for _, want := range []string{"  Update available · 0.161.0 → 0.162.1", "› 1. Update now (runs `npm install -g @openai/codex`)", "  3. Skip until next version", "  enter continue · esc skip"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%q not in %q", want, got)
		}
	}
}

// A resize keeps what fits, the rows at the bottom when the cursor would
// fall off; sizes are bounded.
func TestScreenResize(t *testing.T) {
	s := screenOf(20, 4, "a\r\nb\r\nc\r\nd")
	s.Resize(20, 2)
	if got := s.Text(); got != "c\nd" {
		t.Fatalf("shrunk %q", got)
	}
	s.Resize(3, 2)
	s.Write([]byte("\x1b[1;1Hxyzw"))
	if got := s.Text(); got != "xyz\nw" {
		t.Fatalf("narrowed %q", got)
	}
	s.Resize(1<<20, 1<<20)
	if s.cols != maxScreenCols || s.rows != maxScreenRows {
		t.Fatalf("bounded to %dx%d", s.cols, s.rows)
	}
	s.Resize(0, 0)
	if s.cols != 80 || s.rows != 24 {
		t.Fatalf("default %dx%d", s.cols, s.rows)
	}
}

// Whatever comes, a Screen neither panics nor grows past its bounds.
func TestScreenTakesAnyBytes(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	alphabet := []byte("\x1b[];?0123456789HJKLMPSTXr@dGfhlm\r\n\b\t\x07\\abc ›日\xe2\x80\x9b\xff")
	s := NewScreen(30, 8)
	for i := 0; i < 20000; i++ {
		n := r.Intn(64)
		chunk := make([]byte, n)
		for j := range chunk {
			chunk[j] = alphabet[r.Intn(len(alphabet))]
		}
		s.Write(chunk)
		if i%1000 == 0 {
			s.Resize(1+r.Intn(60), 1+r.Intn(20))
		}
		if len(s.cells) != s.rows || s.x < 0 || s.x >= s.cols || s.y < 0 || s.y >= s.rows {
			t.Fatalf("out of bounds: %d rows, cursor %d,%d in %dx%d", len(s.cells), s.x, s.y, s.cols, s.rows)
		}
	}
	_ = s.Text()
	big := NewScreen(10, 2)
	big.Write([]byte("\x1b[" + strings.Repeat("9", 10000) + ";" + strings.Repeat("9", 1000) + "H!"))
	if got := big.Text(); got != "!" {
		t.Fatalf("a sequence longer than kept is dropped: %q", got)
	}
}

// FuzzScreen checks that no input panics a Screen or puts its cursor off it.
func FuzzScreen(f *testing.F) {
	f.Add([]byte(codexUpdateScreen), 80, 24)
	f.Add([]byte("\x1b[1;3r\x1b[3;1H\r\nhist\x1b[r\x1bM\x1b[?1049h日本\x1b[2J"), 10, 3)
	f.Fuzz(func(t *testing.T, data []byte, cols, rows int) {
		s := NewScreen(cols%300, rows%100)
		s.Write(data)
		if s.x < 0 || s.x >= s.cols || s.y < 0 || s.y >= s.rows {
			t.Fatalf("cursor %d,%d in %dx%d", s.x, s.y, s.cols, s.rows)
		}
		_ = s.Text()
	})
}
