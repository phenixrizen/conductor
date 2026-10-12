package hostagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// leaveOf is the server's viewer_leave of the viewer id with reason ("" for
// none, as an older server's).
func leaveOf(id, reason string) []byte {
	return mustJSON(proto.ViewerLeave{T: proto.HostViewerLeave, ViewerID: id, Reason: reason})
}

// holdShows waits until the host's session shows the hold of its turn.
func holdShows(t *testing.T, local *session.Local) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		att := local.Info().Attention
		if att.State == session.AttentionNeedsInput && att.Source == session.SourceSubmit && att.Message == session.HeldWords && local.Held() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("attention %+v (held %v), want the hold's", att, local.Held())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A viewer the server lets go for its link (viewer_leave with a reason,
// revoked or expired) once its submission's text is typed has no Enter:
// the session's turn is held, Conductor's next prompt waits, and a
// person's keys end the hold, the prompt following them. What the viewer
// had waiting is dropped.
func TestAViewerLetGoForItsLinkOnceItsTextIsTypedHoldsTheTurn(t *testing.T) {
	for _, reason := range []string{proto.LeaveRevoked, proto.LeaveExpired} {
		t.Run(reason, func(t *testing.T) {
			v := newSubmitViewer(t, 400*time.Millisecond)
			a := v.p.a
			v.send(proto.Submit{T: proto.CtlSubmit, Text: "first"})
			v.send(proto.Submit{T: proto.CtlSubmit, Text: "waits"})
			v.sync()
			if got := v.release(); got != "first" {
				t.Fatalf("typed %q", got)
			}
			if err := a.handleControl(t.Context(), leaveOf(v.p.id, reason)); err != nil {
				t.Fatal(err)
			}
			v.quiet(700 * time.Millisecond)
			holdShows(t, a.local)

			next := make(chan error, 1)
			go func() {
				_, err := a.local.Submit(context.Background(), session.Submission{Text: "second", ByName: "run"})
				next <- err
			}()
			v.quiet(300 * time.Millisecond)
			owner, err := a.local.AttachWith(session.AttachOptions{Role: session.RoleControl, Name: "owner", Owner: true}, nopSink{})
			if err != nil {
				t.Fatal(err)
			}
			if err := a.local.Input(owner, []byte{0x15}); err != nil {
				t.Fatal(err)
			}
			if typed := v.typed(v.release(), 3); strings.Join(typed, "|") != "\x15|second|\r" {
				t.Fatalf("typed %q", typed)
			}
			select {
			case err := <-next:
				if err != nil {
					t.Fatalf("the next prompt: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the next prompt never returned")
			}
			if a.local.Held() {
				t.Fatal("still held")
			}
			v.quiet(300 * time.Millisecond)
		})
	}
}

// A viewer_leave with no reason (an older server's, or a viewer that only
// left), or with one this host does not know, lets the viewer go as before:
// its line being typed is finished, its Enter included, and nothing holds
// the turn.
func TestAViewerLeaveWithoutAKnownReasonFinishesTheLine(t *testing.T) {
	for _, reason := range []string{"", "left", strings.Repeat("r", proto.MaxLeaveReason+1)} {
		t.Run(reason, func(t *testing.T) {
			v := newSubmitViewer(t, 400*time.Millisecond)
			a := v.p.a
			v.send(proto.Submit{T: proto.CtlSubmit, Text: "first"})
			v.sync()
			if got := v.release(); got != "first" {
				t.Fatalf("typed %q", got)
			}
			if err := a.handleControl(t.Context(), leaveOf(v.p.id, reason)); err != nil {
				t.Fatal(err)
			}
			if got := v.release(); got != "\r" {
				t.Fatalf("typed %q, want the line's Enter", got)
			}
			if a.local.Held() {
				t.Fatal("the turn is held")
			}
		})
	}
}

// holdTurn has Conductor's own line take the session's turn and keep it
// until the returned release: its text's write waits in the process (hold).
func holdTurn(t *testing.T, v *submitViewer) (release func()) {
	t.Helper()
	v.hold()
	go v.p.a.local.Submit(context.Background(), session.Submission{Text: "conductor's", ByName: "run"})
	v.untilBegun(1)
	return func() {
		t.Helper()
		if typed := v.typed(v.release(), 2); strings.Join(typed, "|") != "conductor's|\r" {
			t.Fatalf("typed %q", typed)
		}
	}
}

// A viewer that left once it had sent (a reply box), its submission still
// waiting for the session's turn, has it withdrawn, never typed, when the
// server says the link it joined through went (link_gone), or once its
// link's expiry passes; with neither, it is typed in its turn, as a reply
// box's is.
func TestASubmissionOfAViewerThatLeftIsWithdrawnWhenItsLinkGoes(t *testing.T) {
	for _, c := range []struct {
		name   string
		until  time.Duration
		gone   bool
		typed  bool
		reason string
	}{
		{name: "revoked", gone: true},
		{name: "expired", until: 300 * time.Millisecond},
		{name: "standing", typed: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var until time.Time
			if c.until > 0 {
				until = time.Now().Add(c.until)
			}
			v := newLinkedSubmitViewer(t, 10*time.Millisecond, "l1", until)
			a := v.p.a
			release := holdTurn(t, v)
			v.send(proto.Submit{T: proto.CtlSubmit, Text: "reply"})
			v.sync()
			if err := a.handleControl(t.Context(), leaveOf(v.p.id, "")); err != nil {
				t.Fatal(err)
			}
			if c.gone {
				if err := a.handleControl(t.Context(), mustJSON(proto.LinkGone{T: proto.HostLinkGone, LinkID: "l1"})); err != nil {
					t.Fatal(err)
				}
			}
			time.Sleep(c.until + 100*time.Millisecond)
			release()
			if c.typed {
				if typed := v.typed(v.release(), 2); strings.Join(typed, "|") != "reply|\r" {
					t.Fatalf("typed %q", typed)
				}
			}
			v.quiet(500 * time.Millisecond)
			if a.local.Held() {
				t.Fatal("the turn is held")
			}
		})
	}
}

// A viewer that left once it had sent, its submission's text typed and its
// Enter to come, has no Enter when the server then says its link went: the
// session's turn is held for a person, as for a viewer let go for its link.
func TestATypedLineOfAViewerThatLeftHoldsTheTurnWhenItsLinkGoes(t *testing.T) {
	v := newLinkedSubmitViewer(t, 600*time.Millisecond, "l1", time.Time{})
	a := v.p.a
	v.send(proto.Submit{T: proto.CtlSubmit, Text: "reply"})
	v.sync()
	if got := v.release(); got != "reply" {
		t.Fatalf("typed %q", got)
	}
	if err := a.handleControl(t.Context(), leaveOf(v.p.id, "")); err != nil {
		t.Fatal(err)
	}
	if err := a.handleControl(t.Context(), mustJSON(proto.LinkGone{T: proto.HostLinkGone, LinkID: "l1"})); err != nil {
		t.Fatal(err)
	}
	v.quiet(900 * time.Millisecond)
	holdShows(t, a.local)
}

// A link_gone without a link, or with one past MaxLinkID bytes, is refused
// and changes nothing.
func TestALinkGoneOutOfBoundsIsRefused(t *testing.T) {
	a := &agent{peers: map[string]*peer{}, log: discardLog}
	long := strings.Repeat("l", proto.MaxLinkID+1)
	c := a.creds.of(long)
	for _, id := range []string{"", long} {
		if err := a.handleControl(t.Context(), mustJSON(proto.LinkGone{T: proto.HostLinkGone, LinkID: id})); err == nil {
			t.Errorf("link_gone %q was taken", id)
		}
	}
	if c.err() != nil {
		t.Fatal("a refused link_gone marked its link gone")
	}
}

// What the host keeps of the links is held by their viewers and what they
// asked for: a link gone stays gone for whoever holds it, however many
// other links go after it and whatever the collector sweeps; one nobody
// holds is not kept, and a link's viewer after the connection to the server
// was lost starts afresh while those before it count the link as gone.
func TestALinksGoingReachesWhoeverStillHoldsIt(t *testing.T) {
	var l linkCreds
	held := l.of("l0")
	if l.of("l0") != held {
		t.Fatal("two viewers of a link hold two creds")
	}
	l.gone("l0")
	for i := range 4096 {
		id := fmt.Sprintf("other-%d", i)
		_ = l.of(id)
		l.gone(id)
	}
	runtime.GC()
	if !errors.Is(held.err(), session.ErrRevoked) {
		t.Fatalf("the link's going was lost: %v", held.err())
	}
	// Swept, as the next viewer past the bound would have it.
	l.mu.Lock()
	l.sweep()
	kept := len(l.m)
	l.mu.Unlock()
	if kept != 1 {
		t.Fatalf("%d links kept, one held", kept)
	}

	standing := l.of("l1")
	l.lost()
	if !errors.Is(standing.err(), session.ErrRevoked) {
		t.Fatal("the connection's loss left a link standing")
	}
	if fresh := l.of("l1"); fresh == standing || fresh.err() != nil {
		t.Fatal("a viewer of the link once the connection is back holds the old cred")
	}
	runtime.KeepAlive(held)
	runtime.KeepAlive(standing)
}

// A viewer that left once it had sent, its submission waiting for the
// session's turn, has it withdrawn when the host's connection to the server
// is lost (connLost): the host is not told of the link going until it is
// back.
func TestASubmissionOfAViewerThatLeftIsWithdrawnWhenTheConnectionIsLost(t *testing.T) {
	v := newLinkedSubmitViewer(t, 10*time.Millisecond, "l1", time.Time{})
	a := v.p.a
	release := holdTurn(t, v)
	v.send(proto.Submit{T: proto.CtlSubmit, Text: "reply"})
	v.sync()
	if err := a.handleControl(t.Context(), leaveOf(v.p.id, "")); err != nil {
		t.Fatal(err)
	}
	a.connLost(nil)
	release()
	v.quiet(500 * time.Millisecond)
}

// A viewer that left once its text's write had begun, the program taking
// nothing, has no Enter when its link goes, however many links go after it
// before the program reads again: the link's going is kept by what is under
// way of its viewer.
func TestALinkGoneStaysGoneForAStalledLine(t *testing.T) {
	v := newLinkedSubmitViewer(t, 10*time.Millisecond, "l1", time.Time{})
	a := v.p.a
	v.hold()
	v.send(proto.Submit{T: proto.CtlSubmit, Text: "reply"})
	v.sync()
	v.untilBegun(1) // its text's write waits in the process
	if err := a.handleControl(t.Context(), leaveOf(v.p.id, "")); err != nil {
		t.Fatal(err)
	}
	if err := a.handleControl(t.Context(), mustJSON(proto.LinkGone{T: proto.HostLinkGone, LinkID: "l1"})); err != nil {
		t.Fatal(err)
	}
	for i := range 2048 {
		id := fmt.Sprintf("other-%d", i)
		_ = a.creds.of(id)
		if err := a.handleControl(t.Context(), mustJSON(proto.LinkGone{T: proto.HostLinkGone, LinkID: id})); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	if got := v.release(); got != "reply" {
		t.Fatalf("typed %q", got)
	}
	v.quiet(500 * time.Millisecond)
	holdShows(t, a.local)
}

// A link's controller's submission waits for its turn behind a write the
// program does not take (another controller's keys filling the terminal's
// buffer), and its link is revoked meanwhile, the controller still attached
// or gone once it had sent (a reply box): the server tells the host the
// link went, and the submission is withdrawn, never typed once the program
// reads again; the session goes on with the next line.
func TestARevokedLinksSubmissionWaitingOnAHostIsNeverTyped(t *testing.T) {
	for _, left := range []bool{false, true} {
		t.Run(map[bool]string{false: "attached", true: "left"}[left], func(t *testing.T) {
			revokedLinksSubmissionNeverTyped(t, left)
		})
	}
}

func revokedLinksSubmissionNeverTyped(t *testing.T, left bool) {
	dir := t.TempDir()
	start := filepath.Join(dir, "read")
	srv, base, id := relayHostOn(t, []string{"/bin/sh", "-c", "stty raw -echo; echo ready; while [ ! -e '" + start + "' ]; do sleep 0.05; done; exec cat"})
	owner := relayControllerSeeing(t, base, id, "ready")
	link, tok, err := srv.Links().Create(id, session.RoleControl, "guest", 0)
	if err != nil {
		t.Fatal(err)
	}
	guest := relayControllerOf(t, base, id, tok)

	// The owner's keys fill the terminal's buffer: the program reads
	// nothing, and the session's writer waits on them.
	big := bytes.Repeat([]byte("x"), proto.MaxInput)
	for range 4 {
		owner.send(proto.Encode(proto.TypeInput, big))
	}
	time.Sleep(300 * time.Millisecond)
	guest.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "queued line"}))
	time.Sleep(300 * time.Millisecond)
	if left {
		// A reply box closes its connection once it has sent.
		_ = guest.c.Close(websocket.StatusNormalClosure, "")
		time.Sleep(300 * time.Millisecond)
	}
	srv.Links().Revoke(id, link.ID)
	for !left {
		if _, err := guest.read(); err != nil {
			if websocket.CloseStatus(err) != proto.CloseForbidden {
				t.Fatalf("the revoked viewer's close: %v", err)
			}
			break
		}
	}
	// The host hears of it before the program reads again.
	time.Sleep(300 * time.Millisecond)
	if err := os.WriteFile(start, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	owner.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "owner line"}))
	var acc []byte
	for !bytes.Contains(acc, []byte("owner line")) {
		f, err := owner.read()
		if err != nil {
			t.Fatalf("waiting for the owner's line: %v (have %d bytes)", err, len(acc))
		}
		if f.Type == proto.TypeOutput {
			acc = append(acc, f.Payload...)
		}
	}
	// What comes in the next second: nothing of the guest's.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		_, data, err := owner.c.Read(ctx)
		if err != nil {
			break
		}
		if f, err := proto.Decode(data); err == nil && f.Type == proto.TypeOutput {
			acc = append(acc, f.Payload...)
		}
	}
	if bytes.Contains(acc, []byte("queued line")) {
		t.Fatal("the revoked link's submission was typed")
	}
}
