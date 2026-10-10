package api

// A link opens a session for as long as it is neither revoked nor expired,
// and is checked again as each viewer attaches, under the lock a revoke takes
// to close the link's viewers: a connection that presented its token before
// a revoke or an expiry, and attaches after, is refused with 4403.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// seen is what a viewer was sent until its connection closed.
type seen struct {
	welcomeRole string // "" when no welcome came
	errCode     string // the last error message's code
	closed      bool
	closeCode   websocket.StatusCode
	output      []byte // OUTPUT and SCROLLBACK payloads
}

// watch reads c until it closes or for d, whichever comes first. It returns
// early once the output holds until, or, with until nil, at the ready marker
// (a Read that times out closes a coder/websocket connection, so a test that
// goes on using c must not let it time out).
func watch(c *wsClient, d time.Duration, until []byte) seen {
	var o seen
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_, data, err := c.c.Read(ctx)
		cancel()
		if err != nil {
			if code := websocket.CloseStatus(err); code != -1 {
				o.closed, o.closeCode = true, code
			}
			return o
		}
		f, err := proto.Decode(data)
		if err != nil {
			continue
		}
		switch f.Type {
		case proto.TypeControl:
			var m map[string]any
			_ = json.Unmarshal(f.Payload, &m)
			switch m["t"] {
			case proto.CtlWelcome:
				o.welcomeRole, _ = m["role"].(string)
			case proto.CtlError:
				o.errCode, _ = m["code"].(string)
			case proto.CtlReady:
				if until == nil {
					return o
				}
			}
		case proto.TypeOutput, proto.TypeScrollback:
			o.output = append(o.output, f.Payload...)
			if until != nil && bytes.Contains(o.output, until) {
				return o
			}
		}
	}
	return o
}

// wantRefused says the connection was closed as one whose link no longer
// opens the session: 4403 with the error code, and nothing of the session.
func wantRefused(t *testing.T, what string, o seen, errCode string) {
	t.Helper()
	if !o.closed || o.closeCode != proto.CloseForbidden || o.errCode != errCode {
		t.Fatalf("%s: closed %v with %d, error %q; want 4403 and %q", what, o.closed, o.closeCode, o.errCode, errCode)
	}
	if o.welcomeRole != "" || len(o.output) > 0 {
		t.Fatalf("%s: welcomed as %q, sent %q", what, o.welcomeRole, o.output)
	}
}

// A connection that authenticated with a session's link before the link was
// revoked, and says hello after, is refused: no welcome, no scrollback, no
// input. The links list shows the link revoked with no viewer.
func TestALinkRevokedBeforeTheHelloAttachesNothing(t *testing.T) {
	for _, role := range []string{"view", "control"} {
		t.Run(role, func(t *testing.T) {
			e := newTestEnv(t, nil)
			id := e.createSession("cat")
			owner := dialViewer(t, e, id, adminToken)
			owner.hello(80, 24)
			owner.expectControl(proto.CtlReady)
			owner.send(proto.Encode(proto.TypeInput, []byte("owner-words-before-revoke\n")))
			owner.expectOutput("owner-words-before-revoke")

			_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": role, "label": "guest"})
			token := lo["token"].(string)
			linkID := lo["link"].(map[string]any)["id"].(string)
			// The upgrade completes: the server waits for the hello.
			guest := dialViewer(t, e, id, token)
			if resp, _ := e.do("DELETE", "/api/sessions/"+id+"/links/"+linkID, adminToken, nil); resp.StatusCode != http.StatusNoContent {
				t.Fatalf("revoke: %d", resp.StatusCode)
			}
			dialViewer(t, e, id, token).expectClose(proto.CloseUnauthorized)

			guest.hello(80, 24)
			wantRefused(t, "hello after the revoke", watch(guest, 3*time.Second, []byte("owner-words-before-revoke")), proto.ErrCodeRevoked)

			_, out := e.do("GET", "/api/sessions/"+id+"/links", adminToken, nil)
			for _, l := range out["links"].([]any) {
				if l := l.(map[string]any); l["id"] == linkID && (l["revoked"] != true || l["active"] != 0.0) {
					t.Fatalf("links list: %v", l)
				}
			}
		})
	}
}

// closeSink is a session.Sink that keeps the reason it was closed for.
type closeSink struct{ closed chan error }

func (s *closeSink) WriteFrame([]byte) error { return nil }
func (s *closeSink) Close(reason error)      { s.closed <- reason }

