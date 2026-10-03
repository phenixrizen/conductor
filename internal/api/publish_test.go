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
	if _, out := local.do("GET", "/api/sessions/"+id+"/links", adminToken, nil); strings.Contains(fmt.Sprint(out), "for the PR") {
		t.Fatalf("the local store kept a remote link: %v", out)
	}
}
