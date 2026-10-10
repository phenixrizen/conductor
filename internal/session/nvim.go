package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/phenixrizen/conductor/internal/nvim"
	"github.com/phenixrizen/conductor/internal/proto"
)

// The editor's Neovim bridge (design round 12, F8): one of the owner's own
// connections, on a session whose FileEdit allows editing, opens the real
// `nvim` on this machine for a file; keys go in, the buffer's changes and
// the editor's state come back as nvim_event control frames on the same
// connection.
//
// Neovim runs as the person who runs the session, with their config, and
// takes any command it is typed; nothing inside it can be relied on to keep
// what is typed to the session's folder (Neovim has no restricted mode). So
// it is the owner's alone: a controller through a link or a paste invite
// edits with the page's own keys and saves through FileWrite, which resolves
// every path as a read does.

// ErrNvimUnavailable says `nvim` is missing here, or the session offers no
// Neovim to this connection.
var ErrNvimUnavailable = errors.New("session: neovim is not available")

// nvimEditor is one open editor of one subscription.
type nvimEditor struct {
	id     string
	path   string // the resolved file
	ed     *nvim.Editor
	cancel context.CancelFunc
	gone   atomic.Bool
	// ready gates the frames: nothing before `opened` is sent.
	ready atomic.Bool
	// ending ends Neovim once, however many ways ask (end).
	ending sync.Once
}

// end ends Neovim and reaps it, once: with discard as `:qa!` does, else as
// a lost connection does. Neovim that quit on its own (`:q`) is let finish
// its exit, which removes its swap files last, before it is reaped; the
// context's cancel comes after, never during, so it is not killed halfway.
func (e *nvimEditor) end(discard bool) {
	e.ending.Do(func() {
		if e.ed != nil {
			if discard {
				e.ed.Discard()
			} else {
				e.ed.Close()
			}
		}
		if e.cancel != nil {
			e.cancel()
		}
	})
}

// nvimAvailable is `nvim` on PATH, checked once a minute at most.
var (
	nvimCheckMu sync.Mutex
	nvimCheckAt time.Time
	nvimCheck   bool
)

func nvimAvailable() bool {
	nvimCheckMu.Lock()
	defer nvimCheckMu.Unlock()
	if time.Since(nvimCheckAt) > time.Minute {
		nvimCheck, nvimCheckAt = nvim.Available(), time.Now()
	}
	return nvimCheck
}

// editAllowed reports whether role may edit files under the FileEdit policy.
func (s *Local) editAllowed(role Role) bool {
	return role == RoleControl && s.opts.FileEdit != "off" && s.fileAllowed(role)
}

// nvimAllowed reports whether sub may use the editor's Neovim: it may edit
// and it is one of the owner's own connections.
func (s *Local) nvimAllowed(sub *Subscription) bool {
	return sub.owner && s.editAllowed(sub.Role)
}

// nvimOwnerOnly reports whether sub may edit but not through Neovim, which
// is kept for the owner's own connections (the welcome's nvimOwnerOnly).
func (s *Local) nvimOwnerOnly(sub *Subscription) bool {
	return !sub.owner && s.editAllowed(sub.Role)
}

// NvimOpen starts an editor for sub on req.Path and answers with an
// nvim_event of kind opened (then the whole buffer as lines), or returns
// why not: ErrNvimUnavailable, ErrFileDenied (the path or the policy),
// ErrTooManyRequests (the bounds).
func (s *Local) NvimOpen(ctx context.Context, sub *Subscription, req proto.NvimOpen) error {
	if req.ReqID == "" || len(req.ReqID) > 64 || len(req.Path) > 4096 {
		return errors.New("session: invalid nvim request")
	}
	if !s.nvimAllowed(sub) || !nvimAvailable() {
		return ErrNvimUnavailable
	}
	target, err := ResolvePath(s.info.Cwd, req.Path, s.fileDeny())
	if err != nil {
		return ErrFileDenied
	}
	sub.nvimMu.Lock()
	if sub.nvims == nil {
		sub.nvims = map[string]*nvimEditor{}
	}
	if len(sub.nvims) >= proto.MaxNvimPerSub || s.nvimCount.Load() >= proto.MaxNvimPerSession {
		sub.nvimMu.Unlock()
		return ErrTooManyRequests
	}
	id := newNvimID()
	e := &nvimEditor{id: id, path: target}
	sub.nvims[id] = e
	s.nvimCount.Add(1)
	sub.nvimMu.Unlock()
	ectx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	h := &nvimHandler{s: s, sub: sub, e: e}
	ed, err := nvim.Open(ectx, s.info.Cwd, target, h)
	if err != nil {
		cancel()
		s.forgetNvim(sub, e)
		return errors.New("session: neovim: " + err.Error())
	}
	e.ed = ed
	sub.send(proto.MustControl(proto.NvimEvent{T: proto.CtlNvimEvent, ID: id, Kind: proto.NvimOpened, ReqID: req.ReqID, Path: target}))
	e.ready.Store(true)
	if lines, err := ed.Lines(); err == nil {
		h.Lines(0, -1, lines)
	}
	ed.Report()
	// A swap file found while the file opened was told to no one yet.
	if sw := ed.FoundSwap(); sw != nil {
		h.Swap(*sw)
	}
	return nil
}

