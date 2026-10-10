package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/proto"
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
