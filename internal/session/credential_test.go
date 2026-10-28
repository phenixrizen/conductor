package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/proto"
)

// What a viewer who joined through a link sends passes the link's check
// (AttachOptions.Authorize, asked again by revoked): a keystroke and a
// save's part as the write is queued and again as it is taken, the save's
// rename once more, a read, an editor's open and a chat post as each is
// taken. A refusal says why (revoked, expired) and does nothing. A viewer
// with no link has no credential and is never refused by it; the role is
// asked first.

// credential stands in for a link's credential as the viewer's
// AttachOptions.Authorize asks it: it stands until goneFor.
type credential struct{ gone atomic.Pointer[error] }

func (c *credential) goneFor(why error) { c.gone.Store(&why) }

func (c *credential) check(role Role) func() (Grant, error) {
	return func() (Grant, error) {
		if why := c.gone.Load(); why != nil {
			return Grant{}, *why
		}
		return Grant{Role: role}, nil
	}
}

// linkViewer attaches a viewer of role through linkID, its credential c.
func linkViewer(t *testing.T, s *Local, linkID string, role Role, c *credential) (*Subscription, *chanSink) {
	t.Helper()
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: role, Name: "guest", LinkID: linkID, Authorize: c.check(role)}, sink)
	if err != nil {
		t.Fatal(err)
	}
	return sub, sink
}

// quietKind is a kind of ErrRevoked with words of its own, as a host's that
// has not heard from its server lately is.
type quietKind struct{}

func (quietKind) Error() string        { return "the server has not been heard from lately" }
func (quietKind) Is(target error) bool { return target == ErrRevoked }

// The ways a link no longer opens the session, each with the code and the
// words a refusal answers with.
var linkEnds = []struct {
	name  string
	why   error
	code  string
	words string
}{
	{"revoked", ErrRevoked, proto.ErrCodeRevoked, "this link no longer opens the session"},
	{"expired", ErrExpired, proto.ErrCodeExpired, "this link has expired"},
	{"server quiet", quietKind{}, proto.ErrCodeRevoked, "the server has not been heard from lately"},
}

// A link's refusal names its code and its words; any other error is none.
func TestLinkRefusalNamesHowTheLinkEnded(t *testing.T) {
	for _, e := range linkEnds {
		code, words, ok := LinkRefusal(e.why)
		if !ok || code != e.code || words != e.words {
			t.Errorf("%s: %q %q %v", e.name, code, words, ok)
		}
	}
	for _, err := range []error{nil, ErrReadOnly, ErrInputStalled, ErrSessionEnded, errGone} {
		if _, _, ok := LinkRefusal(err); ok {
			t.Errorf("%v named a link's end", err)
		}
	}
}

// A link's keystrokes once its credential is gone, the connection still
// attached (the session has not closed it yet), are refused with why and
// never written; the owner's are written.
func TestInputRefusedOnceCredentialGone(t *testing.T) {
	for _, e := range linkEnds {
		t.Run(e.name, func(t *testing.T) {
			s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second})
			var c credential
			guest, _ := linkViewer(t, s, "l1", RoleControl, &c)
			if err := s.Input(guest, []byte("before")); err != nil {
				t.Fatal(err)
			}
			if got := written(t, p); got != "before" {
				t.Fatalf("written %q", got)
			}
			c.goneFor(e.why)
			if err := s.Input(guest, []byte("after")); !errors.Is(err, e.why) {
				t.Fatalf("keys once the link went: %v", err)
			}
			nothingWritten(t, p, 200*time.Millisecond)
			owner := ownerOf(t, s)
			if err := s.Input(owner, []byte("owner's")); err != nil {
				t.Fatal(err)
			}
			if got := written(t, p); got != "owner's" {
				t.Fatalf("written %q", got)
			}
		})
	}
}

// A link's keystrokes waiting behind another write when its credential
// goes are refused as the writer takes them, never written: whether the
// session closed the connection for its link (the waiter is woken) or only
// the credential went (the writer alone finds it, procWrite.over).
func TestInputQueuedBeforeCredentialGoneIsNotWritten(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "credential gone", true: "connection closed"}[closed], func(t *testing.T) {
			s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: time.Minute})
			takeAgain := stopTaking(t, p)
			owner := ownerOf(t, s)
			go s.Input(owner, []byte("owner's"))
			untilWaiting(t, s, 0)
			var c credential
			guest, _ := linkViewer(t, s, "l1", RoleControl, &c)
			done := make(chan error, 1)
			go func() { done <- s.Input(guest, []byte("guest's")) }()
			untilWaiting(t, s, 1)
			c.goneFor(ErrRevoked)
			if closed {
				s.DisconnectLink("l1")
			}
			takeAgain()
			if got := written(t, p); got != "owner's" {
				t.Fatalf("written %q", got)
			}
			nothingWritten(t, p, 300*time.Millisecond)
			if err := returnsWithin(t, 5*time.Second, "the link's keys", func() error { return <-done }); !errors.Is(err, ErrRevoked) {
				t.Fatalf("the link's keys: %v", err)
			}
		})
	}
}

