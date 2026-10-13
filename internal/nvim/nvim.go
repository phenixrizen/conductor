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
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neovim/go-client/nvim"
)

// Handler receives what the editor reports. Its methods run on the client's
// goroutine, one at a time; they must not block on the editor. Exited also
// comes from elsewhere (a key that finds Neovim gone, the client's reading
// as it ends) and may come before the last changes a Neovim that died sent,
// which still come after it.
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
	// Ack says Neovim has handled the keys of InputSeq's seq and every one
	// before, with the cursor and the mode after them (round 13, G5). It
	// comes after every buffer change those keys made.
	Ack(seq uint32, line, col int, mode string, visualLine, visualCol int)
	// Modified says the buffer now holds changes not written (true) or no
	// longer does (false: written, undone back, or another file shown).
	Modified(modified bool)
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

// childDropped reports whether an environment variable is kept from the
// Neovim child: Conductor's own variables (its tokens and configuration, all
// CONDUCTOR_*), and the dynamic linker's injection variables, which on glibc
// and macOS begin LD_ or DYLD_ (LD_PRELOAD, LD_AUDIT, LD_LIBRARY_PATH,
// DYLD_INSERT_LIBRARIES and the rest of their families), are dropped so that
// no Conductor secret, and no library the launcher's environment named,
// reaches an editor the person drives.
func childDropped(k string) bool {
	return strings.HasPrefix(k, "CONDUCTOR_") || strings.HasPrefix(k, "LD_") || strings.HasPrefix(k, "DYLD_")
}

