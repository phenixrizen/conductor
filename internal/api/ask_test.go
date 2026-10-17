package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// attentionOf is a session's attention as GET /api/sessions/{id} lists it.
func (e *testEnv) attentionOf(id string) map[string]any {
	e.t.Helper()
	_, got := e.do("GET", "/api/sessions/"+id, adminToken, nil)
	s, _ := got["session"].(map[string]any)
	att, _ := s["attention"].(map[string]any)
	return att
}

// The agent's own question (#193): the attention route's ask, with
// needs_input, marks it, and the turn's done (with turn, as a hook reports
// it) leaves the session needs_input with the question's words; the done
// is recorded all the same, and a webhook for done fires once. The events
// route's attention words take ask too. ask is a boolean; it is never sent
// to a browser.
func TestTheAgentsOwnQuestionOutlastsItsTurnsDone(t *testing.T) {
	rcv := newReceiver(t, nil)
	e, _ := newWebhookEnv(t, config.Webhook{URL: rcv.URL, Events: []string{"needs_input", "done"}, AllowPrivate: true})
	id := e.createSession("cat")
	tok := e.agentToken(id)
	if resp, out := e.do("POST", "/api/sessions/"+id+"/attention", tok, map[string]any{"state": "needs_input", "message": "Which database?", "ask": true}); resp.StatusCode != http.StatusOK {
		t.Fatalf("ask: %d %v", resp.StatusCode, out)
	}
	if resp, out := e.do("POST", "/api/sessions/"+id+"/attention", tok, map[string]any{"state": "done", "message": "asked about the database", "turn": true}); resp.StatusCode != http.StatusOK {
		t.Fatalf("done: %d %v", resp.StatusCode, out)
	}
	att := e.attentionOf(id)
	if att["state"] != "needs_input" || att["message"] != "Which database?" {
		t.Fatalf("after the turn's done: %v", att)
	}
	if _, sent := att["ask"]; sent {
		t.Fatalf("the mark is listed: %v", att)
	}
	for _, want := range []struct{ event, message string }{{"needs_input", "Which database?"}, {"done", "asked about the database"}} {
		d := rcv.next(t)
		if got := d.header.Get("X-Conductor-Event"); got != want.event || d.message(t) != want.message {
			t.Fatalf("webhook %s %q, want %s %q", got, d.message(t), want.event, want.message)
		}
	}
	rcv.none(t, 300*time.Millisecond)

	// The events route's attention words.
	if resp, out := e.do("POST", "/api/sessions/"+id+"/events", tok, map[string]any{"type": "needs_input", "message": "Which port?", "ask": true}); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("ask by the events route: %d %v", resp.StatusCode, out)
	}
	if resp, out := e.do("POST", "/api/sessions/"+id+"/events", tok, map[string]any{"type": "done"}); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("done by the events route: %d %v", resp.StatusCode, out)
	}
	if att := e.attentionOf(id); att["state"] != "needs_input" || att["message"] != "Which port?" {
		t.Fatalf("after a done by the events route: %v", att)
	}

	// Without the mark, as before: a done replaces a needs_input.
	other := e.createSession("cat")
	otherTok := e.agentToken(other)
	for _, body := range []map[string]any{{"state": "needs_input", "message": "Allow Bash?", "ask": false}, {"state": "done", "turn": true}} {
		if resp, out := e.do("POST", "/api/sessions/"+other+"/attention", otherTok, body); resp.StatusCode != http.StatusOK {
			t.Fatalf("%v: %d %v", body, resp.StatusCode, out)
		}
	}
	if att := e.attentionOf(other); att["state"] != "done" {
		t.Fatalf("a done over a wait without the mark: %v", att)
	}

	// ask is a boolean, on both routes.
	for _, r := range []struct {
		suffix string
		body   map[string]any
	}{
		{"/attention", map[string]any{"state": "needs_input", "message": "?", "ask": "yes"}},
		{"/events", map[string]any{"type": "needs_input", "message": "?", "ask": 1}},
	} {
		resp, out := e.do("POST", "/api/sessions/"+id+r.suffix, tok, r.body)
		if resp.StatusCode != http.StatusBadRequest || out["error"].(map[string]any)["code"] != "invalid_request" {
			t.Fatalf("ask not a boolean on %s: %d %v", r.suffix, resp.StatusCode, out)
		}
	}
}

// A hosted session's question (#193): the server sends the mark on to the
// host with the report (ask), and its copy keeps a marked question it
// shows through a done it forwards, the host told the done; the host
// tells its attention back with the mark. A question the host has set
// aside (it tells working) gives way here to the forwarded done until the
// host tells its attention again.
func TestAHostedSessionsQuestionReachesItsHostMarked(t *testing.T) {
	e := newTestEnv(t, nil)
	host := dialFakeHost(t, e, "hosted-agent-token")
	id := host.sessionID
	report := func(body map[string]any) {
		t.Helper()
		if resp, out := e.do("POST", "/api/sessions/"+id+"/attention", "hosted-agent-token", body); resp.StatusCode != http.StatusOK {
			t.Fatalf("%v: %d %v", body, resp.StatusCode, out)
		}
	}
	showing := func(state, message string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			att := e.attentionOf(id)
			if att["state"] == state && (message == "" || att["message"] == message) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("the hosted session shows %v, want %s %q", att, state, message)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	report(map[string]any{"state": "needs_input", "message": "Which database?", "ask": true})
	if m := host.expect(proto.HostAttention); m["state"] != "needs_input" || m["message"] != "Which database?" || m["ask"] != true {
		t.Fatalf("the host was sent %v", m)
	}
	report(map[string]any{"state": "done", "message": "asked", "turn": true})
	if m := host.expect(proto.HostAttention); m["state"] != "done" || m["ask"] != nil {
		t.Fatalf("the host was sent %v", m)
	}
	showing("needs_input", "Which database?")

	// The host tells its question back, with its id and the mark: the
	// server's copy keeps it through the next forwarded done too.
	host.send(proto.HostAttentionMsg{T: proto.HostAttention, SessionID: id, State: "needs_input", Message: "Which database?", Source: session.SourceAPI, ID: "0123456789abcdef", Ask: true})
	deadline := time.Now().Add(5 * time.Second)
	for e.attentionOf(id)["id"] != "0123456789abcdef" {
		if time.Now().After(deadline) {
			t.Fatalf("the host's id never showed: %v", e.attentionOf(id))
		}
		time.Sleep(10 * time.Millisecond)
	}
	report(map[string]any{"state": "done", "message": "idle"})
	host.expect(proto.HostAttention)
	if att := e.attentionOf(id); att["state"] != "needs_input" || att["id"] != "0123456789abcdef" {
		t.Fatalf("a forwarded done replaced the host's question: %v", att)
	}

	// Set aside on the host (it tells working): the forwarded done shows
	// here until the host tells its question again.
	host.send(proto.HostAttentionMsg{T: proto.HostAttention, SessionID: id, State: "working", Source: session.SourceAPI, ID: "fedcba9876543210"})
	showing("working", "")
	report(map[string]any{"state": "done", "message": "asked"})
	host.expect(proto.HostAttention)
	showing("done", "asked")
	host.send(proto.HostAttentionMsg{T: proto.HostAttention, SessionID: id, State: "needs_input", Message: "Which database?", Source: session.SourceAPI, ID: "0123456789abcdef", Ask: true})
	showing("needs_input", "Which database?")
}
