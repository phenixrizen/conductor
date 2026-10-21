package hostagent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/session/sessiontest"
)

// What a viewer who joined through a link sends a host passes the link's
// check (peer.authorize: the link standing, and the server heard from
// lately), over the data channel and the relay alike: its keys and saves
// once the link went, or while the server is not heard from, are refused
// with revoked (or expired) and the words, nothing written; so are its
// reads, editor opens and chat posts. The owner's windows, a paste
// invite's viewer and the host's own terminal have no link, and the check
// never refuses them.

// editOpts is a host session's options with the editor's saves on.
func editOpts() session.Options {
	return session.Options{SubmitPause: 10 * time.Millisecond, FileView: "control", FileEdit: "control", Log: discardLog}
}

// credHost is a host of a session on a FakeProc, files and saves on, whose
// server is heard from all along; viewer attaches a viewer over a data
// channel of its own, joined through linkID ("" for one of the owner's
// windows).
func credHost(t *testing.T) (a *agent, viewer func(id, linkID string) *submitViewer) {
	t.Helper()
	a, proc, begun := newHostOf(t, editOpts(), 0, 0)
	r := &signalRouter{to: map[string]chan any{}}
	a.sendHook = r.send
	heardFromServer(t, a)
	return a, func(id, linkID string) *submitViewer {
		t.Helper()
		return submitViewerOn(t, a, proc, begun, id, linkID, r.add(id))
	}
}

// fileIn writes body to name in a's session folder and returns its path.
func fileIn(t *testing.T, a *agent, name, body string) string {
	t.Helper()
	p := filepath.Join(a.local.Info().Cwd, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// hasBody fails unless the file at p holds body.
func hasBody(t *testing.T, p, body string) {
	t.Helper()
	if b, err := os.ReadFile(p); err != nil || string(b) != body {
		t.Fatalf("%s holds %q (%v), want %q", filepath.Base(p), b, err, body)
	}
}

// saveFrame is a one-part save of body over path, forced.
func saveFrame(t *testing.T, reqID, path, body string) []byte {
	t.Helper()
	f, err := proto.EncodeFileWrite(proto.FileWrite{ReqID: reqID, Path: path, Total: int64(len(body)), Force: true}, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// saveOver sends a one-part save of body over path on the viewer's data
// channel.
func (v *submitViewer) saveOver(reqID, path, body string) {
	v.t.Helper()
	if err := v.dc.Send(saveFrame(v.t, reqID, path, body)); err != nil {
		v.t.Fatal(err)
	}
}

// fileAnswer returns the FILE frame answering reqID on the viewer's data
// channel.
func (v *submitViewer) fileAnswer(reqID string) proto.FileHeader {
	v.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-v.frames:
			f, err := proto.Decode(raw)
			if err != nil || f.Type != proto.TypeFile {
				continue
			}
			if h, _, err := proto.DecodeFile(f.Payload); err == nil && h.ReqID == reqID {
				return h
			}
		case <-deadline:
			v.t.Fatalf("no FILE frame answers %s", reqID)
		}
	}
}

// first returns the first control message on the viewer's data channel
// that want matches.
func (v *submitViewer) first(want func(m map[string]any) bool) map[string]any {
	v.t.Helper()
	got := v.until(want)
	return got[len(got)-1]
}

// refusedFor fails unless m is an error refusal with code and words.
func refusedFor(t *testing.T, m map[string]any, code, words string) {
	t.Helper()
	if m["t"] != proto.CtlError || m["code"] != code || m["message"] != words {
		t.Fatalf("refusal %v, want %s %q", m, code, words)
	}
}

// fileRefusedFor fails unless h is a FILE error with code and words.
func fileRefusedFor(t *testing.T, h proto.FileHeader, code, words string) {
	t.Helper()
	if h.Kind != "error" || h.Error == nil || h.Error.Code != code || h.Error.Message != words {
		t.Fatalf("FILE answer %+v %+v, want %s %q", h, h.Error, code, words)
	}
}

// goneWords are the words a refusal for a link gone says.
const goneWords = "this link no longer opens the session"

// linkGone tells a, as the server does, that linkID no longer opens the
// session.
func linkGone(t *testing.T, a *agent, linkID string) {
	t.Helper()
	if err := a.handleControl(t.Context(), mustJSON(proto.LinkGone{T: proto.HostLinkGone, LinkID: linkID})); err != nil {
		t.Fatal(err)
	}
}

// relayCredHost is credHost whose viewers are on the relay: its control
// connection goes to a server that reads everything it is sent (s.got),
// heard from all along. viewer attaches a relayed viewer joined through
// linkID.
func relayCredHost(t *testing.T) (a *agent, s *heldServer, proc *sessiontest.FakeProc, viewer func(id, linkID string) *peer) {
	t.Helper()
	s, c, pc := dialHeld(t, 0)
	close(s.read)
	a, proc, _ = newHostOf(t, editOpts(), 0, 0)
	a.opts.RelayOnly = true
	a.sessID = "s"
	w := a.useConn(c, pc)
	t.Cleanup(w.stop)
	heardFromServer(t, a)
	t.Cleanup(a.closeAllPeers)
	return a, s, proc, func(id, linkID string) *peer {
		t.Helper()
		a.addPeer(id, session.RoleControl, linkID, "guest")
		p := a.peer(id)
		p.startRelay()
		p.attach(proto.Hello{T: proto.CtlHello, Proto: proto.ProtoVersion, Cols: 80, Rows: 24}, nil)
		p.mu.Lock()
		attached := p.sub != nil
		p.mu.Unlock()
		if !attached {
			t.Fatalf("%s never attached", id)
		}
		return p
	}
}

// relayedTo returns the next frame s gets relayed to viewer id that want
// matches.
func relayedTo(t *testing.T, s *heldServer, id string, want func(proto.Frame) bool) proto.Frame {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case data := <-s.got:
			if len(data) < 1+proto.ViewerIDLen || data[0] != proto.TypeRelay {
				continue
			}
			viewer, inner, err := proto.DecodeRelay(data[1:])
			if err == nil && viewer == id && want(inner) {
				return inner
			}
		case <-deadline:
			t.Fatalf("nothing of that kind was relayed to %s", id)
		}
	}
}