// A write begun before the link went (the program not reading) lands as the
// program reads, the one exception: the keys queued behind it are refused
// as the writer takes them, never begun.
func TestBegunWriteLandsAndNothingQueuedBehindItDoes(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "credential gone", true: "connection closed"}[closed], func(t *testing.T) {
			s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: time.Minute})
			var writes atomic.Int32
			release := make(chan struct{})
			t.Cleanup(func() {
				select {
				case <-release:
				default:
					close(release)
				}
			})
			p.onWrite = func() {
				writes.Add(1)
				<-release
			}
			var c credential
			guest, _ := linkViewer(t, s, "l1", RoleControl, &c)
			key := make(chan error, 1)
			go func() { key <- s.Input(guest, []byte("y")) }()
			untilWrites(t, &writes, 1) // begun: the program takes nothing
			enter := make(chan error, 1)
			go func() { enter <- s.Input(guest, []byte("\r")) }()
			untilWaiting(t, s, 1)
			c.goneFor(ErrRevoked)
			if closed {
				s.DisconnectLink("l1")
				// The connection's end wakes both waiters: the Enter is
				// refused before the program reads, and the begun key's
				// sender is told the connection is gone while its key
				// still lands when the program reads again. Both are
				// waited for before the program reads: a key seen landed
				// before its waiter saw the connection's end would be
				// reported written, which is the order under load this
				// test does not assert (#220).
				if err := returnsWithin(t, 5*time.Second, "the Enter", func() error { return <-enter }); !errors.Is(err, ErrRevoked) {
					t.Fatalf("the Enter: %v", err)
				}
				if err := returnsWithin(t, 5*time.Second, "the begun key", func() error { return <-key }); !errors.Is(err, ErrRevoked) {
					t.Fatalf("the begun key: %v", err)
				}
			}
			close(release)
			if got := written(t, p); got != "y" {
				t.Fatalf("written %q, want the begun write", got)
			}
			nothingWritten(t, p, 300*time.Millisecond)
			if !closed {
				if err := returnsWithin(t, 5*time.Second, "the Enter", func() error { return <-enter }); !errors.Is(err, ErrRevoked) {
					t.Fatalf("the Enter: %v", err)
				}
				// The credential gone alone leaves the begun key be: written.
				if err := returnsWithin(t, 5*time.Second, "the key", func() error { return <-key }); err != nil {
					t.Fatalf("the begun key: %v", err)
				}
			}
			if n := writes.Load(); n != 1 {
				t.Fatalf("%d writes reached the program", n)
			}
		})
	}
}

