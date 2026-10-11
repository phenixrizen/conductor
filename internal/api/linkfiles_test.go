package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
)

// keptSwitchyard is a switchyard on the data directory dir, as a restart finds it.
func keptSwitchyard(t *testing.T, dir string, mutate func(*config.Config)) *testEnv {
	t.Helper()
	return newTestEnv(t, func(c *config.Config) {
		c.Switchyard.Enabled = true
		c.Switchyard.OpenHosts = true
		c.DataDir = dir
		if mutate != nil {
			mutate(c)
		}
	})
}

func instanceHost(e *testEnv, instance, local string) (proto.HostInfo, proto.HostSession) {
	return proto.HostInfo{Name: "laptop", Instance: instance}, proto.HostSession{Name: "hosted", AgentID: "cat", Command: []string{"cat"}, Cwd: e.root, Cols: 80, Rows: 24, LocalID: local}
}

func mintHostLink(t *testing.T, host *fakeHost, id string) (linkID, token string) {
	t.Helper()
	host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: id, Role: "view"})
	m := host.expect(proto.HostLinkCreated)
	u, _ := m["url"].(string)
	return m["linkId"].(string), u[strings.LastIndex(u, "/")+1:]
}

func joinStatus(t *testing.T, e *testEnv, token string) (int, map[string]any) {
	t.Helper()
	resp, out := e.do("GET", "/api/join/"+token, token, nil)
	return resp.StatusCode, out
}

// A switchyard keeps the links it minted for a host with an instance: after
// a restart a join says the host is away, and once the host registers again
// it gets its session's id back and the link works as before.
func TestLinksSurviveASwitchyardRestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	a := keptSwitchyard(t, dir, nil)
	hi, hs := instanceHost(a, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, a, hi, hs, "")
	linkID, token := mintHostLink(t, host, "r1")
	b, err := os.ReadFile(filepath.Join(dir, "links", linkID+".json"))
	if err != nil || strings.Contains(string(b), token) {
		t.Fatalf("the kept file: %v, holds the token: %v", err, strings.Contains(string(b), token))
	}
	host.c.Close(websocket.StatusNormalClosure, "")
	<-host.done

	b2 := keptSwitchyard(t, dir, nil)
	if code, out := joinStatus(t, b2, token); code != http.StatusServiceUnavailable || out["error"].(map[string]any)["code"] != "host_offline" {
		t.Fatalf("before the host is back: %d %v", code, out)
	}
	hi, hs = instanceHost(b2, "instance-secret-0123456789", "1")
	back, reg := dialFakeHostWith(t, b2, hi, hs, "")
	if back.sessionID != host.sessionID || fmt.Sprint(reg["links"]) != fmt.Sprint([]any{linkID}) {
		t.Fatalf("back: %s (was %s), links %v", back.sessionID, host.sessionID, reg["links"])
	}
	if code, out := joinStatus(t, b2, token); code != http.StatusOK {
		t.Fatalf("after the host is back: %d %v", code, out)
	}
	v := dialViewer(t, b2, back.sessionID, token)
	v.hello(80, 24)
	v.expectControl(proto.CtlWelcome)
}