// relayedError is the next error message s gets relayed to viewer id.
func relayedError(t *testing.T, s *heldServer, id string) map[string]any {
	t.Helper()
	var m map[string]any
	relayedTo(t, s, id, func(f proto.Frame) bool {
		if f.Type != proto.TypeControl {
			return false
		}
		m = map[string]any{}
		return json.Unmarshal(f.Payload, &m) == nil && m["t"] == proto.CtlError
	})
	return m
}

// relayedFile is the FILE frame s gets relayed to viewer id answering reqID.
func relayedFile(t *testing.T, s *heldServer, id, reqID string) proto.FileHeader {
	t.Helper()
	var h proto.FileHeader
	relayedTo(t, s, id, func(f proto.Frame) bool {
		if f.Type != proto.TypeFile {
			return false
		}
		got, _, err := proto.DecodeFile(f.Payload)
		h = got
		return err == nil && got.ReqID == reqID
	})
	return h
}

// typedNothing fails when anything is written to proc within d.
func typedNothing(t *testing.T, proc *sessiontest.FakeProc, d time.Duration) {
	t.Helper()
	select {
	case b := <-proc.Input:
		t.Fatalf("the process was written %q", b)
	case <-time.After(d):
	}
}

// typedNext returns the next write to proc.
func typedNext(t *testing.T, proc *sessiontest.FakeProc) string {
	t.Helper()
	select {
	case b := <-proc.Input:
		return string(b)
	case <-time.After(10 * time.Second):
		t.Fatal("nothing was written to the process")
	}
	return ""
}

