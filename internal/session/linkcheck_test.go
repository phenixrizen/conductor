package session

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// linkFlag stands in for a link store: a revoke marks the link, then closes
// its clients, as share.Store.revoke and its hook do.
type linkFlag struct{ revoked atomic.Bool }

func (f *linkFlag) check(role Role) func() (Grant, error) {
	return func() (Grant, error) {
		if f.revoked.Load() {
			return Grant{}, ErrRevoked
		}
		return Grant{Role: role}, nil
	}
}

func (f *linkFlag) revoke(s *Local, linkID string) {
	f.revoked.Store(true)
	s.DisconnectLink(linkID)
}

// A client whose link was revoked before it attached is refused: the
// revoke closed only the clients already there, and the attach checks the
// link again under the session's lock. Nothing of the session reaches it.
func TestAttachChecksTheLinkAgain(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	p.outW.Write([]byte("before\n"))
	time.Sleep(50 * time.Millisecond)
	var link linkFlag
	link.revoke(s, "link-1")

	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, LinkID: "link-1", Cols: 80, Rows: 24, Authorize: link.check(RoleControl)}, sink)
	if !errors.Is(err, ErrRevoked) || sub != nil {
		t.Fatalf("attach after the revoke: %v %v", sub, err)
	}
	if n := sink.count(); n != 0 {
		t.Fatalf("a refused client was sent %d frames", n)
	}
	if n := s.LinkViewers()["link-1"]; n != 0 {
		t.Fatalf("the link counts %d viewers", n)
	}
}

// The role the check returns is the one the client attaches with, and a
// check that returns none refuses it.
func TestAttachTakesTheRoleTheCheckGives(t *testing.T) {
	s, _ := newLocal(t, t.TempDir())
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleControl, LinkID: "l", Authorize: func() (Grant, error) { return Grant{Role: RoleView}, nil }}, sink)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Detach(sub)
	if sub.Role != RoleView {
		t.Fatalf("role %q", sub.Role)
	}
	sink.waitFrames(t, 1)
	if m := decodeControl(t, sink.frame(0)); m["role"] != "view" {
		t.Fatalf("welcome %v", m)
	}
	if err := s.Input(sub, []byte("x")); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("input from the view role: %v", err)
	}
	if _, err := s.AttachWith(AttachOptions{Role: RoleControl, Authorize: func() (Grant, error) { return Grant{}, nil }}, newChanSink(false)); err == nil {
		t.Fatal("a check with no role attached the client")
	}
}

// A revoke and an attach that race end one way or the other, never with
// the client attached through the revoked link: either the attach is
// refused or the revoke closes it (go test -race; many rounds).
func TestRevokeAndAttachRace(t *testing.T) {
	dir := t.TempDir()
	var s *Local
	const rounds = 500
	var refused, closed int
	for i := range rounds {
		// A fresh session now and then: each loud client leaves join and
		// leave lines in the chat, which every attach replays.
		if i%25 == 0 {
			s, _ = newLocal(t, dir)
		}
		var link linkFlag
		linkID := "race-" + NewID()
		sink := newChanSink(false)
		var sub *Subscription
		var err error
		var wg sync.WaitGroup
		start := make(chan struct{})
		// One side or the other yields a little first, by turns, so the
		// revoke lands before, during and after the attach's steps. Some
		// clients are quiet (a run's chat alone), the attach's other path.
		wait := func(mine bool) {
			if mine {
				for range i % 64 {
					runtime.Gosched()
				}
			}
		}
		wg.Go(func() {
			<-start
			wait(i%2 == 1)
			sub, err = s.AttachWith(AttachOptions{Role: RoleControl, LinkID: linkID, ChatOnly: i%4 != 0, Authorize: link.check(RoleControl)}, sink)
		})
		wg.Go(func() {
			<-start
			wait(i%2 == 0)
			link.revoke(s, linkID)
		})
		close(start)
		wg.Wait()
		if err != nil {
			if !errors.Is(err, ErrRevoked) {
				t.Fatalf("round %d: %v", i, err)
			}
			refused++
			continue
		}
		select {
		case r := <-sink.closed:
			if !errors.Is(r, ErrRevoked) {
				t.Fatalf("round %d: closed for %v", i, r)
			}
			closed++
		case <-time.After(2 * time.Second):
			t.Fatalf("round %d: the client attached through the revoked link stays open", i)
		}
		s.Detach(sub)
	}
	t.Logf("%d rounds: refused at attach %d, closed by the revoke %d", rounds, refused, closed)
}