// A kept link goes with a session that ended, and with a revoke, file and all;
// it outlives a host that only went away.
func TestAKeptLinkGoesWithItsSessionOrItsRevoke(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	e := keptSwitchyard(t, dir, nil)
	hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, e, hi, hs, "")
	revoked, _ := mintHostLink(t, host, "r1")
	kept, token := mintHostLink(t, host, "r2")
	host.send(proto.HostLinkRevokeMsg{T: proto.HostLinkRevoke, RequestID: "rv", LinkID: revoked})
	host.expect(proto.HostLinkRevoked)
	if _, err := os.Stat(filepath.Join(dir, "links", revoked+".json")); !os.IsNotExist(err) {
		t.Fatalf("a revoked link's file stays: %v", err)
	}
	// The host goes away and the hub forgets the session: the link stays.
	host.c.Close(websocket.StatusNormalClosure, "")
	<-host.done
	e.srv.hosts.Expire(time.Now().Add(time.Hour))
	if code, _ := joinStatus(t, e, token); code != http.StatusServiceUnavailable {
		t.Fatalf("a host away: %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "links", kept+".json")); err != nil {
		t.Fatalf("the kept file went with a host away: %v", err)
	}
	// Back, then the session ends and leaves the registry: the link goes.
	back, _ := dialFakeHostWith(t, e, hi, hs, "")
	back.send(proto.HostStatusMsg{T: proto.HostStatus, SessionID: back.sessionID, Status: "exited"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if d, ok := e.srv.registry.Get(back.sessionID); ok && d.Info().Status.Ended() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session never ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.srv.registry.Remove(back.sessionID)
	if _, err := os.Stat(filepath.Join(dir, "links", kept+".json")); !os.IsNotExist(err) {
		t.Fatalf("the file of an ended session's link stays: %v", err)
	}
}

// The open hosts of one address keep at most openHostLinks links; a tokened
// host is not counted. A plain server keeps no files.
func TestKeptLinksAreBoundedPerOpenHostAddress(t *testing.T) {
	e := keptSwitchyard(t, filepath.Join(t.TempDir(), "data"), func(c *config.Config) { c.Switchyard.OpenHostLinks = 2 })
	hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, e, hi, hs, "")
	mintHostLink(t, host, "r1")
	mintHostLink(t, host, "r2")
	host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "r3", Role: "view"})
	if m := host.expect(proto.HostError); m["code"] != "link_refused" {
		t.Fatalf("third link: %v", m)
	}
	tokened, _ := dialFakeHostWith(t, e, proto.HostInfo{Name: "box", Instance: "another-instance-0123456789"}, hs, "Bearer test-host-token")
	mintHostLink(t, tokened, "t1")

	plain := newTestEnv(t, nil)
	if _, err := os.Stat(filepath.Join(plain.srv.store.Dir(), "links")); !os.IsNotExist(err) {
		t.Fatalf("a plain server made a links directory: %v", err)
	}
}

// A kept link leaves its address's count once, however often it is revoked
// and whether its expiry is told before the sweep drops it: the bound holds.
func TestAKeptLinkLeavesItsAddressCountOnce(t *testing.T) {
	open := func(t *testing.T) (*testEnv, *fakeHost, func(id string)) {
		e := keptSwitchyard(t, filepath.Join(t.TempDir(), "data"), func(c *config.Config) { c.Switchyard.OpenHostLinks = 2 })
		hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
		host, _ := dialFakeHostWith(t, e, hi, hs, "")
		refused := func(id string) {
			t.Helper()
			host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: id, Role: "view"})
			if m := host.expect(proto.HostError); m["code"] != "link_refused" {
				t.Fatalf("%s past the bound: %v", id, m)
			}
		}
		return e, host, refused
	}
	t.Run("revoked again", func(t *testing.T) {
		_, host, refused := open(t)
		a, _ := mintHostLink(t, host, "r1")
		mintHostLink(t, host, "r2")
		for i := range 3 {
			host.send(proto.HostLinkRevokeMsg{T: proto.HostLinkRevoke, RequestID: fmt.Sprintf("rv%d", i), LinkID: a})
			host.expect(proto.HostLinkRevoked)
		}
		mintHostLink(t, host, "r3")
		refused("r4")
	})
	t.Run("expired, then swept", func(t *testing.T) {
		e, host, refused := open(t)
		host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "e1", Role: "view", TTLSeconds: 60})
		host.expect(proto.HostLinkCreated)
		mintHostLink(t, host, "e2")
		later := time.Now().Add(2 * time.Minute)
		if n := e.srv.links.ExpireDue(later); n != 1 {
			t.Fatalf("expired %d", n)
		}
		e.srv.sweepLinks(later)
		mintHostLink(t, host, "e3")
		refused("e4")
	})
}

// An unreadable file is left out at startup, never fatal; an expired one is deleted.
func TestABadOrExpiredKeptLinkIsLeftOutAtStartup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(dir, "links"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "links", "0123456789abcdef.json"), []byte("{not json"), 0o600)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	_ = os.WriteFile(filepath.Join(dir, "links", "fedcba9876543210.json"), []byte(`{"id":"fedcba9876543210","tokenHash":"`+strings.Repeat("ab", 32)+`","sessionId":"s","role":"view","createdAt":"`+past+`","expiresAt":"`+past+`","owner":"o"}`), 0o600)
	e := keptSwitchyard(t, dir, nil)
	if e.srv.links.Count() != 0 {
		t.Fatalf("links restored: %d", e.srv.links.Count())
	}
	if _, err := os.Stat(filepath.Join(dir, "links", "fedcba9876543210.json")); !os.IsNotExist(err) {
		t.Fatalf("the expired file stays: %v", err)
	}
}