// Every revoke closes the viewers attached through the link, a link revoked
// before included.
func TestEveryRevokeClosesTheLinksViewers(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "control"})
	linkID := lo["link"].(map[string]any)["id"].(string)
	if resp, _ := e.do("DELETE", "/api/sessions/"+id+"/links/"+linkID, adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	// A client the first revoke did not close (attached as the session
	// allows its own, with no check), as one that stayed would be.
	d, _ := e.srv.registry.Get(id)
	sink := &closeSink{closed: make(chan error, 1)}
	sub, err := d.(*session.Local).AttachWith(session.AttachOptions{Role: session.RoleControl, LinkID: linkID}, sink)
	if err != nil {
		t.Fatal(err)
	}
	defer d.(*session.Local).Detach(sub)
	if resp, _ := e.do("DELETE", "/api/sessions/"+id+"/links/"+linkID, adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("second revoke: %d", resp.StatusCode)
	}
	select {
	case r := <-sink.closed:
		if !errors.Is(r, session.ErrRevoked) {
			t.Fatalf("closed for %v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the second revoke closed nobody")
	}
}

// The same for a crew run's link on a member's session.
func TestARunLinkRevokedBeforeTheHelloAttachesNothing(t *testing.T) {
	e := newTestEnv(t, nil)
	e.stopEverything(t)
	runID := e.launchCrew(t, "Squad", catMember("core", "immediately"))
	coreID := e.waitRunning(t, runID, "core")
	_, out := e.do("POST", "/api/runs/"+runID+"/links", adminToken, map[string]any{"role": "control", "label": "standup"})
	token := out["token"].(string)
	linkID := out["link"].(map[string]any)["id"].(string)

	guest := dialViewer(t, e, coreID, token)
	if resp, _ := e.do("DELETE", "/api/runs/"+runID+"/links/"+linkID, adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	dialViewer(t, e, coreID, token).expectClose(proto.CloseUnauthorized)
	guest.hello(80, 24)
	wantRefused(t, "run link, hello after the revoke", watch(guest, 3*time.Second, nil), proto.ErrCodeRevoked)
}

// A run the engine forgets takes its links: a connection that authenticated
// with one before, and says hello after, is refused too.
func TestAForgottenRunsLinkAttachesNothing(t *testing.T) {
	e := newTestEnv(t, nil)
	e.stopEverything(t)
	runID := e.launchCrew(t, "Squad", catMember("core", "immediately"))
	coreID := e.waitRunning(t, runID, "core")
	_, out := e.do("POST", "/api/runs/"+runID+"/links", adminToken, map[string]any{"role": "view"})
	token := out["token"].(string)

	guest := dialViewer(t, e, coreID, token)
	e.srv.runs.ForgetForTest(runID)
	guest.hello(80, 24)
	wantRefused(t, "hello after the run was forgotten", watch(guest, 3*time.Second, nil), proto.ErrCodeRevoked)
}

// A hosted session registers a viewer as it connects; a revoke racing that
// registration leaves no viewer attached once it has returned: each
// connection is refused at authentication, refused as it registers, or
// closed by the revoke. Many rounds, the revoke spread across the steps.
func TestHostedViewersAndARevokeRace(t *testing.T) {
	e := newTestEnv(t, nil)
	// A dial refused as unauthorized spends the per-address limiter; lift it.
	e.srv.limiter = newRateLimiter(1e6, 1e6)
	host := dialFakeHost(t, e, "")
	go func() {
		for {
			select {
			case <-host.msgs:
			case <-host.done:
				return
			}
		}
	}()
	const rounds = 400
	var atAuth, closed int
	for i := range rounds {
		link, token, err := e.srv.links.Create(host.sessionID, session.RoleControl, "race", 0)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var c *websocket.Conn
		var dialErr error
		wg.Go(func() {
			url := strings.Replace(e.http.URL, "http://", "ws://", 1) + "/ws/sessions/" + host.sessionID + "?token=" + token
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, _, dialErr = websocket.Dial(ctx, url, nil)
		})
		spin := time.Duration(i%40) * 25 * time.Microsecond
		for start := time.Now(); time.Since(start) < spin; {
		}
		e.srv.links.Revoke(host.sessionID, link.ID)
		wg.Wait()
		if dialErr != nil {
			t.Fatalf("dial: %v", dialErr)
		}
		c.SetReadLimit(proto.MaxFrame)
		o := watch(&wsClient{t: t, c: c}, 3*time.Second, nil)
		switch {
		case o.closed && o.closeCode == proto.CloseUnauthorized:
			atAuth++
		case o.closed && o.closeCode == proto.CloseForbidden:
			closed++
		default:
			t.Fatalf("round %d: a hosted viewer stayed attached through a revoked link (%+v)", i, o)
		}
		c.CloseNow()
		// A revoked link still counts against MaxLinksPerSession.
		e.srv.links.Drop(link.ID)
	}
	t.Logf("%d rounds: refused at authentication %d, refused or closed with 4403 %d", rounds, atAuth, closed)
}

// expiresIn makes a link to session id that expires in about a second and
// returns its token and when it expires.
func expiresIn(t *testing.T, e *testEnv, id, role string) (string, time.Time) {
	t.Helper()
	resp, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": role, "ttlSeconds": 1})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("link: %d %v", resp.StatusCode, lo)
	}
	at, err := time.Parse(time.RFC3339Nano, lo["link"].(map[string]any)["expiresAt"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return lo["token"].(string), at
}

// A link that expires closes its viewers as it expires, 4403 with the code
// expired, with no sweep needed; a connection that authenticated before the
// expiry and says hello after is refused alike; the token opens nothing after.
func TestALinkThatExpiresClosesItsViewers(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	token, at := expiresIn(t, e, id, "control")
	v := dialViewer(t, e, id, token)
	v.hello(80, 24)
	if o := watch(v, 900*time.Millisecond, nil); o.welcomeRole != "control" || o.closed {
		t.Fatalf("before the expiry: %+v", o)
	}
	o := watch(v, 5*time.Second, []byte("never"))
	if !o.closed || o.closeCode != proto.CloseForbidden || o.errCode != proto.ErrCodeExpired {
		t.Fatalf("at the expiry: %+v", o)
	}
	if time.Now().Before(at) {
		t.Fatal("closed before the link expired")
	}
	dialViewer(t, e, id, token).expectClose(proto.CloseUnauthorized)

	late, at := expiresIn(t, e, id, "view")
	pending := dialViewer(t, e, id, late)
	time.Sleep(time.Until(at) + 50*time.Millisecond)
	pending.hello(80, 24)
	wantRefused(t, "hello after the expiry", watch(pending, 3*time.Second, nil), proto.ErrCodeExpired)
}

// The same on a hosted session at a switchyard: the host's link that
// expires closes the viewer attached through it.
func TestAHostedLinkThatExpiresClosesItsViewers(t *testing.T) {
	e := switchyardEnv(t, true)
	host := dialFakeHost(t, e, "")
	host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "r1", Role: "view", TTLSeconds: 1})
	m := host.expect(proto.HostLinkCreated)
	u := m["url"].(string)
	token := u[strings.LastIndex(u, "/")+1:]
	v := dialViewer(t, e, host.sessionID, token)
	o := watch(v, 5*time.Second, []byte("never"))
	if o.welcomeRole != "view" || !o.closed || o.closeCode != proto.CloseForbidden || o.errCode != proto.ErrCodeExpired {
		t.Fatalf("hosted viewer of an expiring link: %+v", o)
	}
	if _, ok := e.srv.registry.Get(host.sessionID); !ok {
		t.Fatal("the session went with the link")
	}
}

// The maintenance sweep takes an expired link as a revoke does: whoever is
// still attached through it is closed.
func TestTheSweepClosesAnExpiredLinksViewers(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	_, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view", "ttlSeconds": 3600})
	v := dialViewer(t, e, id, lo["token"].(string))
	v.hello(80, 24)
	v.expectControl(proto.CtlReady)
	// The close waits for the viewer's answer, which this goroutine reads.
	expired := make(chan int, 1)
	go func() { expired <- e.srv.links.ExpireDue(time.Now().Add(2 * time.Hour)) }()
	v.expectClose(proto.CloseForbidden)
	if n := <-expired; n != 1 {
		t.Fatalf("expired %d", n)
	}
}

// A kept link the switchyard's sweep drops (its host gone for good) closes
// whoever is still attached through it.
func TestAKeptLinkSweptAwayClosesItsViewers(t *testing.T) {
	e := keptSwitchyard(t, t.TempDir(), nil)
	hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, e, hi, hs, "")
	_, token := mintHostLink(t, host, "r1")
	v := dialViewer(t, e, host.sessionID, token)
	v.expectControl(proto.CtlWelcome)
	e.srv.sweepLinks(time.Now().Add(linkOrphanAfter + time.Hour))
	v.expectClose(proto.CloseForbidden)
}

