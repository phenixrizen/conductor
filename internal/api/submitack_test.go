package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// A submit naming a request over the wire (#322): the welcome says the
// owner answers it; a line into a running session is entered and told
// submit_ok; one with the unless-waiting guard into a session that asks is
// held back and told not_sent with the question's words, nothing typed;
// the same line without the guard is typed into a prompt the API flagged;
// one naming no request is typed and told nothing; one past its bounds is
// bad_frame; a view link's is read_only, named.
func TestSubmitAcknowledgedOverTheWire(t *testing.T) {
	e := newTestEnv(t, nil)
	id := e.createSession("cat")
	l := e.local(id)
	a := dialViewer(t, e, id, adminToken)
	a.hello(0, 0)
	if w := a.expectControl(proto.CtlWelcome); w["submitAck"] != true {
		t.Fatalf("welcome %v", w)
	}
	a.expectControl(proto.CtlReady)
	watch := dialViewer(t, e, id, adminToken)
	watch.hello(0, 0)
	watch.expectControl(proto.CtlReady)
	submit := func(reqID, text string, unless bool) []byte {
		return proto.MustControl(proto.Submit{T: proto.CtlSubmit, ReqID: reqID, Text: text, UnlessWaiting: unless})
	}
	a.send(submit("s1", "looks good", true))
	if m := a.verdict("s1"); m["t"] != proto.CtlSubmitOK {
		t.Fatalf("into a running session: %v", m)
	}
	watch.outputUntil("looks good")

	// The session asks (as the API flags it): the guard holds the line and says so; nothing reaches cat.
	l.SetAttentionFull(session.AttentionNeedsInput, "Which database?", session.SourceAPI, session.KindPrompt, nil)
	a.send(submit("s2", "SQLite", true))
	if m := a.verdict("s2"); m["code"] != proto.ErrCodeNotSent || m["message"] != session.QuestionWords {
		t.Fatalf("held back: %v", m)
	}
	// Without the guard the same line goes in (the API's flag is not the program's own question).
	a.send(submit("s3", "SQLite", false))
	if m := a.verdict("s3"); m["t"] != proto.CtlSubmitOK {
		t.Fatalf("without the guard: %v", m)
	}
	watch.outputUntil("SQLite")
	l.SetAttention(session.AttentionNone, "", "")

	// An older client's submit: typed, nothing said; the next word is the next request's own.
	a.send(submit("", "plain", false))
	watch.outputUntil("plain")
	a.send(submit("s4", "after", false))
	if m := a.verdict("s4"); m["t"] != proto.CtlSubmitOK {
		t.Fatalf("after an unnamed one: %v", m)
	}
	watch.outputUntil("after")

	// Past its bounds: the text, named; the request id itself, unnamed.
	a.send(submit("s5", strings.Repeat("x", proto.MaxSubmit+1), false))
	if m := a.verdict("s5"); m["code"] != proto.ErrCodeBadFrame {
		t.Fatalf("a long line: %v", m)
	}
	a.send(submit(strings.Repeat("r", proto.MaxAnswerReqID+1), "x", false))
	if m := a.expectControl(proto.CtlError); m["code"] != proto.ErrCodeBadFrame || m["requestId"] != nil {
		t.Fatalf("a long request id: %v", m)
	}

	// A view link's line: read_only, named.
	resp, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("link %d %v", resp.StatusCode, lo)
	}
	v := dialViewer(t, e, id, lo["token"].(string))
	v.hello(0, 0)
	v.expectControl(proto.CtlReady)
	v.send(submit("v1", "x", false))
	if m := v.verdict("v1"); m["code"] != proto.ErrCodeReadOnly {
		t.Fatalf("a view link's submit: %v", m)
	}
}