// NvimSwap answers the swap file one of sub's editors found (round 13, G3)
// with one of the fixed choices; an answer that does not apply (no swap
// file, or recover and delete while its writer runs) comes back as the
// editor's error message.
func (s *Local) NvimSwap(sub *Subscription, req proto.NvimSwap) error {
	switch req.Choice {
	case proto.NvimSwapEdit, proto.NvimSwapRecover, proto.NvimSwapDelete:
	default:
		return errors.New("session: invalid swap choice")
	}
	if !s.nvimAllowed(sub) {
		return ErrNvimUnavailable
	}
	sub.nvimMu.Lock()
	e := sub.nvims[req.ID]
	sub.nvimMu.Unlock()
	if e == nil || e.ed == nil {
		return ErrFileDenied
	}
	if err := e.ed.SwapChoice(req.Choice); err != nil {
		h := &nvimHandler{s: s, sub: sub, e: e}
		h.emit(proto.NvimEvent{Kind: proto.NvimMessage, Text: truncateRunes(err.Error(), proto.MaxNvimLine), MessageKind: "emsg"})
	}
	return nil
}

// NvimInput sends keys to one of sub's editors.
func (s *Local) NvimInput(sub *Subscription, req proto.NvimInput) error {
	if len(req.Keys) == 0 || len(req.Keys) > proto.MaxNvimKeys {
		return errors.New("session: invalid nvim input")
	}
	if !s.nvimAllowed(sub) {
		return ErrNvimUnavailable
	}
	sub.nvimMu.Lock()
	e := sub.nvims[req.ID]
	if sub.nvimKeys.rate == 0 {
		// Keys, not posts: sized for typing (proto.NvimKeysPerSecond).
		sub.nvimKeys.rate, sub.nvimKeys.burst = proto.NvimKeysPerSecond, proto.NvimKeyBurst
	}
	ok := sub.nvimKeys.take(time.Now())
	sub.nvimMu.Unlock()
	if e == nil {
		return ErrFileDenied
	}
	if !ok {
		return ErrTooManyRequests
	}
	return e.ed.InputSeq(req.Keys, req.Seq)
}

// NvimClose ends one of sub's editors: with Discard as `:qa!` does (the
// person chose to drop the changes not written; the swap file goes), else as
// a lost connection does (the swap file of changes not written stays).
func (s *Local) NvimClose(sub *Subscription, req proto.NvimClose) {
	sub.nvimMu.Lock()
	e := sub.nvims[req.ID]
	sub.nvimMu.Unlock()
	if e != nil {
		e.gone.Store(true)
		e.end(req.Discard)
		s.forgetNvim(sub, e)
	}
}

// closeNvims ends every editor of sub (the connection is going).
func (s *Local) closeNvims(sub *Subscription) {
	sub.nvimMu.Lock()
	all := make([]*nvimEditor, 0, len(sub.nvims))
	for _, e := range sub.nvims {
		all = append(all, e)
	}
	sub.nvimMu.Unlock()
	for _, e := range all {
		e.gone.Store(true)
		e.end(false)
		s.forgetNvim(sub, e)
	}
}

// forgetNvim takes e off sub's editors and the session's count; ending it
// is end's.
func (s *Local) forgetNvim(sub *Subscription, e *nvimEditor) {
	sub.nvimMu.Lock()
	if _, ok := sub.nvims[e.id]; ok {
		delete(sub.nvims, e.id)
		s.nvimCount.Add(-1)
	}
	sub.nvimMu.Unlock()
}