// childEnv is the environment for the Neovim child: the parent's, with the
// dropped variables (childDropped) removed. The rest is kept as it is,
// because the editor runs the person's own config and plugins, which read
// HOME, the XDG directories, PATH and whatever else they were set up with.
func childEnv(parent []string) []string {
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || childDropped(k) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Editor is one embedded Neovim showing one file.
type Editor struct {
	v *nvim.Nvim
	// cmd is the Neovim process; served is closed once the client's
	// reading of it has ended; reaping waits for both, once (reap).
	cmd     *exec.Cmd
	served  chan struct{}
	reaping sync.Once
	// out is the read end of Neovim's output, the editor's own: closed
	// last, as it is reaped, and as its context ends.
	out *os.File
	// ended is the editor's context's Done: its end cuts reap's waits short.
	ended <-chan struct{}

	h      Handler
	mu     sync.Mutex
	buf    nvim.Buffer
	closed atomic.Bool
	// cmdline keeps the last shown command line for cmdline_pos events.
	cmdContent, cmdPrompt string
	// asking says the last message shown is one of Neovim's questions
	// (promptKinds). Like cmdContent it is the client's notification
	// goroutine's alone (onRedraw, the acknowledgement).
	asking bool
	// swap is the last swap file found, for SwapChoice; guarded by mu.
	swap *Swap
	// ch is the client's channel in Neovim, which rpcnotify answers on.
	ch int
	// previews counts the lines of previews not relayed (the tests read it).
	previews atomic.Int64
}

// ErrClosed is returned by Input once the editor is gone.
var ErrClosed = errors.New("nvim: the editor has exited")

// exitWait is how long Close lets Neovim leave on its own once its
// connection is closed, before it is ended (the client's own wait was the
// same).
var exitWait = 10 * time.Second

// Open starts Neovim in dir, loads path, and reports to h until Close or
// Neovim's own exit. ctx ends the process when it ends, and with it what
// it started (its process group, ownGroup).
func Open(ctx context.Context, dir, path string, h Handler) (*Editor, error) {
	prog, err := exec.LookPath(Program)
	if err != nil {
		return nil, err
	}
	// The child is started here rather than by the client's
	// NewChildProcess, so that its process group, the end of its pipes and
	// its reaping are the editor's own: the client's close waits on its
	// output, which a child of a `nvim` wrapper may hold open.
	cmd := exec.CommandContext(ctx, prog, "--embed")
	cmd.Dir = dir
	cmd.Env = childEnv(os.Environ())
	ownGroup(cmd)
	// The pipes are the editor's own, handed to the child as they are:
	// Wait closes neither, so a Neovim still leaving when the process
	// started is reaped (under a wrapper that exited) keeps its connection
	// until it has left (reap), and is never cut off halfway.
	inR, in, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	out, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		in.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout = inR, outW
	err = cmd.Start()
	// The child's ends are the child's alone from now on.
	inR.Close()
	outW.Close()
	if err != nil {
		in.Close()
		out.Close()
		return nil, err
	}
	served := make(chan struct{})
	v, err := nvim.New(out, in, in, func(string, ...interface{}) {})
	if err != nil {
		close(served)
		in.Close()
		e := &Editor{cmd: cmd, served: served, out: out}
		e.end()
		return nil, err
	}
	e := &Editor{v: v, h: h, cmd: cmd, served: served, out: out, ended: ctx.Done()}
	go func() {
		defer close(served)
		err := v.Serve()
		// The client's reading ended with no close and no quit: Neovim is
		// gone (it died; its output was cut), and is said so at once rather
		// than at the next key. Its own quit is told before its output ends
		// (conductor_leaving is a request it waits on); a close, or the
		// context's end, which kills it, is the editor's own.
		if isDone(ctx.Done()) || !e.closed.CompareAndSwap(false, true) {
			return
		}
		reason := "error: neovim ended"
		if err != nil {
			reason = "error: " + err.Error()
		}
		h.Exited(reason)
	}()
	// Its context's end closes the editor's connection too, at once, and
	// Neovim's output: what waits on Neovim (its start, keys, a close) is
	// answered then, though something Neovim started (a descendant out of
	// its group) still holds that output.
	context.AfterFunc(ctx, func() {
		_ = v.Close()
		_ = out.Close()
	})
	fail := func(err error) (*Editor, error) {
		e.end()
		return nil, err
	}
	if err := e.register(); err != nil {
		return fail(err)
	}
	if err := v.AttachUI(80, 24, map[string]interface{}{"ext_linegrid": true, "ext_cmdline": true, "ext_messages": true, "ext_popupmenu": true}); err != nil {
		return fail(err)
	}
	ch := v.ChannelID()
	e.ch = ch
	for _, cmd := range []string{
		fmt.Sprintf("autocmd CursorMoved,CursorMovedI * call rpcnotify(%d, 'conductor_cursor', line('.'), col('.'), mode(), getpos('v')[1], getpos('v')[2])", ch),
		fmt.Sprintf("autocmd BufWritePost * call rpcnotify(%d, 'conductor_written', expand('<afile>:p'))", ch),
		// A request, which Neovim waits on: its quit is told before its
		// output ends, so the client's end (Open) never takes it for a loss.
		fmt.Sprintf("autocmd VimLeavePre * silent! call rpcrequest(%d, 'conductor_leaving')", ch),
		fmt.Sprintf("autocmd BufModifiedSet * if str2nr(expand('<abuf>')) == bufnr() | call rpcnotify(%d, 'conductor_modified', &modified) | endif", ch),
		// Another editor's swap file: open read-only instead of asking (the
		// question would block the open), and say whose it is.
		fmt.Sprintf("autocmd SwapExists * let v:swapchoice = 'o' | call rpcnotify(%d, 'conductor_swap', v:swapname, swapinfo(v:swapname))", ch),
		// No live preview of a substitute (after the person's config, which
		// has run by now): Neovim shows it by changing the buffer for as long
		// as the command line is open and undoing it with no change reported,
		// and its split form opens a window the page has no place for. The
		// page shows the buffer alone; a preview turned on again later (a
		// plugin) is not relayed either (the lines event).
		"set inccommand=",
	} {
		if err := v.Command(cmd); err != nil {
			return fail(err)
		}
	}
	var escaped string
	if err := v.Call("fnameescape", &escaped, path); err != nil {
		return fail(err)
	}
	if err := v.Command("edit " + escaped); err != nil {
		// A swap file prompt or a directory: Neovim says why.
		return fail(err)
	}
	if err := e.attachCurrent(); err != nil {
		return fail(err)
	}
	// Only now does a buffer change (`:e other`, `:bnext`) get followed: the
	// first file is attached above, deterministically.
	if err := v.Command(fmt.Sprintf("autocmd BufEnter * call rpcnotify(%d, 'conductor_buffer', expand('<afile>:p'))", ch)); err != nil {
		return fail(err)
	}
	return e, nil
}

// end ends Neovim at once (Open's failures): its connection closed, its
// process group killed, then reaped.
func (e *Editor) end() {
	e.closed.Store(true)
	if e.v != nil {
		e.v.Close()
	}
	e.kill()
	e.reap(0)
}

// kill ends Neovim's process group (the process alone where there are
// none, ownGroup).
func (e *Editor) kill() {
	if e.cmd.Cancel != nil {
		_ = e.cmd.Cancel()
		return
	}
	_ = e.cmd.Process.Kill()
}

// reap waits, once, for the process started and what is in its process
// group to leave, within after (none when zero: at once) before what is
// left is killed, then closes Neovim's output, which ends the client's
// reading whatever still holds it, and waits for that.
func (e *Editor) reap(after time.Duration) {
	e.reaping.Do(func() {
		deadline := time.Now().Add(after)
		exited := make(chan struct{})
		go func() {
			_ = e.cmd.Wait()
			close(exited)
		}()
		if after > 0 {
			t := time.NewTimer(after)
			select {
			case <-exited:
			case <-t.C:
				e.kill()
				<-exited
			}
			t.Stop()
			// Neovim may outlive the process started (a wrapper that left
			// it running as it exited): it is let leave within the same
			// time, or until the context ends.
			for !groupLeft(e.cmd) && time.Now().Before(deadline) && !isDone(e.ended) {
				time.Sleep(20 * time.Millisecond)
			}
		} else {
			<-exited
		}
		endGroup(e.cmd)
		_ = e.out.Close()
		<-e.served
	})
}

// isDone reports whether done is closed; a nil done never is.
func isDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

// register wires the client's notifications to the handler. Every handler
// takes its arguments as they come (interface{}) and reads them itself: the
// client ends its connection, and the editor loses Neovim, on a message whose
// arguments do not decode into a handler's types (a preview's lines, whose
// changedtick is v:null, once did), so a notification of an unexpected shape
// is passed over instead.
func (e *Editor) register() error {
	v := e.v
	if err := v.RegisterHandler("redraw", e.onRedraw); err != nil {
		return err
	}
	// [buf, changedtick, firstline, lastline, linedata, more]. The
	// changedtick is v:null in the lines of a preview ('inccommand'):
	// Neovim's screen showing what a substitute would do, not the buffer,
	// and undone with no change reported. It is never relayed: the page's
	// text is the buffer's alone.
	if err := v.RegisterHandler(nvim.EventBufLines, func(args ...interface{}) {
		if len(args) < 5 {
			return
		}
		buf, ok := args[0].(nvim.Buffer)
		if !ok {
			return
		}
		if args[1] == nil {
			e.previews.Add(1)
			return
		}
		first, ok1 := intArg(args[2])
		last, ok2 := intArg(args[3])
		raw, ok3 := args[4].([]interface{})
		if !ok1 || !ok2 || !ok3 {
			return
		}
		ss := make([]string, len(raw))
		for i, l := range raw {
			switch l := l.(type) {
			case string:
				ss[i] = l
			case []byte:
				ss[i] = string(l)
			default:
				return
			}
		}
		e.mu.Lock()
		cur := e.buf
		e.mu.Unlock()
		if buf != cur {
			return
		}
		e.h.Lines(first, last, ss)
	}); err != nil {
		return err
	}
	if err := v.RegisterHandler(nvim.EventBufChangedtick, func(args ...interface{}) {}); err != nil {
		return err
	}
	if err := v.RegisterHandler(nvim.EventBufDetach, func(args ...interface{}) {
		// Neovim stops following a buffer it reloads (:e!, :recover): the
		// one on screen is followed again and sent whole.
		if e.closed.Load() || len(args) < 1 {
			return
		}
		buf, ok := args[0].(nvim.Buffer)
		if !ok {
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
	if err := v.RegisterHandler("conductor_modified", func(args ...interface{}) {
		if len(args) > 0 {
			e.h.Modified(toInt(args[0]) != 0)
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
			var modified int
			if e.v.Eval("&modified", &modified) == nil {
				e.h.Modified(modified != 0)
			}
		}
	}); err != nil {
		return err
	}
	if err := v.RegisterHandler("conductor_ack", func(args ...interface{}) {
		if len(args) < 6 {
			return
		}
		// Neovim acknowledges no keys while it waits on a question
		// (InputSeq): one now has been answered. Neovim leaves the question
		// shown with nothing saying so (the last `replace with` of a
		// `:s///c`), and the page would go on asking it.
		if e.asking {
			e.asking = false
			e.h.Message("", "clear")
		}
		e.h.Ack(uint32(toInt(args[0])), toInt(args[1]), toInt(args[2]), fmt.Sprint(args[3]), toInt(args[4]), toInt(args[5]))
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
	return v.RegisterHandler("conductor_leaving", func(args ...interface{}) error {
		if e.closed.CompareAndSwap(false, true) {
			e.h.Exited("quit")
		}
		return nil
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
func (e *Editor) Input(keys string) error { return e.InputSeq(keys, 0) }

// InputSeq is Input with the page's number for these keys (round 13, G5):
// with one, the cursor comes back as Ack once Neovim has handled them, on
// the channel the buffer's changes ride and so after them, which is how a
// page that showed the keys at once knows which of its guesses the buffer
// now holds. While Neovim waits for a command's next key nothing is
// acknowledged; the next keys' Ack covers these.
func (e *Editor) InputSeq(keys string, seq uint32) error {
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
	if seq != 0 {
		_ = e.v.Command(fmt.Sprintf("call rpcnotify(%d, 'conductor_ack', %d, line('.'), col('.'), mode(), getpos('v')[1], getpos('v')[2])", e.ch, seq))
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

// Close ends Neovim without saving: its connection closed, it is let leave
// for exitWait, then killed, and reaped. Its context's end (Open) ends it
// sooner.
func (e *Editor) Close() {
	e.closed.Store(true)
	e.v.Close()
	e.reap(exitWait)
}

// Discard ends Neovim as `:qa!` does: changes not written are dropped and
// its swap file goes. Close alone ends it as a lost connection does, which
// keeps the swap file of a buffer with changes not written, for recovery.
func (e *Editor) Discard() {
	if !e.closed.Load() {
		// Neovim exits inside the request, so the request itself fails.
		_ = e.v.Command("qall!")
	}
	e.Close()
}

// onRedraw reads the UI events the bridge cares about: the mode, the
// command line and the messages.
func (e *Editor) onRedraw(updates ...interface{}) {
	for _, update := range updates {
		u, _ := update.([]interface{})
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
					kind := fmt.Sprint(args[0])
					e.asking = promptKinds[kind]
					e.h.Message(chunksText(args[1]), kind)
				}
			case "msg_clear":
				e.asking = false
				e.h.Message("", "clear")
			}
		}
	}
}

// promptKinds are the kinds of the messages that are Neovim's questions,
// shown while it waits for their answer: `:confirm`'s, a substitute's
// `replace with ...?` (its c flag), and the hit-enter prompt.
var promptKinds = map[string]bool{"confirm": true, "confirm_sub": true, "return_prompt": true}

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

// intArg is a notification's integer argument, and whether it is one.
func intArg(v interface{}) (int, bool) {
	switch v.(type) {
	case int64, int, uint64, int32, uint32, int8, uint8, int16, uint16:
		return toInt(v), true
	}
	return 0, false
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
