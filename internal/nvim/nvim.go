// Package nvim embeds the real Neovim for the editor (design round 12, F8):
// one `nvim --embed` per open file on the machine that runs the session,
// the person's own config loaded, driven over msgpack-rpc by the official
// client. The bridge reports the buffer's changes, the cursor, the mode, the
// command line and the messages to a Handler and takes keys in Neovim
// notation. What a viewer sees of it rides the terminal's connection in
// internal/session.
package nvim

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/neovim/go-client/nvim"
)

// Handler receives what the editor reports. Its methods run on the client's
// goroutine, one at a time; they must not block on the editor.
type Handler interface {
	// Lines says the buffer lines [first, last) became lines (0-based; last
	// -1 means to the end, used when the whole buffer is sent).
	Lines(first, last int, lines []string)
	// Cursor is the 1-based position, the mode and the other end of a
	// visual selection (the cursor itself outside visual modes).
	Cursor(line, col int, mode string, visualLine, visualCol int)
	Mode(mode string)
	// Cmdline shows the command line as typed (show false hides it).
	Cmdline(show bool, content string, pos int, prompt string)
	// Message is one of Neovim's messages; kind "" is an echo, "emsg" an
	// error, "clear" an empty text clearing the last.
	Message(text, kind string)
	// Written says the buffer was written to path.
	Written(path string)
	// Buffer says the editor now shows another file (after `:e` or `:bnext`).
	Buffer(path string)
	// Exited says Neovim left (reason "quit" for its own exit).
	Exited(reason string)
	// Swap says the file has another editor's swap file: Neovim opened it
	// read-only (round 13, G3); SwapChoice answers.
	Swap(s Swap)
}

// Swap is another editor's swap file for the file opened, as swapinfo()
// tells it: the swap file, the process that wrote it and whether that runs
// on this machine now, its user and host, whether it holds changes not
// written, and when it was written (Unix seconds).
type Swap struct {
	File     string
	Pid      int
	Running  bool
	User     string
	Host     string
	Modified bool
	Mtime    int64
}

// The answers SwapChoice takes.
const (
	SwapEdit    = "edit"
	SwapRecover = "recover"
	SwapDelete  = "delete"
)

// ErrSwap refuses a swap answer that does not apply: no swap file was
// found, or recover and delete while the process that wrote it runs.
var ErrSwap = errors.New("nvim: that answer does not apply to the swap file")

// Program is the executable looked up on PATH.
const Program = "nvim"

// Available reports whether Neovim can be run on this machine.
func Available() bool {
	_, err := exec.LookPath(Program)
	return err == nil
}

// Editor is one embedded Neovim showing one file.
type Editor struct {
	v      *nvim.Nvim
	h      Handler
	mu     sync.Mutex
	buf    nvim.Buffer
	closed atomic.Bool
	// cmdline keeps the last shown command line for cmdline_pos events.
	cmdContent, cmdPrompt string
	// swap is the last swap file found, for SwapChoice; guarded by mu.
	swap *Swap
}

// ErrClosed is returned by Input once the editor is gone.
var ErrClosed = errors.New("nvim: the editor has exited")