// nvimHandler turns the editor's reports into frames for the subscription.
type nvimHandler struct {
	s   *Local
	sub *Subscription
	e   *nvimEditor
}

func (h *nvimHandler) emit(ev proto.NvimEvent) {
	if h.e.gone.Load() || !h.e.ready.Load() {
		return
	}
	ev.T, ev.ID = proto.CtlNvimEvent, h.e.id
	h.sub.send(proto.MustControl(ev))
}

func (h *nvimHandler) Lines(first, last int, lines []string) {
	for _, ev := range proto.NvimLineEvents(h.e.id, first, last, lines) {
		h.emit(ev)
	}
}
func (h *nvimHandler) Cursor(line, col int, mode string, vl, vc int) {
	h.emit(proto.NvimEvent{Kind: proto.NvimCursor, Line: line, Col: col, Mode: mode, VisualLine: vl, VisualCol: vc})
}
func (h *nvimHandler) Mode(mode string) { h.emit(proto.NvimEvent{Kind: proto.NvimMode, Mode: mode}) }
func (h *nvimHandler) Cmdline(show bool, content string, pos int, prompt string) {
	h.emit(proto.NvimEvent{Kind: proto.NvimCmdline, Show: show, Content: truncateRunes(content, proto.MaxNvimLine), Pos: pos, Prompt: prompt})
}
func (h *nvimHandler) Message(text, kind string) {
	h.emit(proto.NvimEvent{Kind: proto.NvimMessage, Text: truncateRunes(text, proto.MaxNvimLine), MessageKind: kind})
}
func (h *nvimHandler) Written(path string) {
	h.emit(proto.NvimEvent{Kind: proto.NvimWritten, Path: path})
	// The write is the person's: Changes and Touched follow it (design 4d, 4e).
	rel := path
	if r, err := filepath.Rel(h.s.info.Cwd, path); err == nil {
		rel = r
	}
	h.s.Record(ActivityEntry{Type: ActivityFile, Op: FileOpWrite, Path: rel, Tool: "nvim", By: h.sub.ID, ByName: h.sub.Name})
}
func (h *nvimHandler) Buffer(path string) {
	h.e.path = path
	h.emit(proto.NvimEvent{Kind: proto.NvimOpened, Path: path})
}
func (h *nvimHandler) Ack(seq uint32, line, col int, mode string, vl, vc int) {
	h.emit(proto.NvimEvent{Kind: proto.NvimCursor, Line: line, Col: col, Mode: mode, VisualLine: vl, VisualCol: vc, Ack: seq})
}
func (h *nvimHandler) Modified(modified bool) {
	h.emit(proto.NvimEvent{Kind: proto.NvimModified, Modified: modified})
}
func (h *nvimHandler) Swap(sw nvim.Swap) {
	info := &proto.NvimSwapInfo{File: sw.File, Pid: sw.Pid, Running: sw.Running, User: sw.User, Host: sw.Host, Modified: sw.Modified}
	if sw.Mtime > 0 {
		info.Mtime = time.Unix(sw.Mtime, 0).UTC().Format(time.RFC3339)
	}
	h.emit(proto.NvimEvent{Kind: proto.NvimSwapFound, Swap: info})
}
func (h *nvimHandler) Exited(reason string) {
	h.emit(proto.NvimEvent{Kind: proto.NvimClosed, Reason: reason})
	h.e.gone.Store(true)
	h.s.forgetNvim(h.sub, h.e)
	// Off this goroutine: reaping waits for the client, which runs this.
	go h.e.end(false)
}

func newNvimID() string {
	var b [8]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// truncateRunes cuts s to at most n bytes on a rune boundary.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// NvimRefused is the nvim_event of kind error for a refused request: the
// code names the reason (nvim_unavailable, too_many_requests, file_denied).
func NvimRefused(reqID string, err error) []byte {
	code := proto.ErrCodeFileDenied
	switch {
	case errors.Is(err, ErrNvimUnavailable):
		code = proto.ErrCodeNvimUnavailable
	case errors.Is(err, ErrTooManyRequests):
		code = proto.ErrCodeTooManyRequests
	}
	return proto.MustControl(proto.NvimEvent{T: proto.CtlNvimEvent, Kind: proto.NvimError, ReqID: reqID, Code: code, Message: err.Error()})
}