// The sweep closes a kept run link's viewers on every session the link
// names as it drops it, one a run update added after the link was made
// included.
func TestAKeptRunLinkSweptAwayClosesItsViewersOnEveryMember(t *testing.T) {
	e := keptSwitchyard(t, t.TempDir(), nil)
	hi, hsA := instanceHost(e, "instance-secret-0123456789", "a")
	_, hsB := instanceHost(e, "instance-secret-0123456789", "b")
	a, _ := dialFakeHostWith(t, e, hi, hsA, "")
	b, _ := dialFakeHostWith(t, e, hi, hsB, "")
	group := func(ids ...string) proto.RunGroup {
		g := proto.RunGroup{ID: "pair-0123abcd", Name: "pair"}
		for i, id := range ids {
			g.Members = append(g.Members, proto.RunMember{Name: fmt.Sprintf("m%d", i), SessionID: id, AgentID: "cat", Status: "running"})
		}
		return g
	}
	a.send(proto.HostRunLinkMsg{T: proto.HostRunLink, RequestID: "r1", Role: "view", Run: group(a.sessionID)})
	u := a.expect(proto.HostLinkCreated)["url"].(string)
	token := u[strings.LastIndex(u, "/")+1:]
	a.send(proto.HostRunLinkUpdateMsg{T: proto.HostRunLinkUpdate, RequestID: "u1", Run: group(a.sessionID, b.sessionID)})
	a.expect(proto.HostRunLinkUpdated)
	onA, onB := dialViewer(t, e, a.sessionID, token), dialViewer(t, e, b.sessionID, token)
	onA.expectControl(proto.CtlWelcome)
	onB.expectControl(proto.CtlWelcome)
	e.srv.sweepLinks(time.Now().Add(linkOrphanAfter + time.Hour))
	onA.expectClose(proto.CloseForbidden)
	onB.expectClose(proto.CloseForbidden)
}