// fileIn makes a file of body in s's folder and returns its path.
func fileIn(t *testing.T, s *Local, name, body string) string {
	t.Helper()
	p := filepath.Join(s.info.Cwd, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// leftBeside lists what the file's directory holds besides it: a save's
// temporary file left behind among it.
func leftBeside(t *testing.T, file string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil {
		t.Fatal(err)
	}
	var other []string
	for _, e := range entries {
		if e.Name() != filepath.Base(file) {
			other = append(other, e.Name())
		}
	}
	return other
}

// A viewer attaches on its credential alone (AttachOptions.Authorize),
// whatever AttachOptions.Fresh says: a host's first viewer after a start,
// a reconnect or an idle spell attaches before the host vouches for its
// server. What it sends is held to both: while Fresh refuses, its keys, a
// save's part, a read and a chat post are refused with Fresh's words and do
// nothing; once Fresh passes, its keys are written and its save lands.
func TestAttachNeedsNoFreshnessAndAWriteDoes(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, FileView: "control", FileEdit: "control"})
	file := fileIn(t, s, "notes.txt", "alpha\n")
	var stale atomic.Bool
	stale.Store(true)
	fresh := func() error {
		if stale.Load() {
			return quietKind{}
		}
		return nil
	}
	var c credential
	sink := newChanSink(false)
	guest, err := s.AttachWith(AttachOptions{Role: RoleControl, Name: "guest", LinkID: "l1", Authorize: c.check(RoleControl), Fresh: fresh}, sink)
	if err != nil {
		t.Fatalf("an attach with its credential standing and no freshness yet: %v", err)
	}
	if err := s.Input(guest, []byte("early")); !errors.Is(err, quietKind{}) {
		t.Fatalf("keys before the host vouches: %v", err)
	}
	save(s, guest, "w1", "notes.txt", []byte("early\n"), 512, "", true)
	if h := fileReply(t, sink, "w1"); h.Kind != "error" || h.Error.Code != proto.ErrCodeRevoked || h.Error.Message != (quietKind{}).Error() {
		t.Fatalf("a save before the host vouches: %+v %+v", h, h.Error)
	}
	if err := s.FileGet(guest, proto.FileGet{ReqID: "r1", Path: "notes.txt"}); !errors.Is(err, quietKind{}) {
		t.Fatalf("a read before the host vouches: %v", err)
	}
	if _, err := s.Chat(guest, proto.ChatPost{Text: "early"}); !errors.Is(err, quietKind{}) {
		t.Fatalf("a chat post before the host vouches: %v", err)
	}
	nothingWritten(t, p, 200*time.Millisecond)
	if b, _ := os.ReadFile(file); string(b) != "alpha\n" {
		t.Fatalf("the file is %q", b)
	}

	stale.Store(false)
	if err := s.Input(guest, []byte("vouched")); err != nil {
		t.Fatal(err)
	}
	if got := written(t, p); got != "vouched" {
		t.Fatalf("written %q", got)
	}
	save(s, guest, "w2", "notes.txt", []byte("vouched\n"), 512, "", true)
	if h := fileReply(t, sink, "w2"); h.Kind != "written" {
		t.Fatalf("a save once the host vouches: %+v %+v", h, h.Error)
	}
	if b, _ := os.ReadFile(file); string(b) != "vouched\n" {
		t.Fatalf("the file is %q", b)
	}
	// The credential gone refuses as before, Fresh passing or not.
	c.goneFor(ErrRevoked)
	if err := s.Input(guest, []byte("gone")); !errors.Is(err, ErrRevoked) || errors.Is(err, quietKind{}) {
		t.Fatalf("keys once the link went: %v", err)
	}
}

// A link's save once its credential is gone is refused with why at its
// part, the file as it was, nothing recorded.
func TestFileWriteRefusedOnceCredentialGone(t *testing.T) {
	for _, e := range linkEnds {
		t.Run(e.name, func(t *testing.T) {
			s, _ := newLocal(t, t.TempDir())
			file := fileIn(t, s, "notes.txt", "alpha\n")
			var c credential
			guest, sink := linkViewer(t, s, "l1", RoleControl, &c)
			c.goneFor(e.why)
			save(s, guest, "w1", "notes.txt", []byte("beta\n"), 512, "", true)
			h := fileReply(t, sink, "w1")
			if h.Kind != "error" || h.Error.Code != e.code || h.Error.Message != e.words {
				t.Fatalf("the save once the link went: %+v %+v", h, h.Error)
			}
			if b, _ := os.ReadFile(file); string(b) != "alpha\n" {
				t.Fatalf("the file is %q", b)
			}
			for _, a := range s.Activity() {
				if a.Type == ActivityFile && a.Op == FileOpWrite {
					t.Fatalf("a refused save was recorded: %+v", a)
				}
			}
		})
	}
}

// A link's credential that goes in the middle of a save refuses the next
// part and drops the save: what was taken never reaches the file, and the
// parts after it, the credential standing again (a host that hears from its
// server again), are not taken as its continuation.
func TestFileWriteRefusedWhenCredentialGoesMidSave(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	file := fileIn(t, s, "notes.txt", "alpha\n")
	var c credential
	guest, sink := linkViewer(t, s, "l1", RoleControl, &c)
	body := []byte(strings.Repeat("a line of the save\n", 20))
	part := func(off, end int) {
		s.FileWrite(guest, proto.FileWrite{ReqID: "w1", Path: "notes.txt", Offset: int64(off), Total: int64(len(body)), Force: true}, body[off:end])
	}
	part(0, 100)
	part(100, 200)
	c.goneFor(ErrRevoked)
	part(200, 300)
	if h := fileReply(t, sink, "w1"); h.Kind != "error" || h.Error.Code != proto.ErrCodeRevoked {
		t.Fatalf("the part once the link went: %+v %+v", h, h.Error)
	}
	c.gone.Store(nil)
	sink.mu.Lock()
	sink.frames = nil
	sink.mu.Unlock()
	part(300, len(body))
	if h := fileReply(t, sink, "w1"); h.Kind != "error" || h.Error.Code != "out_of_order" {
		t.Fatalf("the rest of the dropped save: %+v %+v", h, h.Error)
	}
	if b, _ := os.ReadFile(file); string(b) != "alpha\n" {
		t.Fatalf("the file is %q", b)
	}
}

