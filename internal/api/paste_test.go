package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/paste"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// A fake answerer stands in for hostagent.AnswerPaste (api stays free of
// hostagent; the real peer is tested there, and end to end by paste.spec.ts).
type fakePastePeer struct {
	mu     sync.Mutex
	closed bool
	since  time.Time
}

func (p *fakePastePeer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
}
func (p *fakePastePeer) Connected() bool  { return true }
func (p *fakePastePeer) Since() time.Time { return p.since }

func fakeAnswerer(peers *[]*fakePastePeer) PasteAnswerer {
	return func(ctx context.Context, local *session.Local, offer string, role session.Role, label string, ice []proto.ICEServer) (PastePeer, string, error) {
		if !strings.HasPrefix(offer, "v=0") {
			return nil, "", context.Canceled
		}
		p := &fakePastePeer{since: time.Now()}
		*peers = append(*peers, p)
		return p, "v=0\r\ns=answer for " + label + " as " + string(role) + "\r\n", nil
	}
}

// POST /api/sessions/{id}/paste decodes the viewer's blob, has the answerer
// answer it for the server session, keeps the peer until the session ends
// and records the invite; bad blobs, a bad role, a hosted session and a
// missing token are refused; the STUN route lists the servers without a
// TURN secret; a server without an answerer says so.
func TestPasteRouteAnswersAnOffer(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) {
		c.ICEServers = []config.ICEServer{{URLs: []string{"stun:stun.example:3478"}}, {URLs: []string{"turn:turn.example:3478"}, Username: "u", Credential: "secret"}}
	})
	var peers []*fakePastePeer
	e.srv.SetPasteAnswerer(fakeAnswerer(&peers))
	id := e.createSession("cat")
	offer, _ := paste.EncodeBlob("offer", "v=0\r\ns=viewer offer\r\n")
	resp, out := e.do("POST", "/api/sessions/"+id+"/paste", adminToken, map[string]any{"offer": offer, "role": "control", "label": "a friend"})
	if resp.StatusCode != http.StatusCreated || out["role"] != "control" {
		t.Fatalf("paste: %d %v", resp.StatusCode, out)
	}
	answer, err := paste.DecodeBlob(out["answer"].(string), "answer")
	if err != nil || answer != "v=0\r\ns=answer for a friend as control\r\n" {
		t.Fatalf("answer %q %v", answer, err)
	}
	if len(peers) != 1 || peers[0].closed {
		t.Fatalf("peers %+v", peers)
	}
	recorded := false
	for _, entry := range e.local(id).Activity() {
		if entry.Type == "link" && entry.Message == "paste invite answered (control)" {
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("activity %+v", e.local(id).Activity())
	}

	for _, tc := range []struct {
		body map[string]any
		code string
	}{
		{map[string]any{"offer": "hello", "role": "view"}, "invalid_offer"},
		{map[string]any{"offer": offer, "role": "admin"}, "invalid_role"},
		{map[string]any{"offer": func() string { b, _ := paste.EncodeBlob("answer", "v=0\r\n"); return b }(), "role": "view"}, "invalid_offer"},
		{map[string]any{"offer": func() string { b, _ := paste.EncodeBlob("offer", "not an sdp"); return b }(), "role": "view"}, "invalid_offer"},
	} {
		if resp, out := e.do("POST", "/api/sessions/"+id+"/paste", adminToken, tc.body); resp.StatusCode == http.StatusCreated || errorCode(out) != tc.code {
			t.Errorf("%v: %d %v", tc.body, resp.StatusCode, out)
		}
	}
	// An over-long blob is refused before it is read (by the body bound or the blob's own).
	if resp, out := e.do("POST", "/api/sessions/"+id+"/paste", adminToken, map[string]any{"offer": paste.BlobPrefix + strings.Repeat("A", paste.MaxBlob+1), "role": "view"}); resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("too long: %d %v", resp.StatusCode, errorCode(out))
	}
	if resp, _ := e.do("POST", "/api/sessions/"+id+"/paste", "", map[string]any{"offer": offer, "role": "view"}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	host := dialFakeHost(t, e, "hosted-agent-token")
	if resp, out := e.do("POST", "/api/sessions/"+host.sessionID+"/paste", adminToken, map[string]any{"offer": offer, "role": "view"}); resp.StatusCode != http.StatusBadRequest || errorCode(out) != "hosted_session" {
		t.Fatalf("hosted: %d %v", resp.StatusCode, out)
	}
	// The STUN route: the TURN server's credentials never leave.
	resp, out = e.do("GET", "/api/ice", "", nil)
	if b, _ := json.Marshal(out["stun"]); resp.StatusCode != http.StatusOK || string(b) != `["stun:stun.example:3478"]` {
		t.Fatalf("ice: %d %v", resp.StatusCode, out)
	}
	// The session's end closes the peer.
	e.do("DELETE", "/api/sessions/"+id, adminToken, nil)
	e.do("DELETE", "/api/sessions/"+id, adminToken, nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.srv.pastes.mu.Lock()
		_, kept := e.srv.pastes.peers[id]
		e.srv.pastes.mu.Unlock()
		peers[0].mu.Lock()
		closed := peers[0].closed
		peers[0].mu.Unlock()
		if !kept && closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the paste peer outlived the session: kept %v closed %v", kept, closed)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Without an answerer the route says so.
	plain := newTestEnv(t, nil)
	pid := plain.createSession("cat")
	if resp, out := plain.do("POST", "/api/sessions/"+pid+"/paste", adminToken, map[string]any{"offer": offer, "role": "view"}); resp.StatusCode != http.StatusServiceUnavailable || errorCode(out) != "paste_unavailable" {
		t.Fatalf("no answerer: %d %v", resp.StatusCode, out)
	}
}

// At most 16 paste peers a session; closeAll ends them.
func TestPasteStoreBounds(t *testing.T) {
	var ps pasteStore
	for i := 0; i < maxPastesPerSession; i++ {
		if !ps.add("s", &fakePastePeer{since: time.Now()}) {
			t.Fatalf("peer %d refused", i)
		}
	}
	if ps.add("s", &fakePastePeer{since: time.Now()}) {
		t.Fatal("a seventeenth peer was kept")
	}
	ps.closeAll("s")
	if len(ps.peers["s"]) != 0 {
		t.Fatal("peers kept after closeAll")
	}
}