// A run update writes the files of the run's links again; a revoke at the
// same moment still has the last word on disk: after a restart every link
// revoked meanwhile stays revoked.
func TestARevokeOutlastsARunUpdatesWriteAcrossARestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	e := keptSwitchyard(t, dir, nil)
	hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, e, hi, hs, "Bearer test-host-token")
	d, _ := e.srv.hosts.Get(host.sessionID)
	owner := d.Owner()
	const runs, perRun = 4, 25
	group := share.Group{Name: "pair", Members: []share.GroupMember{{Name: "m0", SessionID: host.sessionID, AgentID: "cat", Status: "running"}}}
	type minted struct{ id, token string }
	var links []minted
	for r := range runs {
		run := fmt.Sprintf("run-%d", r)
		for range perRun {
			l, tok, err := e.srv.links.CreateGroupLink(run, owner, group, session.RoleView, "", 0)
			if err != nil || e.srv.keepLink(l, tok, owner, "") != nil {
				t.Fatalf("mint: %v", err)
			}
			links = append(links, minted{l.ID, tok})
		}
	}
	// Each run's files are written again, over and over, while every link is revoked.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for r := range runs {
		run := fmt.Sprintf("run-%d", r)
		for range 2 {
			wg.Go(func() {
				for {
					select {
					case <-stop:
						return
					default:
						e.srv.keepGroup(owner, run)
					}
				}
			})
		}
	}
	for _, l := range links {
		if found, _ := e.srv.links.RevokeGroup(owner, l.id); !found {
			t.Fatalf("revoke %s", l.id)
		}
	}
	close(stop)
	wg.Wait()
	host.c.Close(websocket.StatusNormalClosure, "")
	<-host.done

	b := keptSwitchyard(t, dir, nil)
	back := 0
	for _, l := range links {
		if _, err := b.srv.links.Resolve(l.token); err == nil {
			back++
		}
	}
	if back > 0 {
		t.Fatalf("%d of %d revoked links open again after a restart", back, len(links))
	}
}

// A revoked link's file that cannot be deleted at once (its directory not
// writable for a while) is deleted by a later sweep, once it can be: after
// a restart the link stays revoked.
func TestARevokedLinksFileIsDeletedOnceItCanBe(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory whose mode keeps its owner from deleting in it")
	}
	dir := filepath.Join(t.TempDir(), "data")
	e := keptSwitchyard(t, dir, nil)
	hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, e, hi, hs, "")
	id, token := mintHostLink(t, host, "r1")
	links := filepath.Join(dir, "links")
	if err := os.Chmod(links, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(links, 0o700) })
	host.send(proto.HostLinkRevokeMsg{T: proto.HostLinkRevoke, RequestID: "rv", LinkID: id})
	host.expect(proto.HostLinkRevoked)
	if _, err := os.Stat(filepath.Join(links, id+".json")); err != nil {
		t.Fatalf("the file went though its directory is read-only: %v", err)
	}
	if err := os.Chmod(links, 0o700); err != nil {
		t.Fatal(err)
	}
	e.srv.sweepLinks(time.Now())
	if _, err := os.Stat(filepath.Join(links, id+".json")); !os.IsNotExist(err) {
		t.Fatalf("the revoked link's file stays after the sweep: %v", err)
	}
	host.c.Close(websocket.StatusNormalClosure, "")
	<-host.done
	b := keptSwitchyard(t, dir, nil)
	if _, err := b.srv.links.Resolve(token); err == nil {
		t.Fatal("the revoked link opens again after a restart")
	}
}