// Open starts Neovim in dir, loads path, and reports to h until Close or
// Neovim's own exit. ctx ends the process when it ends.
func Open(ctx context.Context, dir, path string, h Handler) (*Editor, error) {
	prog, err := exec.LookPath(Program)
	if err != nil {
		return nil, err
	}
	v, err := nvim.NewChildProcess(nvim.ChildProcessCommand(prog), nvim.ChildProcessArgs("--embed"), nvim.ChildProcessDir(dir), nvim.ChildProcessContext(ctx),
		nvim.ChildProcessLogf(func(string, ...interface{}) {}))
	if err != nil {
		return nil, err
	}
	e := &Editor{v: v, h: h}
	if err := e.register(); err != nil {
		v.Close()
		return nil, err
	}
	if err := v.AttachUI(80, 24, map[string]interface{}{"ext_linegrid": true, "ext_cmdline": true, "ext_messages": true, "ext_popupmenu": true}); err != nil {
		v.Close()
		return nil, err
	}
	ch := v.ChannelID()
	for _, cmd := range []string{
		fmt.Sprintf("autocmd CursorMoved,CursorMovedI * call rpcnotify(%d, 'conductor_cursor', line('.'), col('.'), mode(), getpos('v')[1], getpos('v')[2])", ch),
		fmt.Sprintf("autocmd BufWritePost * call rpcnotify(%d, 'conductor_written', expand('<afile>:p'))", ch),
		fmt.Sprintf("autocmd VimLeavePre * call rpcnotify(%d, 'conductor_leaving')", ch),
		// Another editor's swap file: open read-only instead of asking (the
		// question would block the open), and say whose it is.
		fmt.Sprintf("autocmd SwapExists * let v:swapchoice = 'o' | call rpcnotify(%d, 'conductor_swap', v:swapname, swapinfo(v:swapname))", ch),
	} {
		if err := v.Command(cmd); err != nil {
			v.Close()
			return nil, err
		}
	}
	var escaped string
	if err := v.Call("fnameescape", &escaped, path); err != nil {
		v.Close()
		return nil, err
	}
	if err := v.Command("edit " + escaped); err != nil {
		// A swap file prompt or a directory: Neovim says why.
		v.Close()
		return nil, err
	}
	if err := e.attachCurrent(); err != nil {
		v.Close()
		return nil, err
	}
	// Only now does a buffer change (`:e other`, `:bnext`) get followed: the
	// first file is attached above, deterministically.
	if err := v.Command(fmt.Sprintf("autocmd BufEnter * call rpcnotify(%d, 'conductor_buffer', expand('<afile>:p'))", ch)); err != nil {
		v.Close()
		return nil, err
	}
	return e, nil
}

// register wires the client's notifications to the handler.
func (e *Editor) register() error {
	v := e.v
	if err := v.RegisterHandler("redraw", e.onRedraw); err != nil {
		return err
	}
	if err := v.RegisterHandler(nvim.EventBufLines, func(buf nvim.Buffer, tick int64, first, last int64, lines [][]byte, more bool) {
		e.mu.Lock()
		cur := e.buf
		e.mu.Unlock()
		if buf != cur {
			return
		}
		ss := make([]string, len(lines))
		for i, l := range lines {
			ss[i] = string(l)
		}
		e.h.Lines(int(first), int(last), ss)
	}); err != nil {
		return err
	}
	if err := v.RegisterHandler(nvim.EventBufChangedtick, func(buf nvim.Buffer, tick int64) {}); err != nil {
		return err
	}
	if err := v.RegisterHandler(nvim.EventBufDetach, func(buf nvim.Buffer) {
		// Neovim stops following a buffer it reloads (:e!, :recover): the
		// one on screen is followed again and sent whole.
		if e.closed.Load() {
			return
		}
		e.mu.Lock()
		cur := e.buf
		e.mu.Unlock()
		if buf != cur {
			return
		}
		if ok, err := e.v.AttachBuffer(buf, false, map[string]interface{}{}); err == nil && ok {
			e.sendWhole()
		}
	}); err != nil {
		return err
	}
	if err := v.RegisterHandler("conductor_cursor", func(args ...interface{}) {
		if len(args) < 5 {
			return
		}
		e.h.Cursor(toInt(args[0]), toInt(args[1]), fmt.Sprint(args[2]), toInt(args[3]), toInt(args[4]))
	}); err != nil {
		return err
	}
	if err := v.RegisterHandler("conductor_written", func(args ...interface{}) {
		if len(args) > 0 {
			e.h.Written(fmt.Sprint(args[0]))
		}
	}); err != nil {
		return err
	}
	if err := v.RegisterHandler("conductor_buffer", func(args ...interface{}) {
		if len(args) == 0 {
			return
		}
		path := fmt.Sprint(args[0])
		if e.closed.Load() || path == "" {
			return
		}
		e.mu.Lock()
		changed := e.attachCurrentLocked() == nil
		e.mu.Unlock()
		if changed {
			e.h.Buffer(path)
			e.sendWhole()
		}
	}); err != nil {
		return err
	}
	if err := v.RegisterHandler("conductor_swap", func(args ...interface{}) {
		if len(args) < 2 {
			return
		}
		sw := swapOf(fmt.Sprint(args[0]), args[1])
		e.mu.Lock()
		e.swap = &sw
		e.mu.Unlock()
		e.h.Swap(sw)
	}); err != nil {
		return err
	}
	return v.RegisterHandler("conductor_leaving", func(args ...interface{}) {
		if e.closed.CompareAndSwap(false, true) {
			e.h.Exited("quit")
		}
	})
}

