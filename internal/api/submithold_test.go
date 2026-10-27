package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/catalog"
	"github.com/rocksolidlabs/conductor/internal/proto"
	"github.com/rocksolidlabs/conductor/internal/session"
)

// drawer is a program that keeps drawing (a dot every 100 ms, as an agent's
// spinner does) and answers each line it reads with "got:<line>". Its trust
// prompt is never drawn: with one, a submission's Enter waits for the
// screen to be quiet, which this screen never is, so the Enter comes the
// look's budget (2 s) after the text.
var drawer = catalog.Agent{
	ID: "drawer", Name: "drawer", TrustPrompt: "never drawn here",
	Command: []string{"/bin/sh", "-c", `(while :; do printf .; sleep 0.1; done) & while IFS= read -r line; do echo "got:$line"; done`},
}

// outputLog reads a viewer's frames on a goroutine of its own and keeps its
// output, and the codes of the errors it was sent.
type outputLog struct {
	mu     sync.Mutex
	out    []byte
	errors []string
}

func watchOutput(t *testing.T, w *wsClient) *outputLog {
	t.Helper()
	l := &outputLog{}
	go func() {
		for {
			f, err := w.read()
			if err != nil {
				return
			}
			l.mu.Lock()
			switch f.Type {
			case proto.TypeOutput, proto.TypeScrollback:
				l.out = append(l.out, f.Payload...)
			case proto.TypeControl:
				var m map[string]any
				if json.Unmarshal(f.Payload, &m) == nil && m["t"] == proto.CtlError {
					code, _ := m["code"].(string)
					l.errors = append(l.errors, code)
				}
			}
			l.mu.Unlock()
		}
	}()
	return l
}

// errorCodes are the codes of the errors the viewer was sent so far.
func (l *outputLog) errorCodes() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.errors...)
}

func (l *outputLog) has(needle string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return bytes.Contains(l.out, []byte(needle))
}

func (l *outputLog) waitFor(t *testing.T, needle string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !l.has(needle) {
		if time.Now().After(deadline) {
			t.Fatalf("no %q in the output within %v", needle, d)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (l *outputLog) absentFor(t *testing.T, needle string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if l.has(needle) {
			t.Fatalf("%q is in the output", needle)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitAttention waits for the session's attention to be want's state and
// words.
func waitAttention(t *testing.T, local *session.Local, state session.AttentionState, words string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		att := local.Info().Attention
		if att.State == state && att.Message == words {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("attention %+v, want %s %q", att, state, words)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A link's controller submits a line; its text is typed, and its link is
// revoked, or expires, before its Enter: the Enter is never written, and
// the session needs input with the hold's words. The owner's reply waits
// for its turn, off the connection's read loop (a second meanwhile is
// refused, too_many_requests); once the owner types into the terminal on
// the same connection (Ctrl+U, the text killed), the hold's words clear and
// the reply goes, a line of its own.
func TestALinksLineTypedBeforeTheLinkIsLetGoWaitsForAPersonsKeys(t *testing.T) {
	for _, c := range []struct {
		name string
		ttl  int
		// replyFirst: the owner's reply comes while the link's line is in
		// its look, before the hold, and waits on the read loop as the hold
		// begins.
		replyFirst bool
	}{{"revoked", 0, false}, {"expired", 1, false}, {"revoked, the reply before the hold", 0, true}} {
		t.Run(c.name, func(t *testing.T) {
			e := newTestEnvAgents(t, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), []catalog.Agent{drawer})
			id := e.createSession("drawer")
			local := e.local(id)
			owner := dialViewer(t, e, id, adminToken)
			owner.hello(0, 0)
			owner.expectControl(proto.CtlReady)
			out := watchOutput(t, owner)

			body := map[string]any{"role": "control"}
			if c.ttl > 0 {
				body["ttlSeconds"] = c.ttl
			}
			resp, lo := e.do("POST", "/api/sessions/"+id+"/links", adminToken, body)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("link: %d %v", resp.StatusCode, lo)
			}
			linkID := lo["link"].(map[string]any)["id"].(string)
			guest := dialViewer(t, e, id, lo["token"].(string))
			guest.hello(0, 0)
			guest.expectControl(proto.CtlReady)
			guest.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "first"}))
			out.waitFor(t, "first", 5*time.Second) // typed (the terminal echoes it)
			if c.replyFirst {
				// It waits for the turn the link's line holds.
				owner.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "second"}))
				time.Sleep(200 * time.Millisecond)
			}
			if c.ttl == 0 {
				if resp, _ := e.do("DELETE", "/api/sessions/"+id+"/links/"+linkID, adminToken, nil); resp.StatusCode != http.StatusNoContent {
					t.Fatalf("revoke: %d", resp.StatusCode)
				}
			} // else the link expires a second after it was made, within the Enter's look
			waitAttention(t, local, session.AttentionNeedsInput, session.HeldWords)
			out.absentFor(t, "got:", 2500*time.Millisecond)

			// The owner's reply waits for the session's turn; a second one
			// of the connection's meanwhile is refused.
			if !c.replyFirst {
				owner.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "second"}))
			}
			owner.send(proto.MustControl(proto.Submit{T: proto.CtlSubmit, Text: "third"}))
			out.absentFor(t, "second", time.Second)
			waitAttention(t, local, session.AttentionNeedsInput, session.HeldWords)
			if codes := out.errorCodes(); strings.Join(codes, ",") != proto.ErrCodeTooManyRequests {
				t.Fatalf("errors %v, want the second reply's too_many_requests", codes)
			}

			// The owner types into the terminal on the same connection: the
			// line is killed, the hold ends, and the reply is typed and
			// entered.
			owner.send(proto.Encode(proto.TypeInput, []byte{0x15}))
			waitAttention(t, local, session.AttentionNone, "")
			out.waitFor(t, "got:second", 8*time.Second)
			out.mu.Lock()
			got := string(out.out)
			out.mu.Unlock()
			if strings.Contains(got, "got:first") || strings.Contains(got, "third") {
				t.Fatalf("the let-go link's line, or the refused reply, was entered: %q", got)
			}
		})
	}
}