// A link revoked, or gone with its session, before it is kept is neither
// counted against its address nor written: the address's one place stays
// free.
func TestALinkGoneAsItIsMadeIsNotKept(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	e := keptSwitchyard(t, dir, func(c *config.Config) { c.Switchyard.OpenHostLinks = 1 })
	hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, e, hi, hs, "")
	d, _ := e.srv.hosts.Get(host.sessionID)
	revoked, revokedToken, _ := e.srv.links.Create(host.sessionID, session.RoleView, "", 0)
	e.srv.links.Revoke(host.sessionID, revoked.ID)
	dropped, droppedToken, _ := e.srv.links.Create(host.sessionID, session.RoleView, "", 0)
	e.srv.links.Drop(dropped.ID)
	for _, l := range []struct {
		link  *share.Link
		token string
	}{{revoked, revokedToken}, {dropped, droppedToken}} {
		if err := e.srv.keepLink(l.link, l.token, d.Owner(), "127.0.0.1"); !errors.Is(err, errLinkGone) {
			t.Fatalf("keeping a link gone: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "links", l.link.ID+".json")); !os.IsNotExist(err) {
			t.Fatalf("a file for a link gone: %v", err)
		}
	}
	mintHostLink(t, host, "r1")
}

// linkAnswer is the switchyard's answer to the host's link request id:
// link_created or an error.
func linkAnswer(t *testing.T, h *fakeHost, id string) map[string]any {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m := <-h.msgs:
			if m["requestId"] == id {
				return m
			}
		case <-timeout:
			t.Fatalf("no answer to %s", id)
		}
	}
}

// ownerOf is the instance a hosted session was registered under, as the switchyard keeps it.
func ownerOf(t *testing.T, e *testEnv, sessionID string) string {
	t.Helper()
	d, ok := e.srv.hosts.Get(sessionID)
	if !ok {
		t.Fatalf("no hosted session %s", sessionID)
	}
	return d.Owner()
}

// leave closes the host's connection and waits until the switchyard has seen it go.
func leave(t *testing.T, e *testEnv, h *fakeHost) {
	t.Helper()
	d, ok := e.srv.hosts.Get(h.sessionID)
	if !ok {
		t.Fatalf("no hosted session %s", h.sessionID)
	}
	h.c.Close(websocket.StatusNormalClosure, "")
	<-h.done
	deadline := time.Now().Add(5 * time.Second)
	for d.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("the switchyard never saw the host go")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// awayFor makes the host instance owner look gone since d ago.
func awayFor(e *testEnv, owner string, d time.Duration) {
	k := e.srv.keeper
	k.mu.Lock()
	k.seen[owner] = time.Now().Add(-d)
	k.mu.Unlock()
}

// An open host's kept links hold their places in its address's count while
// it may still come back. Once it has been gone longer than a reconnect
// takes, another host of the address with no room left gets a link in
// place of the oldest of them, which is ended: it opens nothing and its
// file goes. Its other link still waits for its host.
func TestAGoneOpenHostsLinksGiveTheirPlacesUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	e := keptSwitchyard(t, dir, func(c *config.Config) { c.Switchyard.OpenHostLinks = 2 })
	hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, e, hi, hs, "")
	oldest, oldestToken := mintHostLink(t, host, "r1")
	newer, newerToken := mintHostLink(t, host, "r2")
	owner := ownerOf(t, e, host.sessionID)
	leave(t, e, host)
	e.srv.hosts.Expire(time.Now().Add(time.Hour))

	other, _ := dialFakeHostWith(t, e, proto.HostInfo{Name: "desk", Instance: "another-instance-0123456789"}, hs, "")
	other.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "o1", Role: "view"})
	if m := linkAnswer(t, other, "o1"); m["code"] != "link_refused" {
		t.Fatalf("while the first host may still come back: %v", m)
	}
	awayFor(e, owner, time.Hour)
	other.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "o2", Role: "view"})
	m := linkAnswer(t, other, "o2")
	if m["t"] != proto.HostLinkCreated {
		t.Fatalf("with the first host gone an hour: %v", m)
	}
	u := m["url"].(string)
	if code, out := joinStatus(t, e, u[strings.LastIndex(u, "/")+1:]); code != http.StatusOK {
		t.Fatalf("the new link: %d %v", code, out)
	}
	if code, out := joinStatus(t, e, oldestToken); code != http.StatusNotFound {
		t.Fatalf("the link that gave its place: %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "links", oldest+".json")); !os.IsNotExist(err) {
		t.Fatalf("its file stays: %v", err)
	}
	if code, out := joinStatus(t, e, newerToken); code != http.StatusServiceUnavailable {
		t.Fatalf("the gone host's other link: %d %v", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "links", newer+".json")); err != nil {
		t.Fatalf("its file: %v", err)
	}
}