// attachCurrent follows the current buffer's changes (and no other's).
func (e *Editor) attachCurrent() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.attachCurrentLocked()
}

func (e *Editor) attachCurrentLocked() error {
	buf, err := e.v.CurrentBuffer()
	if err != nil {
		return err
	}
	if buf == e.buf {
		return errors.New("nvim: same buffer")
	}
	if e.buf != 0 {
		e.v.DetachBuffer(e.buf)
	}
	if _, err := e.v.AttachBuffer(buf, false, map[string]interface{}{}); err != nil {
		return err
	}
	e.buf = buf
	return nil
}

// sendWhole reports the whole current buffer as one change.
func (e *Editor) sendWhole() {
	e.mu.Lock()
	buf := e.buf
	e.mu.Unlock()
	lines, err := e.v.BufferLines(buf, 0, -1, true)
	if err != nil {
		return
	}
	ss := make([]string, len(lines))
	for i, l := range lines {
		ss[i] = string(l)
	}
	e.h.Lines(0, -1, ss)
}

// Lines returns the whole buffer, for the first showing.
func (e *Editor) Lines() ([]string, error) {
	e.mu.Lock()
	buf := e.buf
	e.mu.Unlock()
	lines, err := e.v.BufferLines(buf, 0, -1, true)
	if err != nil {
		return nil, err
	}
	ss := make([]string, len(lines))
	for i, l := range lines {
		ss[i] = string(l)
	}
	return ss, nil
}

// Input sends keys (Neovim notation) and then reports the cursor and the
// mode, so a mode entered without moving (`v`, `i`) shows at once.
func (e *Editor) Input(keys string) error {
	if e.closed.Load() {
		return ErrClosed
	}
	if _, err := e.v.Input(keys); err != nil {
		if e.closed.CompareAndSwap(false, true) {
			e.h.Exited("error: " + err.Error())
		}
		return ErrClosed
	}
	// A command waiting for its next key (the second g of gg, f's
	// character) is cancelled by an eval: nvim_get_mode is answered without
	// touching it and says so; the autocmd reports the cursor once it moves.
	if m, err := e.v.Mode(); err != nil || m.Blocking {
		return nil
	}
	var pos []interface{}
	if err := e.v.Eval("[line('.'), col('.'), mode(), getpos('v')[1], getpos('v')[2]]", &pos); err == nil && len(pos) == 5 {
		e.h.Cursor(toInt(pos[0]), toInt(pos[1]), fmt.Sprint(pos[2]), toInt(pos[3]), toInt(pos[4]))
	}
	return nil
}

// Report tells the handler the cursor and the mode now (the first showing).
func (e *Editor) Report() {
	if e.closed.Load() {
		return
	}
	var pos []interface{}
	if err := e.v.Eval("[line('.'), col('.'), mode(), getpos('v')[1], getpos('v')[2]]", &pos); err == nil && len(pos) == 5 {
		e.h.Mode(fmt.Sprint(pos[2]))
		e.h.Cursor(toInt(pos[0]), toInt(pos[1]), fmt.Sprint(pos[2]), toInt(pos[3]), toInt(pos[4]))
	}
}