// A save whose last part was taken while the link stood, and whose link
// goes while its work is under way (its checks, its hash, its temporary
// file), is refused just before its rename: the temporary file goes and
// the file stays as it was.
func TestSaveRefusedWhenCredentialGoesBeforeTheRename(t *testing.T) {
	for _, e := range linkEnds {
		t.Run(e.name, func(t *testing.T) {
			s, _ := newLocal(t, t.TempDir())
			file := fileIn(t, s, "notes.txt", "alpha\n")
			read, _ := ReadPath(s.info.Cwd, "notes.txt", false, nil)
			var c credential
			guest, sink := linkViewer(t, s, "l1", RoleControl, &c)
			held, release := make(chan struct{}), make(chan struct{})
			onceAfterCheck(t, func() {
				close(held)
				<-release
			})
			go save(s, guest, "w1", "notes.txt", []byte("beta\n"), 512, read.Sha256, false)
			select {
			case <-held:
			case <-time.After(5 * time.Second):
				t.Fatal("the save never reached its checks")
			}
			c.goneFor(e.why)
			close(release)
			h := fileReply(t, sink, "w1")
			if h.Kind != "error" || h.Error.Code != e.code || h.Error.Message != e.words {
				t.Fatalf("the save whose link went before its rename: %+v %+v", h, h.Error)
			}
			if b, _ := os.ReadFile(file); string(b) != "alpha\n" {
				t.Fatalf("the file is %q", b)
			}
			if left := leftBeside(t, file); len(left) != 0 {
				t.Fatalf("left beside the file: %q", left)
			}
		})
	}
}

// A viewer with no link (one of the owner's windows; a terminal of the
// person's own, attached with no credential at all) is never refused by
// the link's check, its keys and saves written whatever links went; a
// link's view-role viewer is refused for its role first, its link gone or
// not.
func TestInputAndFileWriteNeedNoCredentialWithoutALink(t *testing.T) {
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second})
	file := fileIn(t, s, "notes.txt", "alpha\n")
	var c credential
	guest, _ := linkViewer(t, s, "l1", RoleControl, &c)
	viewer, viewerSink := linkViewer(t, s, "l2", RoleView, &c)
	c.goneFor(ErrRevoked)
	s.DisconnectLink("l1")
	<-guest.Done()
	for _, o := range []AttachOptions{
		{Role: RoleControl, Name: "owner", Owner: true},
		{Role: RoleControl, Name: "host terminal"},
	} {
		sink := newChanSink(false)
		sub, err := s.AttachWith(o, sink)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Input(sub, []byte(sub.Name)); err != nil {
			t.Fatalf("%s: %v", sub.Name, err)
		}
		if got := written(t, p); got != sub.Name {
			t.Fatalf("written %q", got)
		}
		save(s, sub, "w-"+sub.ID, "notes.txt", []byte(sub.Name+"\n"), 512, "", true)
		if h := fileReply(t, sink, "w-"+sub.ID); h.Kind != "written" {
			t.Fatalf("%s's save: %+v %+v", sub.Name, h, h.Error)
		}
		if b, _ := os.ReadFile(file); string(b) != sub.Name+"\n" {
			t.Fatalf("the file is %q", b)
		}
	}
	if err := s.Input(viewer, []byte("x")); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("a view link's keys: %v", err)
	}
	s.FileWrite(viewer, proto.FileWrite{ReqID: "v1", Path: "notes.txt", Total: 1, Force: true}, []byte("x"))
	if h := fileReply(t, viewerSink, "v1"); h.Error == nil || h.Error.Code != "read_only" {
		t.Fatalf("a view link's save: %+v", h)
	}
	nothingWritten(t, p, 200*time.Millisecond)
}