// A link's keys once the server said the link went are refused (revoked,
// with the words) and never written, over the data channel and the relay;
// the owner's keys go on.
func TestLinkKeysRefusedAfterLinkGone(t *testing.T) {
	t.Run("data channel", func(t *testing.T) {
		a, viewer := credHost(t)
		guest := viewer("0123456789abcdef", "l1")
		owner := viewer("fedcba9876543210", "")
		guest.key("a")
		if got := guest.release(); got != "a" {
			t.Fatalf("typed %q", got)
		}
		linkGone(t, a, "l1")
		guest.key("b")
		refusedFor(t, guest.first(func(m map[string]any) bool { return m["t"] == proto.CtlError }), proto.ErrCodeRevoked, goneWords)
		guest.quiet(300 * time.Millisecond)
		owner.key("o")
		if got := owner.release(); got != "o" {
			t.Fatalf("typed %q", got)
		}
	})
	t.Run("relay", func(t *testing.T) {
		a, s, proc, viewer := relayCredHost(t)
		guest := viewer("guest01234567890", "l1")
		owner := viewer("owner01234567890", "")
		linkGone(t, a, "l1")
		guest.handleFrame(proto.Frame{Type: proto.TypeInput, Payload: []byte("b")})
		refusedFor(t, relayedError(t, s, guest.id), proto.ErrCodeRevoked, goneWords)
		typedNothing(t, proc, 300*time.Millisecond)
		owner.handleFrame(proto.Frame{Type: proto.TypeInput, Payload: []byte("o")})
		if got := typedNext(t, proc); got != "o" {
			t.Fatalf("typed %q", got)
		}
	})
}

// A link's save once the server said the link went is refused (revoked,
// with the words) at its part, over the data channel and the relay: the
// folder is as it was. The owner's save goes on.
func TestLinkSaveRefusedAfterLinkGone(t *testing.T) {
	t.Run("data channel", func(t *testing.T) {
		a, viewer := credHost(t)
		file := fileIn(t, a, "notes.txt", "alpha\n")
		guest := viewer("0123456789abcdef", "l1")
		owner := viewer("fedcba9876543210", "")
		linkGone(t, a, "l1")
		guest.saveOver("w1", "notes.txt", "guest's\n")
		fileRefusedFor(t, guest.fileAnswer("w1"), proto.ErrCodeRevoked, goneWords)
		hasBody(t, file, "alpha\n")
		owner.saveOver("w2", "notes.txt", "owner's\n")
		if h := owner.fileAnswer("w2"); h.Kind != "written" {
			t.Fatalf("the owner's save: %+v %+v", h, h.Error)
		}
		hasBody(t, file, "owner's\n")
	})
	t.Run("relay", func(t *testing.T) {
		a, s, _, viewer := relayCredHost(t)
		file := fileIn(t, a, "notes.txt", "alpha\n")
		guest := viewer("guest01234567890", "l1")
		linkGone(t, a, "l1")
		guest.handleFrame(proto.Frame{Type: proto.TypeFileWrite, Payload: saveFrame(t, "w1", "notes.txt", "guest's\n")[1:]})
		fileRefusedFor(t, relayedFile(t, s, guest.id, "w1"), proto.ErrCodeRevoked, goneWords)
		hasBody(t, file, "alpha\n")
	})
}

// A link's read, editor open, chat post and send to the agent once the
// server said the link went are each refused (revoked, with the words) as
// it is taken, and do nothing: no file read, no editor, nothing kept in
// the chat or typed. The owner's read and post go on.
func TestLinkReadOpenAndChatRefusedAfterLinkGone(t *testing.T) {
	a, viewer := credHost(t)
	fileIn(t, a, "notes.txt", "alpha\n")
	guest := viewer("0123456789abcdef", "l1")
	owner := viewer("fedcba9876543210", "")
	guest.send(proto.ChatPost{T: proto.CtlChat, Nonce: "c0", Text: "kept before"})
	ref, _ := guest.first(func(m map[string]any) bool { return m["t"] == proto.CtlChat && m["nonce"] == "c0" })["id"].(string)
	before := len(a.local.ChatHistory())
	linkGone(t, a, "l1")

	guest.send(proto.FileGet{T: proto.CtlFileGet, ReqID: "r1", Path: "notes.txt"})
	fileRefusedFor(t, guest.fileAnswer("r1"), proto.ErrCodeRevoked, goneWords)

	guest.send(proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "n1", Path: "notes.txt"})
	if m := guest.first(func(m map[string]any) bool { return m["t"] == proto.CtlNvimEvent && m["reqId"] == "n1" }); m["kind"] != proto.NvimError || m["code"] != proto.ErrCodeRevoked || m["message"] != goneWords {
		t.Fatalf("the open: %v", m)
	}

	guest.send(proto.ChatPost{T: proto.CtlChat, Nonce: "c1", Text: "after"})
	refusedFor(t, guest.first(func(m map[string]any) bool { return m["t"] == proto.CtlError && m["requestId"] == "c1" }), proto.ErrCodeRevoked, goneWords)
	guest.send(proto.ChatSend{T: proto.CtlChatSend, Ref: ref})
	refusedFor(t, guest.first(func(m map[string]any) bool { return m["t"] == proto.CtlError && m["requestId"] == ref }), proto.ErrCodeRevoked, goneWords)
	guest.quiet(300 * time.Millisecond)
	if n := len(a.local.ChatHistory()); n != before {
		t.Fatalf("the chat holds %d messages, %d before", n, before)
	}

	owner.send(proto.FileGet{T: proto.CtlFileGet, ReqID: "r2", Path: "notes.txt"})
	if h := owner.fileAnswer("r2"); h.Kind != "file" {
		t.Fatalf("the owner's read: %+v %+v", h, h.Error)
	}
	owner.send(proto.ChatPost{T: proto.CtlChat, Nonce: "c2", Text: "the owner's"})
	owner.until(func(m map[string]any) bool { return m["t"] == proto.CtlChat && m["nonce"] == "c2" })
}

