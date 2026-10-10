package signal

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// drainHost reads what the session sends its host for as long as the test
// runs.
func drainHost(t *testing.T, conn *HostConn) {
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-conn.Send:
			case <-done:
				return
			}
		}
	}()
}

// A viewer whose link no longer opens the session is refused under the
// session's lock, before the host hears of it; the role the check gives is
// the one the viewer gets.
func TestAddViewerChecksTheLinkAgain(t *testing.T) {
	hub := NewHub(session.NewRegistry(4), nil)
	hs, conn := register(t, hub)
	v, err := hs.AddViewerWith(ViewerOptions{ID: "0123456789abcdef", Role: session.RoleControl, LinkID: "l1",
		Authorize: func() (session.Grant, error) { return session.Grant{}, session.ErrRevoked }})
	if !errors.Is(err, session.ErrRevoked) || v != nil {
		t.Fatalf("revoked link: %v %v", v, err)
	}
	if _, err := hs.AddViewerWith(ViewerOptions{ID: "0123456789abcdee", Role: session.RoleControl, LinkID: "l1",
		Authorize: func() (session.Grant, error) { return session.Grant{}, session.ErrExpired }}); !errors.Is(err, session.ErrExpired) {
		t.Fatalf("expired link: %v", err)
	}
	select {
	case o := <-conn.Send:
		t.Fatalf("the host heard of a refused viewer: %s", o.Text)
	default:
	}
	if n := hs.Info().Viewers; n != 0 {
		t.Fatalf("viewers %d", n)
	}
	v, err = hs.AddViewerWith(ViewerOptions{ID: "fedcba9876543210", Role: session.RoleControl, LinkID: "l2",
		Authorize: func() (session.Grant, error) { return session.Grant{Role: session.RoleView}, nil }})
	if err != nil || v.Role != session.RoleView {
		t.Fatalf("view grant: %+v %v", v, err)
	}
	drainText(t, conn, proto.HostViewerJoin)
}

// A revoke and a viewer's arrival that race end one way or the other, never
// with the viewer registered through the revoked link: either AddViewerWith
// refuses it or DisconnectLink closes it (go test -race; many rounds).
func TestHostedRevokeAndAddViewerRace(t *testing.T) {
	hub := NewHub(session.NewRegistry(4), nil)
	hs, conn := register(t, hub)
	drainHost(t, conn)
	const rounds = 500
	var refused, closed int
	for i := range rounds {
		var revoked atomic.Bool
		linkID := fmt.Sprintf("race-%d", i)
		check := func() (session.Grant, error) {
			if revoked.Load() {
				return session.Grant{}, session.ErrRevoked
			}
			return session.Grant{Role: session.RoleControl}, nil
		}
		wait := func(mine bool) {
			if mine {
				for range i % 64 {
					runtime.Gosched()
				}
			}
		}
		var v *Viewer
		var err error
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Go(func() {
			<-start
			wait(i%2 == 1)
			v, err = hs.AddViewerWith(ViewerOptions{ID: fmt.Sprintf("%016x", i), Role: session.RoleControl, LinkID: linkID, Authorize: check})
		})
		wg.Go(func() {
			<-start
			wait(i%2 == 0)
			// As a revoke does: the mark, then the close.
			revoked.Store(true)
			hs.DisconnectLink(linkID)
		})
		close(start)
		wg.Wait()
		if err != nil {
			if !errors.Is(err, session.ErrRevoked) {
				t.Fatalf("round %d: %v", i, err)
			}
			refused++
			continue
		}
		if !errors.Is(v.Reason(), session.ErrRevoked) {
			t.Fatalf("round %d: the viewer added through the revoked link stays open (reason %v)", i, v.Reason())
		}
		closed++
		hs.RemoveViewer(v)
	}
	t.Logf("%d rounds: refused %d, closed by the revoke %d", rounds, refused, closed)
}

// A hosted session that leaves the registry closes every viewer as at its
// end and takes none after.
func TestHostedRetireClosesEveryViewerAndRefusesMore(t *testing.T) {
	hub := NewHub(session.NewRegistry(4), nil)
	hs, conn := register(t, hub)
	drainHost(t, conn)
	v, err := hs.AddViewer("0123456789abcdef", session.RoleView, "l1", "")
	if err != nil {
		t.Fatal(err)
	}
	hs.Retire()
	if !errors.Is(v.Reason(), session.ErrSessionEnded) {
		t.Fatalf("reason %v", v.Reason())
	}
	if _, err := hs.AddViewer("fedcba9876543210", session.RoleView, "l1", ""); !errors.Is(err, session.ErrSessionEnded) {
		t.Fatalf("add after retiring: %v", err)
	}
	hs.RemoveViewer(v)
}

// A grant that lapses closes the viewer as it lapses, with ErrExpired, and
// the frames still queued for it are dropped as on a revoke.
func TestHostedViewerGrantUntil(t *testing.T) {
	hub := NewHub(session.NewRegistry(4), nil)
	hs, conn := register(t, hub)
	drainHost(t, conn)
	until := time.Now().Add(150 * time.Millisecond)
	v, err := hs.AddViewerWith(ViewerOptions{ID: "0123456789abcdef", Role: session.RoleView, LinkID: "l1",
		Authorize: func() (session.Grant, error) { return session.Grant{Role: session.RoleView, Until: until}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-v.Done():
		if time.Now().Before(until) {
			t.Fatal("closed before its link lapsed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the viewer stays past its link's expiry")
	}
	if !errors.Is(v.Reason(), session.ErrExpired) || framesOwed(v.Reason()) {
		t.Fatalf("reason %v (frames owed %v)", v.Reason(), framesOwed(v.Reason()))
	}
	hs.RemoveViewer(v)

	// One without an end stays; removing it stops nothing that would fire.
	kept, err := hs.AddViewerWith(ViewerOptions{ID: "fedcba9876543210", Role: session.RoleView, LinkID: "l2",
		Authorize: func() (session.Grant, error) { return session.Grant{Role: session.RoleView}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if kept.Reason() != nil {
		t.Fatalf("closed for %v", kept.Reason())
	}
	hs.RemoveViewer(kept)
}