// A link's read once its credential is gone is refused with why, as it is
// taken: nothing of the folder is looked at, and no FILE frame but the
// refusal answers it. A view link's read past the policy is refused for
// that first.
func TestFileGetRefusedOnceCredentialGone(t *testing.T) {
	for _, e := range linkEnds {
		t.Run(e.name, func(t *testing.T) {
			s, _ := newLocal(t, t.TempDir())
			fileIn(t, s, "notes.txt", "alpha\n")
			var looked atomic.Int32
			setAfterCheck(t, func() { looked.Add(1) })
			var c credential
			guest, sink := linkViewer(t, s, "l1", RoleView, &c)
			if err := s.FileGet(guest, proto.FileGet{ReqID: "r0", Path: "notes.txt"}); err != nil {
				t.Fatal(err)
			}
			if h := fileReply(t, sink, "r0"); h.Kind != "file" {
				t.Fatalf("the read while the link stood: %+v", h)
			}
			looked.Store(0)
			c.goneFor(e.why)
			req := proto.FileGet{ReqID: "r1", Path: "notes.txt"}
			err := s.FileGet(guest, req)
			if !errors.Is(err, e.why) {
				t.Fatalf("the read once the link went: %v", err)
			}
			f, ferr := proto.Decode(FileGetRefused(req, err))
			if ferr != nil || f.Type != proto.TypeFile {
				t.Fatalf("the refusal's frame: %v", ferr)
			}
			h, body, _ := proto.DecodeFile(f.Payload)
			if h.ReqID != "r1" || h.Kind != "error" || h.Error.Code != e.code || h.Error.Message != e.words || len(body) != 0 {
				t.Fatalf("the refusal: %+v %+v", h, h.Error)
			}
			time.Sleep(100 * time.Millisecond)
			if n := looked.Load(); n != 0 {
				t.Fatalf("the refused read looked at the folder %d times", n)
			}
			sink.mu.Lock()
			for _, fr := range sink.frames {
				if g, err := proto.Decode(fr); err == nil && g.Type == proto.TypeFile {
					if gh, _, _ := proto.DecodeFile(g.Payload); gh.ReqID == "r1" {
						t.Errorf("the refused read was answered %+v", gh)
					}
				}
			}
			sink.mu.Unlock()
		})
	}
	s, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, FileView: "control"})
	var c credential
	viewer, _ := linkViewer(t, s, "l1", RoleView, &c)
	c.goneFor(ErrRevoked)
	if err := s.FileGet(viewer, proto.FileGet{ReqID: "r2", Path: "notes.txt"}); !errors.Is(err, ErrFileDenied) {
		t.Fatalf("a view link's read past the policy: %v", err)
	}
}

// A link's editor open once its credential is gone is refused with why
// (the editor's error event names it), nothing started; while the link
// stands it is the Neovim a link is never offered, and a view link is
// refused for its role first.
func TestNvimOpenRefusedOnceCredentialGone(t *testing.T) {
	for _, e := range linkEnds {
		t.Run(e.name, func(t *testing.T) {
			s, _ := newLocal(t, t.TempDir())
			fileIn(t, s, "notes.txt", "alpha\n")
			var c credential
			guest, _ := linkViewer(t, s, "l1", RoleControl, &c)
			view, _ := linkViewer(t, s, "l2", RoleView, &c)
			req := proto.NvimOpen{T: proto.CtlNvimOpen, ReqID: "n1", Path: "notes.txt"}
			if err := s.NvimOpen(context.Background(), guest, req); !errors.Is(err, ErrNvimUnavailable) {
				t.Fatalf("a link's open while it stands: %v", err)
			}
			c.goneFor(e.why)
			err := s.NvimOpen(context.Background(), guest, req)
			if !errors.Is(err, e.why) {
				t.Fatalf("the open once the link went: %v", err)
			}
			if n := s.nvimCount.Load(); n != 0 {
				t.Fatalf("%d editors started", n)
			}
			m := decodeControl(t, NvimRefused(req.ReqID, err))
			if m["kind"] != proto.NvimError || m["code"] != e.code || m["message"] != e.words || m["reqId"] != "n1" {
				t.Fatalf("the refusal: %v", m)
			}
			if err := s.NvimOpen(context.Background(), view, req); !errors.Is(err, ErrNvimUnavailable) {
				t.Fatalf("a view link's open: %v", err)
			}
		})
	}
}