// liveEditHost is liveHost whose session takes saves.
func liveEditHost(t *testing.T) (a *agent, px *stallProxy, viewer func(id, linkID string) *submitViewer) {
	t.Helper()
	_, hs := startServer(t)
	px = newStallProxy(t, strings.TrimPrefix(hs.URL, "http://"))
	a, viewer = liveHostOf(t, px.url(), 0, editOpts())
	return a, px, viewer
}

// quietWords are the words a link's refusal says while the server is not
// heard from within liveHost's bound.
func quietWords() string { return serverQuiet{liveTestWithin}.Error() }

// While the server is not heard from within the bound, a link's keys are
// refused (revoked, with words saying so) and never written; the owner's
// are written. Once the server is heard again, the link's keys are written.
func TestLinkKeysRefusedWhenPingsGoUnanswered(t *testing.T) {
	a, px, viewer := liveHost(t, 10*time.Millisecond)
	guest := viewer("0123456789abcdef", "l1")
	owner := viewer("fedcba9876543210", "")
	px.stall()
	untilVouches(t, a, false)
	guest.key("g")
	refusedFor(t, guest.first(func(m map[string]any) bool { return m["t"] == proto.CtlError }), proto.ErrCodeRevoked, quietWords())
	guest.quiet(300 * time.Millisecond)
	owner.key("o")
	if got := owner.release(); got != "o" {
		t.Fatalf("typed %q", got)
	}
	px.resume()
	untilVouches(t, a, true)
	guest.key("again")
	if got := guest.release(); got != "again" {
		t.Fatalf("typed %q", got)
	}
}

// While the server is not heard from within the bound, a link's save is
// refused (revoked, with words saying so), the folder as it was; the
// owner's is written. Once the server is heard again, the link's save is
// written.
func TestLinkSaveRefusedWhenPingsGoUnanswered(t *testing.T) {
	a, px, viewer := liveEditHost(t)
	file := fileIn(t, a, "notes.txt", "alpha\n")
	guest := viewer("0123456789abcdef", "l1")
	owner := viewer("fedcba9876543210", "")
	px.stall()
	untilVouches(t, a, false)
	guest.saveOver("w1", "notes.txt", "guest's\n")
	fileRefusedFor(t, guest.fileAnswer("w1"), proto.ErrCodeRevoked, quietWords())
	hasBody(t, file, "alpha\n")
	owner.saveOver("w2", "notes.txt", "owner's\n")
	if h := owner.fileAnswer("w2"); h.Kind != "written" {
		t.Fatalf("the owner's save: %+v %+v", h, h.Error)
	}
	px.resume()
	untilVouches(t, a, true)
	guest.saveOver("w3", "notes.txt", "guest's\n")
	if h := guest.fileAnswer("w3"); h.Kind != "written" {
		t.Fatalf("the link's save once the server is heard: %+v %+v", h, h.Error)
	}
	hasBody(t, file, "guest's\n")
}