// A host that comes back finds its kept links as it left them, however long
// it was gone, while no other host of its address needed their places.
func TestAHostBackFindsItsKeptLinks(t *testing.T) {
	e := keptSwitchyard(t, filepath.Join(t.TempDir(), "data"), func(c *config.Config) { c.Switchyard.OpenHostLinks = 2 })
	hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, e, hi, hs, "")
	a, _ := mintHostLink(t, host, "r1")
	b, token := mintHostLink(t, host, "r2")
	owner := ownerOf(t, e, host.sessionID)
	leave(t, e, host)
	e.srv.hosts.Expire(time.Now().Add(time.Hour))
	awayFor(e, owner, 6*24*time.Hour)
	e.srv.sweepLinks(time.Now())

	back, reg := dialFakeHostWith(t, e, hi, hs, "")
	links, _ := reg["links"].([]any)
	if back.sessionID != host.sessionID || len(links) != 2 || !slices.Contains(links, any(a)) || !slices.Contains(links, any(b)) {
		t.Fatalf("back as %s (was %s), links %v", back.sessionID, host.sessionID, reg["links"])
	}
	if code, out := joinStatus(t, e, token); code != http.StatusOK {
		t.Fatalf("a link after its host is back: %d %v", code, out)
	}
	// Back, it holds its places again: another host of the address has no room.
	other, _ := dialFakeHostWith(t, e, proto.HostInfo{Name: "desk", Instance: "another-instance-0123456789"}, proto.HostSession{Name: "other", AgentID: "cat", Command: []string{"cat"}, Cwd: e.root, Cols: 80, Rows: 24, LocalID: "2"}, "")
	awayFor(e, owner, time.Hour)
	other.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "o1", Role: "view"})
	if m := linkAnswer(t, other, "o1"); m["code"] != "link_refused" {
		t.Fatalf("the places of a connected host's links: %v", m)
	}
}

// The sweep leaves the kept links of a connected host alone however long it
// has been connected, and takes them once it has been gone linkOrphanAfter.
func TestTheSweepKeepsAConnectedHostsLinks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	e := keptSwitchyard(t, dir, nil)
	hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, e, hi, hs, "Bearer test-host-token")
	id, token := mintHostLink(t, host, "r1")
	later := time.Now().Add(linkOrphanAfter + time.Hour)
	e.srv.sweepLinks(later)
	if code, out := joinStatus(t, e, token); code != http.StatusOK {
		t.Fatalf("a connected host's link after the sweep: %d %v", code, out)
	}
	leave(t, e, host)
	e.srv.sweepLinks(time.Now().Add(linkOrphanAfter - time.Hour))
	if code, out := joinStatus(t, e, token); code != http.StatusOK {
		t.Fatalf("a link whose host has been gone less than %v: %d %v", linkOrphanAfter, code, out)
	}
	e.srv.sweepLinks(time.Now().Add(linkOrphanAfter + time.Hour))
	if code, _ := joinStatus(t, e, token); code != http.StatusNotFound {
		t.Fatalf("a link whose host has been gone %v: %d", linkOrphanAfter, code)
	}
	if _, err := os.Stat(filepath.Join(dir, "links", id+".json")); !os.IsNotExist(err) {
		t.Fatalf("its file stays: %v", err)
	}
}

