package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/hostagent"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

type uplinkPublisher struct{ u *hostagent.Uplink }

func (p uplinkPublisher) Publish(ctx context.Context, local *session.Local) (PublishedSession, error) {
	return p.u.Publish(ctx, local)
}

func (p uplinkPublisher) Server() string { return p.u.ServerURL }

// A server with a rendezvous publishes every session it starts there: the
// rendezvous lists it as hosted by this server, a viewer there types into
// it through the relay, its activity shows there, and ending the session
// here ends it there.
func TestAPublishedServerSessionIsViewableAtTheRendezvous(t *testing.T) {
	rendezvous := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://rendezvous.example.net" })
	local := newTestEnv(t, nil)
	local.srv.SetPublisher(uplinkPublisher{&hostagent.Uplink{ServerURL: rendezvous.http.URL, Token: "test-host-token", HostName: "office-server", RelayOnly: true}})

	id := local.createSession("cat")
	var hosted session.Info
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && hosted.ID == "" {
		rendezvous.srv.Registry().Each(func(d session.Driver) {
			if i := d.Info(); i.Kind == session.KindHosted && i.HostName == "office-server" {
				hosted = i
			}
		})
		time.Sleep(20 * time.Millisecond)
	}
	if hosted.ID == "" {
		t.Fatal("the rendezvous never listed the session")
	}
	if hosted.ID == id || hosted.AgentID != "cat" || !strings.HasPrefix(hosted.Name, "cat #") {
		t.Fatalf("hosted %+v", hosted)
	}
	// The local session's activity says where it is published.
	d, _ := local.srv.Registry().Get(id)
	deadline = time.Now().Add(5 * time.Second)
	var link string
	for time.Now().Before(deadline) && link == "" {
		for _, e := range d.(*session.Local).Activity() {
			if e.Type == session.ActivityLink && strings.Contains(e.Message, "rendezvous") {
				link = e.URL
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if link != "https://rendezvous.example.net/sessions/"+hosted.ID {
		t.Fatalf("published link %q", link)
	}

	v := dialViewer(t, rendezvous, hosted.ID, adminToken)
	v.expectControl(proto.CtlWelcome)
	relay, _ := proto.EncodeJSON(proto.TypeSignal, proto.RelayRequest{T: proto.SigRelay, Reason: "forced"})
	v.send(relay)
	for {
		f, err := v.read()
		if err != nil {
			t.Fatalf("waiting for relay_ok: %v", err)
		}
		var m map[string]any
		if f.Type == proto.TypeSignal && json.Unmarshal(f.Payload, &m) == nil && m["t"] == proto.SigRelayOK {
			break
		}
	}
	v.hello(90, 25)
	v.expectControl(proto.CtlWelcome)
	v.expectControl(proto.CtlReady)
	v.send(proto.Encode(proto.TypeInput, []byte("hello from afar\n")))
	v.expectOutput("hello from afar")

	// A viewer on the local server sees the same session.
	lv := dialViewer(t, local, id, adminToken)
	lv.hello(80, 24)
	lv.expectControl(proto.CtlWelcome)
	lv.expectOutput("hello from afar")

	// An event reported to the local server shows at the rendezvous: it
	// reaches the rendezvous's event hub (its admin stream and webhooks).
	seen := make(chan struct{}, 1)
	rendezvous.srv.events.addSink(func(sid string, e session.ActivityEntry, _ session.AttentionState) {
		if sid == hosted.ID && e.Type == session.ActivityProgress && e.Message == "step 1" {
			select {
			case seen <- struct{}{}:
			default:
			}
		}
	})
	resp, _ := local.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "progress", "message": "step 1"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("event: %d", resp.StatusCode)
	}
	select {
	case <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the event did not reach the rendezvous")
	}
	rd, _ := rendezvous.srv.Registry().Get(hosted.ID)

	// Ending the session here ends it there.
	if resp, _ := local.do("DELETE", "/api/sessions/"+id, adminToken, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !rd.Info().Status.Ended() {
		time.Sleep(20 * time.Millisecond)
	}
	if !rd.Info().Status.Ended() {
		t.Fatalf("rendezvous still shows %s", rd.Info().Status)
	}
}

// A link to a published session is minted at the rendezvous, over the host
// connection: its URL and invite are the rendezvous's, and the rendezvous
// resolves it.
func TestLinkOnAPublishedSessionIsMintedAtTheRendezvous(t *testing.T) {
	rendezvous := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://rendezvous.example.net" })
	local := newTestEnv(t, nil)
	local.srv.SetPublisher(uplinkPublisher{&hostagent.Uplink{ServerURL: rendezvous.http.URL, Token: "test-host-token", HostName: "office-server", RelayOnly: true}})
	id := local.createSession("cat")
	deadline := time.Now().Add(10 * time.Second)
	for local.srv.publishedOf(id) == nil {
		if time.Now().After(deadline) {
			t.Fatal("not published")
		}
		time.Sleep(20 * time.Millisecond)
	}
	resp, out := local.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view", "label": "for the PR", "ttlSeconds": 3600})
	url, _ := out["url"].(string)
	if resp.StatusCode != http.StatusCreated || !strings.HasPrefix(url, "https://rendezvous.example.net/join/") || out["remote"] != true || out["token"] != nil {
		t.Fatalf("link: %d %v", resp.StatusCode, out)
	}
	token := strings.TrimPrefix(url, "https://rendezvous.example.net/join/")
	if out["invite"] != "conductor://rendezvous.example.net/join/"+token {
		t.Fatalf("invite %v", out["invite"])
	}
	link, _ := out["link"].(map[string]any)
	if link["label"] != "for the PR" || link["role"] != "view" || link["remote"] != true {
		t.Fatalf("link %v", link)
	}
	if resp, out := rendezvous.do("GET", "/api/join/"+token, "", nil); resp.StatusCode != http.StatusOK || out["session"] == nil {
		t.Fatalf("the rendezvous resolves it: %d %v", resp.StatusCode, out)
	}
	// Listed here as the rendezvous's, with no viewer count of its own.
	if _, out := local.do("GET", "/api/sessions/"+id+"/links", adminToken, nil); !strings.Contains(fmt.Sprint(out), "for the PR") || !strings.Contains(fmt.Sprint(out), "remote:true") {
		t.Fatalf("the remote link is not listed: %v", out)
	}
}

// A link minted at the rendezvous is revoked from here: the DELETE goes over
// the host connection, the rendezvous refuses the join afterwards, and the
// list no longer holds it. A link the rendezvous forgot is dropped the same way.
func TestRemoteLinksAreListedAndRevokedFromTheLocalServer(t *testing.T) {
	rendezvous := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://rendezvous.example.net" })
	local := newTestEnv(t, nil)
	local.srv.SetPublisher(uplinkPublisher{&hostagent.Uplink{ServerURL: rendezvous.http.URL, Token: "test-host-token", HostName: "office-server", RelayOnly: true}})
	id := local.createSession("cat")
	deadline := time.Now().Add(10 * time.Second)
	for local.srv.publishedOf(id) == nil {
		if time.Now().After(deadline) {
			t.Fatal("not published")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, out := local.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "control", "label": "pairing"})
	link, _ := out["link"].(map[string]any)
	linkID, _ := link["id"].(string)
	token := strings.TrimPrefix(out["url"].(string), "https://rendezvous.example.net/join/")
	if linkID == "" || token == "" {
		t.Fatalf("link %v", out)
	}
	if resp, _ := local.do("DELETE", "/api/sessions/"+id+"/links/"+linkID, adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if resp, out := rendezvous.do("GET", "/api/join/"+token, "", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the rendezvous still resolves it: %d %v", resp.StatusCode, out)
	}
	if _, out := local.do("GET", "/api/sessions/"+id+"/links", adminToken, nil); strings.Contains(fmt.Sprint(out), "pairing") {
		t.Fatalf("still listed: %v", out)
	}
	if resp, _ := local.do("DELETE", "/api/sessions/"+id+"/links/"+linkID, adminToken, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("revoking it twice: %d", resp.StatusCode)
	}
}

// A server with no host token publishes to a switchyard that admits open
// hosts: the session is listed there and a link is minted there.
func TestAServerPublishesToAnOpenSwitchyardWithoutAToken(t *testing.T) {
	rendezvous := newTestEnv(t, func(c *config.Config) {
		c.PublicURL = "https://rendezvous.example.net"
		c.Switchyard.Enabled = true
		c.Switchyard.OpenHosts = true
	})
	local := newTestEnv(t, nil)
	local.srv.SetPublisher(uplinkPublisher{&hostagent.Uplink{ServerURL: rendezvous.http.URL, HostName: "open-box", RelayOnly: true}})
	id := local.createSession("cat")
	var hosted session.Info
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && hosted.ID == "" {
		rendezvous.srv.Registry().Each(func(d session.Driver) {
			if i := d.Info(); i.Kind == session.KindHosted && i.HostName == "open-box" {
				hosted = i
			}
		})
		time.Sleep(20 * time.Millisecond)
	}
	if hosted.ID == "" {
		t.Fatal("the open switchyard never listed the session")
	}
	resp, out := local.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	if resp.StatusCode != http.StatusCreated || out["remote"] != true || !strings.HasPrefix(out["url"].(string), "https://rendezvous.example.net/join/") {
		t.Fatalf("link: %d %v", resp.StatusCode, out)
	}
}

// A link asked for while the session is not published (the switchyard
// refused or is down) is made here and the reply says which switchyard and
// why, so the Share dialog can say it in words.
func TestLinkOnAnUnpublishedSessionSaysWhy(t *testing.T) {
	local := newTestEnv(t, nil)
	local.srv.SetPublisher(uplinkPublisher{&hostagent.Uplink{ServerURL: "http://127.0.0.1:1", HostName: "office-server", RelayOnly: true}})
	id := local.createSession("cat")
	resp, out := local.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	rv, _ := out["rendezvous"].(map[string]any)
	if resp.StatusCode != http.StatusCreated || out["remote"] != nil || out["token"] == nil || rv == nil || rv["server"] != "http://127.0.0.1:1" || rv["error"] == "" {
		t.Fatalf("link: %d %v", resp.StatusCode, out)
	}
	if !strings.HasPrefix(out["url"].(string), "http://example.test/join/") {
		t.Fatalf("not a local link: %v", out["url"])
	}
}

// waitMembersPublished waits until every member of a run with a session is published.
func waitMembersPublished(t *testing.T, e *testEnv, runID string, n int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		run, _ := e.srv.runs.Get(runID)
		published := 0
		for _, m := range run.Members {
			if m.SessionID != "" && e.srv.publishedOf(m.SessionID) != nil {
				published++
			}
		}
		if published >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d members published", published, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A run whose members are published is shared at the rendezvous: one link,
// whose join lists the members as hosted sessions there, following a member
// added later; the run's links list it, and a revoke here revokes it there.
func TestARunLinkOnAPublishedRunIsMintedAtTheRendezvous(t *testing.T) {
	rendezvous := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://rendezvous.example.net" })
	local := newTestEnv(t, nil)
	local.srv.SetPublisher(uplinkPublisher{&hostagent.Uplink{ServerURL: rendezvous.http.URL, Token: "test-host-token", HostName: "office-server", RelayOnly: true}})
	runID := local.launchCrew(t, "Pair", catMember("lead", "immediately"), catMember("core", "immediately"))
	waitMembersPublished(t, local, runID, 2)
	resp, out := local.do("POST", "/api/runs/"+runID+"/links", adminToken, map[string]any{"role": "view", "label": "for the team"})
	if resp.StatusCode != http.StatusCreated || out["remote"] != true {
		t.Fatalf("run link: %d %v", resp.StatusCode, out)
	}
	linkID := out["link"].(map[string]any)["id"].(string)
	token := strings.TrimPrefix(out["url"].(string), "https://rendezvous.example.net/join/")
	members := func() []any {
		_, got := rendezvous.do("GET", "/api/join/"+token, token, nil)
		run, _ := got["run"].(map[string]any)
		ms, _ := run["members"].([]any)
		return ms
	}
	if ms := members(); len(ms) != 2 || ms[0].(map[string]any)["kind"] != "hosted" || ms[0].(map[string]any)["sessionId"] == nil {
		t.Fatalf("the rendezvous's join: %v", ms)
	}
	// A viewer through the run link reaches a member's session there.
	sid := members()[0].(map[string]any)["sessionId"].(string)
	v := dialViewer(t, rendezvous, sid, token)
	v.hello(80, 24)
	v.expectControl(proto.CtlWelcome)
	if resp, out := local.do("POST", "/api/runs/"+runID+"/members", adminToken, catMember("tests", "immediately")); resp.StatusCode >= 300 {
		t.Fatalf("add member: %d %v", resp.StatusCode, out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(members()) != 3 {
		if time.Now().After(deadline) {
			t.Fatalf("the rendezvous never heard of the third member: %v", members())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, out := local.do("GET", "/api/runs/"+runID+"/links", adminToken, nil); !strings.Contains(fmt.Sprint(out), "remote:true") {
		t.Fatalf("the run's links: %v", out)
	}
	if resp, _ := local.do("DELETE", "/api/runs/"+runID+"/links/"+linkID, adminToken, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if resp, out := rendezvous.do("GET", "/api/join/"+token, token, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the rendezvous still resolves it: %d %v", resp.StatusCode, out)
	}
}

// With no member published, the run's link is made here and says why.
func TestARunLinkFallsBackToALocalLinkWhenNoMemberIsPublished(t *testing.T) {
	local := newTestEnv(t, nil)
	local.srv.SetPublisher(uplinkPublisher{&hostagent.Uplink{ServerURL: "http://127.0.0.1:1", Token: "test-host-token", HostName: "office-server"}})
	runID := local.launchCrew(t, "Alone", catMember("lead", "immediately"))
	resp, out := local.do("POST", "/api/runs/"+runID+"/links", adminToken, map[string]any{"role": "view"})
	rv, _ := out["rendezvous"].(map[string]any)
	if resp.StatusCode != http.StatusCreated || out["remote"] == true || rv == nil || rv["error"] == "" {
		t.Fatalf("fallback: %d %v", resp.StatusCode, out)
	}
}

// A session that ended takes its links with it: none are listed, local or
// minted at the rendezvous, the rendezvous no longer resolves them, and a
// revoke of one (a dialog still showing it) succeeds rather than erring.
func TestASessionThatEndedTakesItsLinks(t *testing.T) {
	rendezvous := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://rendezvous.example.net" })
	local := newTestEnv(t, nil)
	local.srv.SetPublisher(uplinkPublisher{&hostagent.Uplink{ServerURL: rendezvous.http.URL, Token: "test-host-token", HostName: "office-server", RelayOnly: true}})
	id := local.createSession("cat")
	deadline := time.Now().Add(10 * time.Second)
	for local.srv.publishedOf(id) == nil {
		if time.Now().After(deadline) {
			t.Fatal("not published")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_, out := local.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "control"})
	remoteID := out["link"].(map[string]any)["id"].(string)
	token := strings.TrimPrefix(out["url"].(string), "https://rendezvous.example.net/join/")
	local.srv.SetPublisher(nil)
	_, out = local.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	localID := out["link"].(map[string]any)["id"].(string)
	if resp, _ := local.do("DELETE", "/api/sessions/"+id, adminToken, nil); resp.StatusCode >= 300 {
		t.Fatalf("stop: %d", resp.StatusCode)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		d, _ := local.srv.registry.Get(id)
		if d != nil && d.Info().Status.Ended() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session never ended")
		}
		time.Sleep(20 * time.Millisecond)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		_, out := local.do("GET", "/api/sessions/"+id+"/links", adminToken, nil)
		if ls, _ := out["links"].([]any); len(ls) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("an ended session still lists links: %v", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, lid := range []string{remoteID, localID} {
		start := time.Now()
		if resp, out := local.do("DELETE", "/api/sessions/"+id+"/links/"+lid, adminToken, nil); resp.StatusCode != http.StatusNoContent || time.Since(start) > 3*time.Second {
			t.Fatalf("revoking %s after the end: %d %v in %v", lid, resp.StatusCode, out, time.Since(start))
		}
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		resp, _ := rendezvous.do("GET", "/api/join/"+token, token, nil)
		if resp.StatusCode == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the rendezvous still resolves the ended session's link: %d", resp.StatusCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