// A session a host's run no longer names is no longer opened by the run's
// link: the viewer attached to it through the link is closed, the one on a
// session still named stays, and a new connection to the one that left is
// refused. A revoke then closes the rest.
func TestASessionLeavingAHostsRunClosesItsViewers(t *testing.T) {
	e := switchyardEnv(t, true)
	hostInfo := proto.HostInfo{Name: "laptop", Instance: "instance-secret-0123456789"}
	sess := func(local string) proto.HostSession {
		return proto.HostSession{Name: local, AgentID: "cat", Command: []string{"cat"}, Cwd: e.root, Cols: 80, Rows: 24, LocalID: local}
	}
	a, _ := dialFakeHostWith(t, e, hostInfo, sess("a"), "Bearer test-host-token")
	b, _ := dialFakeHostWith(t, e, hostInfo, sess("b"), "Bearer test-host-token")
	group := func(ids ...string) proto.RunGroup {
		g := proto.RunGroup{ID: "pair-0123abcd", Name: "pair"}
		for i, id := range ids {
			g.Members = append(g.Members, proto.RunMember{Name: fmt.Sprintf("m%d", i), SessionID: id, AgentID: "cat", Status: "running"})
		}
		return g
	}
	a.send(proto.HostRunLinkMsg{T: proto.HostRunLink, RequestID: "r1", Role: "control", Run: group(a.sessionID, b.sessionID)})
	created := a.expect(proto.HostLinkCreated)
	u := created["url"].(string)
	token := u[strings.LastIndex(u, "/")+1:]
	linkID := created["linkId"].(string)
	onA, onB := dialViewer(t, e, a.sessionID, token), dialViewer(t, e, b.sessionID, token)
	onA.expectControl(proto.CtlWelcome)
	onB.expectControl(proto.CtlWelcome)

	a.send(proto.HostRunLinkUpdateMsg{T: proto.HostRunLinkUpdate, RequestID: "u1", Run: group(a.sessionID, "")})
	a.expect(proto.HostRunLinkUpdated)
	onB.expectClose(proto.CloseForbidden)
	dialViewer(t, e, b.sessionID, token).expectClose(proto.CloseUnauthorized)
	da, _ := e.srv.registry.Get(a.sessionID)
	if n := da.LinkViewers()[linkID]; n != 1 {
		t.Fatalf("viewers through the link on the session still named: %d", n)
	}

	a.send(proto.HostLinkRevokeMsg{T: proto.HostLinkRevoke, RequestID: "rv", LinkID: linkID})
	a.expect(proto.HostLinkRevoked)
	onA.expectClose(proto.CloseForbidden)
}