// FoundSwap is the swap file found and not yet answered (nil when none):
// one found while the file opened was reported before anyone listened.
func (e *Editor) FoundSwap() *Swap {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.swap == nil {
		return nil
	}
	sw := *e.swap
	return &sw
}

// SwapChoice answers the swap file found: SwapEdit leaves read-only,
// SwapRecover reads the swap file's text into the buffer (then leaves
// read-only; the buffer is modified until written), SwapDelete removes the
// swap file and leaves read-only. Recover and delete need the process that
// wrote it gone. The choices map to fixed commands; nothing the browser
// sends is run.
func (e *Editor) SwapChoice(choice string) error {
	if e.closed.Load() {
		return ErrClosed
	}
	e.mu.Lock()
	sw := e.swap
	e.mu.Unlock()
	if sw == nil {
		return ErrSwap
	}
	running := sw.Running && processRuns(sw.Pid)
	switch choice {
	case SwapEdit:
	case SwapRecover:
		if running {
			return ErrSwap
		}
		var escaped string
		if err := e.v.Call("fnameescape", &escaped, sw.File); err != nil {
			return err
		}
		if err := e.v.Command("silent recover " + escaped); err != nil {
			return err
		}
	case SwapDelete:
		if running {
			return ErrSwap
		}
		if err := removeSwap(sw.File); err != nil {
			return err
		}
	default:
		return ErrSwap
	}
	if err := e.v.Command("setlocal noreadonly"); err != nil {
		return err
	}
	if choice == SwapRecover {
		// :recover replaces the buffer's text without a line event: send it whole.
		e.sendWhole()
	}
	if choice != SwapRecover {
		e.mu.Lock()
		e.swap = nil
		e.mu.Unlock()
	}
	return nil
}

// Close ends Neovim without saving.
func (e *Editor) Close() {
	e.closed.Store(true)
	e.v.Close()
}

// onRedraw reads the UI events the bridge cares about: the mode, the
// command line and the messages.
func (e *Editor) onRedraw(updates ...[]interface{}) {
	for _, u := range updates {
		if len(u) < 2 {
			continue
		}
		name, _ := u[0].(string)
		for _, raw := range u[1:] {
			args, _ := raw.([]interface{})
			switch name {
			case "mode_change":
				if len(args) > 0 {
					e.h.Mode(fmt.Sprint(args[0]))
				}
			case "cmdline_show":
				// [content, pos, firstc, prompt, indent, level]
				if len(args) >= 4 {
					e.cmdContent = chunksText(args[0])
					e.cmdPrompt = fmt.Sprint(args[2]) + fmt.Sprint(args[3])
					e.h.Cmdline(true, e.cmdContent, toInt(args[1]), e.cmdPrompt)
				}
			case "cmdline_pos":
				if len(args) >= 1 {
					e.h.Cmdline(true, e.cmdContent, toInt(args[0]), e.cmdPrompt)
				}
			case "cmdline_hide":
				e.cmdContent, e.cmdPrompt = "", ""
				e.h.Cmdline(false, "", 0, "")
			case "msg_show":
				// [kind, content, replace_last]
				if len(args) >= 2 {
					e.h.Message(chunksText(args[1]), fmt.Sprint(args[0]))
				}
			case "msg_clear":
				e.h.Message("", "clear")
			}
		}
	}
}

// chunksText joins the text of UI content chunks ([[attr_id, text], ...]).
func chunksText(v interface{}) string {
	chunks, _ := v.([]interface{})
	var b strings.Builder
	for _, c := range chunks {
		pair, _ := c.([]interface{})
		if len(pair) >= 2 {
			b.WriteString(fmt.Sprint(pair[1]))
		}
	}
	return b.String()
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int64:
		return int(n)
	case int:
		return n
	case uint64:
		return int(n)
	case int32:
		return int(n)
	case uint32:
		return int(n)
	case int8:
		return int(n)
	case uint8:
		return int(n)
	case int16:
		return int(n)
	case uint16:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