// After a restart the restored links hold their places for their hosts as
// long as a reconnect takes, counted from the start, then give them up as
// a gone host's do.
func TestRestoredLinksHoldTheirPlacesForAWhile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	limit := func(c *config.Config) { c.Switchyard.OpenHostLinks = 1 }
	a := keptSwitchyard(t, dir, limit)
	hi, hs := instanceHost(a, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, a, hi, hs, "")
	_, token := mintHostLink(t, host, "r1")
	owner := ownerOf(t, a, host.sessionID)
	leave(t, a, host)

	b := keptSwitchyard(t, dir, limit)
	other, _ := dialFakeHostWith(t, b, proto.HostInfo{Name: "desk", Instance: "another-instance-0123456789"}, hs, "")
	other.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "o1", Role: "view"})
	if m := linkAnswer(t, other, "o1"); m["code"] != "link_refused" {
		t.Fatalf("just after the restart: %v", m)
	}
	awayFor(b, owner, time.Hour)
	other.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "o2", Role: "view"})
	if m := linkAnswer(t, other, "o2"); m["t"] != proto.HostLinkCreated {
		t.Fatalf("with the restored link's host away an hour: %v", m)
	}
	if code, _ := joinStatus(t, b, token); code != http.StatusNotFound {
		t.Fatalf("the restored link that gave its place: %d", code)
	}
}

// A link refused for want of room ends no other: with the switchyard's
// links at their bound, a gone host's link keeps its place at the address.
func TestALinkRefusedEndsNoOther(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	e := keptSwitchyard(t, dir, func(c *config.Config) { c.Switchyard.OpenHostLinks = 1 })
	hi, hs := instanceHost(e, "instance-secret-0123456789", "1")
	host, _ := dialFakeHostWith(t, e, hi, hs, "")
	id, _ := mintHostLink(t, host, "r1")
	owner := ownerOf(t, e, host.sessionID)
	leave(t, e, host)
	awayFor(e, owner, time.Hour)
	for i := 0; e.srv.links.Count() <= maxDurableLinks; i++ {
		if _, _, err := e.srv.links.CreateRunLink(fmt.Sprintf("run-%d", i/share.MaxLinksPerSession), session.RoleView, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	other, _ := dialFakeHostWith(t, e, proto.HostInfo{Name: "desk", Instance: "another-instance-0123456789"}, hs, "")
	other.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "o1", Role: "view"})
	if m := linkAnswer(t, other, "o1"); m["code"] != "link_refused" {
		t.Fatalf("past the switchyard's bound: %v", m)
	}
	if _, ok := e.srv.links.Get(id); !ok {
		t.Fatal("the gone host's link was ended for a link refused")
	}
	if _, err := os.Stat(filepath.Join(dir, "links", id+".json")); err != nil {
		t.Fatalf("its file: %v", err)
	}
}

// Links give their places up only when that makes room for the new one:
// at an address holding more than its bound (lowered across a restart),
// none is ended while too few may give way.
func TestLinksGiveTheirPlacesUpOnlyForRoom(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	a := keptSwitchyard(t, dir, func(c *config.Config) { c.Switchyard.OpenHostLinks = 3 })
	hi, hs := instanceHost(a, "instance-secret-0123456789", "1")
	one, _ := dialFakeHostWith(t, a, hi, hs, "")
	two, _ := dialFakeHostWith(t, a, proto.HostInfo{Name: "desk", Instance: "another-instance-0123456789"}, hs, "")
	var ids []string
	for i, h := range []*fakeHost{one, one, two} {
		id, _ := mintHostLink(t, h, fmt.Sprintf("r%d", i))
		ids = append(ids, id)
	}
	owners := []string{ownerOf(t, a, one.sessionID), ownerOf(t, a, two.sessionID)}
	leave(t, a, one)
	leave(t, a, two)

	b := keptSwitchyard(t, dir, func(c *config.Config) { c.Switchyard.OpenHostLinks = 1 })
	third, _ := dialFakeHostWith(t, b, proto.HostInfo{Name: "box", Instance: "third-instance-0123456789"}, hs, "")
	awayFor(b, owners[0], time.Hour)
	third.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "t1", Role: "view"})
	if m := linkAnswer(t, third, "t1"); m["code"] != "link_refused" {
		t.Fatalf("with two of three links free to go: %v", m)
	}
	for _, id := range ids {
		if _, ok := b.srv.links.Get(id); !ok {
			t.Fatalf("link %s was ended for a link refused", id)
		}
	}
	awayFor(b, owners[1], time.Hour)
	third.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "t2", Role: "view"})
	if m := linkAnswer(t, third, "t2"); m["t"] != proto.HostLinkCreated {
		t.Fatalf("with all three free to go: %v", m)
	}
	for _, id := range ids {
		if _, ok := b.srv.links.Get(id); ok {
			t.Fatalf("link %s kept its place", id)
		}
	}
}
