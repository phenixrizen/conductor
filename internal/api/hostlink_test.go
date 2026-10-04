package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/proto"
)

// A host asks the server for a share link to its session over its control
// connection and gets link_created: the URL on the server's public base,
// the same as an invite, and the link as the server lists it, which a
// viewer then joins by. A bad role, a long label, a long lifetime and the
// sixth request in a minute are refused with an error naming the request;
// the connection stays.
func TestAHostMintsLinksAtTheServer(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://switchyard.example.net" })
	host := dialFakeHost(t, e, "hosted-agent-token")
	host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "r1", Role: "view", TTLSeconds: 3600, Label: "for a friend"})
	m := host.expect(proto.HostLinkCreated)
	url, _ := m["url"].(string)
	if m["requestId"] != "r1" || !strings.HasPrefix(url, "https://switchyard.example.net/join/") || m["invite"] != "conductor://switchyard.example.net/join/"+strings.TrimPrefix(url, "https://switchyard.example.net/join/") || m["role"] != "view" || m["label"] != "for a friend" || m["linkId"] == "" || m["expiresAt"] == "" {
		t.Fatalf("link_created %v", m)
	}
	token := strings.TrimPrefix(url, "https://switchyard.example.net/join/")
	if resp, out := e.do("GET", "/api/join/"+token, "", nil); resp.StatusCode != http.StatusOK || out["session"] == nil {
		t.Fatalf("join by the minted link: %d %v", resp.StatusCode, out)
	}
	// Listed on the session, with its label.
	if _, out := e.do("GET", "/api/sessions/"+host.sessionID+"/links", adminToken, nil); !strings.Contains(fmt.Sprint(out), "label:for a friend") {
		t.Fatalf("links %v", out)
	}

	for _, tc := range []struct {
		msg  proto.HostLinkMsg
		code string
	}{
		{proto.HostLinkMsg{T: proto.HostLink, RequestID: "r2", Role: "admin"}, "invalid_role"},
		{proto.HostLinkMsg{T: proto.HostLink, RequestID: "r3", Role: "view", Label: strings.Repeat("x", proto.MaxLinkLabel+1)}, "invalid_request"},
		{proto.HostLinkMsg{T: proto.HostLink, RequestID: "r4", Role: "view", TTLSeconds: proto.MaxLinkTTLSeconds + 1}, "invalid_request"},
	} {
		host.send(tc.msg)
		m := host.expect(proto.HostError)
		if m["requestId"] != tc.msg.RequestID || m["code"] != tc.code {
			t.Fatalf("%+v: %v", tc.msg, m)
		}
	}
	// Four more within the minute pass (five in all), the sixth is refused.
	for i := 0; i < proto.LinkRequestsPerMinute-1; i++ {
		host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "ok", Role: "control"})
		host.expect(proto.HostLinkCreated)
	}
	host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "r9", Role: "view"})
	if m := host.expect(proto.HostError); m["code"] != "rate_limited" || m["requestId"] != "r9" {
		t.Fatalf("sixth: %v", m)
	}
	// A request without an id is a protocol error: the connection closes.
	host.send(map[string]any{"t": proto.HostLink, "role": "view"})
	select {
	case <-host.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the connection stayed open after a link request without an id")
	}
}

// A link reply names the invite the desktop app opens, beside the URL.
func TestLinkRepliesCarryAnInvite(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://conductor.example.net" })
	id := e.createSession("cat")
	resp, out := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	url, _ := out["url"].(string)
	if resp.StatusCode != http.StatusCreated || out["invite"] != "conductor://conductor.example.net/join/"+strings.TrimPrefix(url, "https://conductor.example.net/join/") {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
}

// A host revokes a link the server minted for it: the answer names the
// link, a join by it is refused as revoked, a viewer through it is closed;
// an unknown link answers not_found; a malformed revoke ends the connection.
func TestAHostRevokesItsLinkAtTheServer(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://switchyard.example.net" })
	host := dialFakeHost(t, e, "hosted-agent-token")
	host.send(proto.HostLinkMsg{T: proto.HostLink, RequestID: "r1", Role: "view", TTLSeconds: 3600})
	m := host.expect(proto.HostLinkCreated)
	linkID, _ := m["linkId"].(string)
	token := strings.TrimPrefix(m["url"].(string), "https://switchyard.example.net/join/")
	v := dialViewer(t, e, host.sessionID, token)
	v.hello(80, 24)
	v.expectControl(proto.CtlWelcome)
	host.expect(proto.HostViewerJoin)

	host.send(proto.HostLinkRevokeMsg{T: proto.HostLinkRevoke, RequestID: "x1", LinkID: linkID})
	if r := host.expect(proto.HostLinkRevoked); r["requestId"] != "x1" || r["linkId"] != linkID {
		t.Fatalf("link_revoked %v", r)
	}
	if resp, out := e.do("GET", "/api/join/"+token, "", nil); resp.StatusCode != http.StatusNotFound || fmt.Sprint(out) == "" {
		t.Fatalf("join after revoke: %d %v", resp.StatusCode, out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := v.read(); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the viewer through the revoked link was not closed")
		}
	}
	host.send(proto.HostLinkRevokeMsg{T: proto.HostLinkRevoke, RequestID: "x2", LinkID: "nope"})
	if r := host.expect(proto.HostError); r["code"] != "not_found" || r["requestId"] != "x2" {
		t.Fatalf("unknown link: %v", r)
	}
	host.send(proto.HostLinkRevokeMsg{T: proto.HostLinkRevoke, RequestID: "x3", LinkID: strings.Repeat("l", proto.MaxLinkID+1)})
	select {
	case <-host.done:
	case <-time.After(5 * time.Second):
		t.Fatal("an oversize link id did not end the connection")
	}
}