// A session that leaves the registry closes every client as at its end and
// takes none after.
func TestRetireClosesEveryClientAndRefusesMore(t *testing.T) {
	s, p := newLocal(t, t.TempDir())
	a, b := newChanSink(false), newChanSink(false)
	if _, err := s.Attach("", RoleControl, "", 80, 24, a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, LinkID: "run-link", Authorize: func() (Grant, error) { return Grant{Role: RoleView}, nil }}, b); err != nil {
		t.Fatal(err)
	}
	p.exit()
	s.Retire()
	for _, sink := range []*chanSink{a, b} {
		select {
		case r := <-sink.closed:
			if !errors.Is(r, ErrSessionEnded) {
				t.Fatalf("closed for %v", r)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("a client of the retired session stays")
		}
	}
	if _, err := s.Attach("", RoleControl, "", 80, 24, newChanSink(false)); !errors.Is(err, ErrSessionEnded) {
		t.Fatalf("attach after retiring: %v", err)
	}
}

// A retired session still tells its hooks what changes (its end among
// them, which a run records): retiring closes clients, it silences nothing.
func TestARetiredSessionStillTellsItsChanges(t *testing.T) {
	var mu sync.Mutex
	var statuses []Status
	s, p := newLocalWith(t, Options{ScrollbackBytes: 4096, OnChange: func(i Info) {
		mu.Lock()
		statuses = append(statuses, i.Status)
		mu.Unlock()
	}})
	sink := newChanSink(false)
	if _, err := s.Attach("", RoleControl, "", 80, 24, sink); err != nil {
		t.Fatal(err)
	}
	s.Retire()
	<-sink.closed
	p.exit()
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		ended := len(statuses) > 0 && statuses[len(statuses)-1].Ended()
		mu.Unlock()
		if ended {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the end was not told: %v", statuses)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A grant that lapses closes the client as it lapses, with ErrExpired, a
// kind of revoke; one that lapsed already closes it at once.
func TestGrantUntilClosesTheClient(t *testing.T) {
	if !errors.Is(ErrExpired, ErrRevoked) || errors.Is(ErrRevoked, ErrExpired) {
		t.Fatal("ErrExpired is to be a kind of ErrRevoked")
	}
	s, _ := newLocal(t, t.TempDir())
	until := time.Now().Add(150 * time.Millisecond)
	sink := newChanSink(false)
	sub, err := s.AttachWith(AttachOptions{Role: RoleView, LinkID: "l", Authorize: func() (Grant, error) { return Grant{Role: RoleView, Until: until}, nil }}, sink)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-sink.closed:
		if !errors.Is(r, ErrExpired) {
			t.Fatalf("closed for %v", r)
		}
		if time.Now().Before(until) {
			t.Fatal("closed before its link lapsed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the client stays past its link's expiry")
	}
	if !errors.Is(sub.Reason(), ErrExpired) {
		t.Fatalf("reason %v", sub.Reason())
	}

	late := newChanSink(false)
	if _, err := s.AttachWith(AttachOptions{Role: RoleView, LinkID: "l", Authorize: func() (Grant, error) { return Grant{Role: RoleView, Until: time.Now().Add(-time.Millisecond)}, nil }}, late); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-late.closed:
		if !errors.Is(r, ErrExpired) {
			t.Fatalf("closed for %v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a client whose link lapsed as it attached stays")
	}

	// A client without an end stays.
	stays := newChanSink(false)
	kept, err := s.AttachWith(AttachOptions{Role: RoleView, LinkID: "m", Authorize: func() (Grant, error) { return Grant{Role: RoleView}, nil }}, stays)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Detach(kept)
	select {
	case r := <-stays.closed:
		t.Fatalf("closed for %v", r)
	case <-time.After(200 * time.Millisecond):
	}
	if m := decodeControl(t, stays.frame(0)); m["t"] != proto.CtlWelcome {
		t.Fatalf("first frame %v", m)
	}
}