// A link's chat post once its credential is gone is refused with why,
// nothing kept or sent; so is a send of a kept message to the agent,
// nothing typed. The owner's post and send go on.
func TestChatPostRefusedOnceCredentialGone(t *testing.T) {
	for _, e := range linkEnds {
		t.Run(e.name, func(t *testing.T) {
			s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, InputWait: 5 * time.Second, SubmitPause: 10 * time.Millisecond})
			var c credential
			guest, _ := linkViewer(t, s, "l1", RoleControl, &c)
			viewer, _ := linkViewer(t, s, "l2", RoleView, &c)
			kept, err := s.Chat(guest, proto.ChatPost{Text: "kept before"})
			if err != nil {
				t.Fatal(err)
			}
			before := len(s.ChatHistory())
			c.goneFor(e.why)
			for _, sub := range []*Subscription{guest, viewer} {
				_, err := s.Chat(sub, proto.ChatPost{Nonce: "n1", Text: "after"})
				if !errors.Is(err, e.why) {
					t.Fatalf("%s's post once the link went: %v", sub.Role, err)
				}
				m := decodeControl(t, ChatErrorFrame(err, "n1"))
				if m["code"] != e.code || m["message"] != e.words || m["requestId"] != "n1" {
					t.Fatalf("the refusal: %v", m)
				}
			}
			// A post to the agent from a view link is refused for its role first.
			if _, err := s.Chat(viewer, proto.ChatPost{Text: "to the agent", To: proto.ChatToAgent}); !errors.Is(err, ErrReadOnly) {
				t.Fatalf("a view link's post to the agent: %v", err)
			}
			err = s.ChatSend(context.Background(), guest, proto.ChatSend{T: proto.CtlChatSend, Ref: kept.ID})
			if !errors.Is(err, e.why) {
				t.Fatalf("the send once the link went: %v", err)
			}
			nothingWritten(t, p, 200*time.Millisecond)
			if n := len(s.ChatHistory()); n != before {
				t.Fatalf("the chat holds %d messages, %d before", n, before)
			}
			owner := ownerOf(t, s)
			if _, err := s.Chat(owner, proto.ChatPost{Text: "the owner's"}); err != nil {
				t.Fatal(err)
			}
			if err := s.ChatSend(context.Background(), owner, proto.ChatSend{T: proto.CtlChatSend, Ref: kept.ID}); err != nil {
				t.Fatal(err)
			}
			if got := written(t, p); !strings.Contains(got, "kept before") {
				t.Fatalf("written %q", got)
			}
		})
	}
}

// A run's chat send of a link's controller to another member's agent that
// passed its check as it was taken, and whose credential goes while its
// text waits behind another write there, is withdrawn, nothing typed, and
// says why (revoked or expired, with the words), not that the member could
// not take it.
func TestARunChatSendWithdrawnWhileItWaitsSaysWhy(t *testing.T) {
	for _, e := range linkEnds {
		t.Run(e.name, func(t *testing.T) {
			room := NewChatRoom(nil, nil)
			a, _ := newLocalWith(t, Options{ScrollbackBytes: 4096, RunChat: room})
			b, pb := newLocalWith(t, Options{ScrollbackBytes: 4096, RunChat: room, InputWait: 5 * time.Second, SubmitPause: 10 * time.Millisecond})
			room.Join(a, "core")
			room.Join(b, "tests")
			takeAgain := stopTaking(t, pb)
			go b.Input(ownerOf(t, b), []byte("other's"))
			untilWaiting(t, b, 0)
			var c credential
			guest, _ := linkViewer(t, a, "l1", RoleControl, &c)
			msg, err := a.Chat(guest, proto.ChatPost{Scope: proto.ChatScopeRun, Text: "run the tests"})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- a.ChatSend(context.Background(), guest, proto.ChatSend{T: proto.CtlChatSend, Ref: msg.ID, Scope: proto.ChatScopeRun, To: "tests"})
			}()
			untilWaiting(t, b, 1)
			c.goneFor(e.why)
			takeAgain()
			if got := written(t, pb); got != "other's" {
				t.Fatalf("written %q", got)
			}
			nothingWritten(t, pb, 300*time.Millisecond)
			err = returnsWithin(t, 5*time.Second, "the send", func() error { return <-done })
			if !errors.Is(err, e.why) {
				t.Fatalf("the send withdrawn as it waited: %v", err)
			}
			m := decodeControl(t, ChatErrorFrame(err, msg.ID))
			if m["code"] != e.code || m["message"] != e.words || m["requestId"] != msg.ID {
				t.Fatalf("the refusal: %v", m)
			}
		})
	}
}