// The viewers with no link (one of the owner's windows, a paste invite's
// viewer) and the host's own terminal are never refused by the link's
// check: with no server heard from at all and every link gone, their keys
// are written and their saves too.
func TestOwnerWindowAndPasteViewerKeysNeedNoVouch(t *testing.T) {
	a, proc, begun := newHostOf(t, editOpts(), 0, 0)
	r := &signalRouter{to: map[string]chan any{}}
	a.sendHook = r.send
	file := fileIn(t, a, "notes.txt", "alpha\n")
	if a.vouches() == nil {
		t.Fatal("a host with no connection vouches for its links")
	}
	a.creds.lost()
	owner := submitViewerOn(t, a, proc, begun, "fedcba9876543210", "", r.add("fedcba9876543210"))
	paste := submitPeerOn(t, a, proc, begun, newPeer(a, "pastepastepastep", session.RoleControl, "", "paste", false), r.add("pastepastepastep"))
	for i, v := range []*submitViewer{owner, paste} {
		keys := []string{"owner's", "paste's"}[i]
		v.key(keys)
		if got := typedNext(t, proc); got != keys {
			t.Fatalf("typed %q, want %q", got, keys)
		}
		v.saveOver("w"+keys, "notes.txt", keys+"\n")
		if h := v.fileAnswer("w" + keys); h.Kind != "written" {
			t.Fatalf("%s save: %+v %+v", keys, h, h.Error)
		}
		hasBody(t, file, keys+"\n")
	}
	sub, err := a.local.AttachWith(localTerminal(80, 24), sinkFunc(func([]byte) {}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.local.Detach(sub) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.local.InputWaiting(ctx, sub, []byte("terminal's")); err != nil {
		t.Fatalf("the host's own terminal: %v", err)
	}
	if got := typedNext(t, proc); got != "terminal's" {
		t.Fatalf("typed %q", got)
	}
}

// A link's viewer whose keys wait (the program not reading) when the host
// loses its connection to the server has none of the waiting ones written
// (the one already begun lands as the program reads), and one the server
// lets join through the same link once the connection is back starts
// afresh: its keys are written. A viewer that only left has its waiting
// keys dropped too.
func TestKeysOfAViewerThatLeftAreDropped(t *testing.T) {
	a, viewer := credHost(t)
	guest := viewer("0123456789abcdef", "l1")
	guest.hold()
	guest.key("begun")
	guest.untilBegun(1)
	guest.key("waits")
	guest.sync()
	a.connLost(nil)
	if got := guest.release(); got != "begun" {
		t.Fatalf("typed %q, want the begun write", got)
	}
	guest.quiet(300 * time.Millisecond)

	again := viewer("1123456789abcdef", "l1")
	again.key("afresh")
	if got := typedNext(t, again.proc); got != "afresh" {
		t.Fatalf("typed %q", got)
	}

	other := viewer("2123456789abcdef", "l2")
	other.hold()
	other.key("begun")
	other.untilBegun(3)
	other.key("waits")
	other.sync()
	if err := a.handleControl(t.Context(), leaveOf(other.p.id, "")); err != nil {
		t.Fatal(err)
	}
	if got := other.release(); got != "begun" {
		t.Fatalf("typed %q, want the begun write", got)
	}
	other.quiet(300 * time.Millisecond)
}

// A save part that cannot be read, through a host, is a protocol error: the
// viewer is let go with bad_frame (a viewer_error to the server, which
// closes the viewer), as one whose hello cannot be read is, and nothing is
// written. (Before this the error came with no save id and the viewer
// stayed.)
func TestAMalformedSavePartLetsTheViewerGo(t *testing.T) {
	a, s, _, viewer := relayCredHost(t)
	file := fileIn(t, a, "notes.txt", "alpha\n")
	guest := viewer("guest01234567890", "l1")
	// A header said to be 5 KiB long, past proto.MaxWriteHeader, in a part of 20 bytes.
	part := make([]byte, 20)
	copy(part[8:12], []byte{0, 0, 0x14, 0}) // the header's length, 5 KiB, big-endian
	guest.handleFrame(proto.Frame{Type: proto.TypeFileWrite, Payload: part})
	deadline := time.After(10 * time.Second)
	for {
		select {
		case data := <-s.got:
			if !bytes.Contains(data, []byte(`"t":"viewer_error"`)) {
				continue
			}
			var m proto.ViewerError
			if json.Unmarshal(data, &m) != nil || m.ViewerID != guest.id || m.Code != proto.ErrCodeBadFrame || m.Message != "malformed file write" {
				t.Fatalf("the viewer was let go with %s", data)
			}
			hasBody(t, file, "alpha\n")
			return
		case <-deadline:
			t.Fatal("the host did not let the viewer go")
		}
	}
}
