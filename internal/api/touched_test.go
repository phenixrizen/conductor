package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/rocksolidlabs/conductor/internal/proto"
	"github.com/rocksolidlabs/conductor/internal/session"
)

// getTouched reads GET /api/touched with the query, decoded.
func getTouched(t *testing.T, e *testEnv, query string) (int, touchedReply) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.http.URL+"/api/touched"+query, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out touchedReply
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode %s: %v", data, err)
		}
	}
	return resp.StatusCode, out
}

// The index across sessions (#304): admin only; the listed sessions in the
// list's order, each with the files its agent touched, the most recently
// touched first, a session with nothing touched left out; the per-session
// limit cuts a session and marks it, a bad limit is refused; the deny list
// leaves a file out.
func TestTouchedIndexAcrossSessions(t *testing.T) {
	e := newTestEnv(t, nil)
	if resp, out := e.do("GET", "/api/touched", "", nil); resp.StatusCode != http.StatusUnauthorized || out["error"].(map[string]any)["code"] != "unauthorized" {
		t.Fatalf("without the token: %d %v", resp.StatusCode, out)
	}
	first := e.createSession("cat")
	second := e.createSession("cat")
	e.createSession("cat") // nothing touched: left out
	event := func(id string, body map[string]any) {
		t.Helper()
		if resp, out := e.do("POST", "/api/sessions/"+id+"/events", adminToken, body); resp.StatusCode >= 300 {
			t.Fatalf("event %v: %d %v", body, resp.StatusCode, out)
		}
	}
	event(first, map[string]any{"type": "file", "op": "read", "path": "README.md", "tool": "Read"})
	time.Sleep(5 * time.Millisecond)
	event(first, map[string]any{"type": "file", "op": "edit", "path": "internal/api/users.go", "tool": "Edit"})
	event(second, map[string]any{"type": "file", "op": "write", "path": filepath.Join(e.root, "notes.md"), "tool": "Write"})
	// A path the deny list refuses (the data directory) is in the session's log and not in the index.
	event(second, map[string]any{"type": "file", "op": "read", "path": filepath.Join(e.srv.cfg.DataDir, "store.db"), "tool": "Read"})

	status, out := getTouched(t, e, "")
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if len(out.Sessions) != 2 || out.Truncated {
		t.Fatalf("sessions %+v truncated %v", out.Sessions, out.Truncated)
	}
	// The list's order: the newest session first.
	if out.Sessions[0].ID != second || out.Sessions[1].ID != first {
		t.Fatalf("order %s, %s; wanted %s, %s", out.Sessions[0].ID, out.Sessions[1].ID, second, first)
	}
	s2 := out.Sessions[0]
	if s2.Kind != "server" || s2.Status != "running" || s2.Cwd != e.root || s2.AgentID != "cat" || s2.Name == "" {
		t.Fatalf("the session's head: %+v", s2)
	}
	if len(s2.Touched) != 1 || s2.Touched[0].Path != filepath.Join(e.root, "notes.md") || s2.Touched[0].Op != "write" || s2.Touched[0].Tool != "Write" || s2.Truncated {
		t.Fatalf("the denied file must be left out: %+v", s2.Touched)
	}
	s1 := out.Sessions[1]
	if len(s1.Touched) != 2 || s1.Touched[0].Path != filepath.Join(e.root, "internal/api/users.go") || s1.Touched[1].Path != filepath.Join(e.root, "README.md") {
		t.Fatalf("newest first, absolute: %+v", s1.Touched)
	}
	if s1.Touched[0].Count != 1 || s1.Touched[0].First == "" || s1.Touched[0].Last == "" || len(s1.Touched[0].Ops) != 1 {
		t.Fatalf("the file's fields: %+v", s1.Touched[0])
	}

	// The per-session limit: the newest files kept, the session marked, the reply not.
	status, out = getTouched(t, e, "?limit=1")
	if status != http.StatusOK || len(out.Sessions) != 2 || out.Truncated {
		t.Fatalf("limit=1: %d %+v", status, out)
	}
	if got := out.Sessions[1]; len(got.Touched) != 1 || got.Touched[0].Path != filepath.Join(e.root, "internal/api/users.go") || !got.Truncated {
		t.Fatalf("limit=1 cuts the session to its newest file: %+v", got)
	}
	if got := out.Sessions[0]; got.Truncated {
		t.Fatalf("a session within the limit is not marked: %+v", got)
	}
	for _, q := range []string{"?limit=0", "?limit=-3", "?limit=x"} {
		if status, _ := getTouched(t, e, q); status != http.StatusBadRequest {
			t.Fatalf("%s: %d", q, status)
		}
	}
	// A limit past the index's cap is the cap.
	if status, out := getTouched(t, e, "?limit=99999"); status != http.StatusOK || len(out.Sessions) != 2 {
		t.Fatalf("limit past the cap: %d %+v", status, out)
	}
}

// The reply's size: the sessions are filled in order as far as the budget
// goes, the one that would pass it cut short and marked, with the reply.
func TestTouchedIndexReplyBudget(t *testing.T) {
	e := newTestEnv(t, nil)
	a := e.createSession("cat")
	b := e.createSession("cat")
	for _, id := range []string{a, b} {
		for _, p := range []string{"one.go", "two.go", "three.go"} {
			if resp, out := e.do("POST", "/api/sessions/"+id+"/events", adminToken, map[string]any{"type": "file", "op": "edit", "path": p, "tool": "Edit"}); resp.StatusCode >= 300 {
				t.Fatalf("event: %d %v", resp.StatusCode, out)
			}
		}
	}
	full := e.srv.touchedIndex(touchedLimitDefault, touchedReplyMax)
	if len(full.Sessions) != 2 || len(full.Sessions[0].Touched) != 3 || len(full.Sessions[1].Touched) != 3 || full.Truncated {
		t.Fatalf("full %+v", full)
	}
	whole, _ := json.Marshal(full)
	// A budget short of the whole: the second session loses files and both marks are set; its size stays within the budget's reach.
	part := e.srv.touchedIndex(touchedLimitDefault, len(whole)-40)
	if !part.Truncated || len(part.Sessions) != 2 || !part.Sessions[1].Truncated || len(part.Sessions[1].Touched) >= 3 || part.Sessions[0].Truncated {
		t.Fatalf("part %+v", part)
	}
	if b, _ := json.Marshal(part); len(b) > len(whole)-40+64 {
		t.Fatalf("the part's size %d is not held to the budget %d", len(b), len(whole)-40)
	}
	// A budget below one session's head: nothing listed, the reply marked.
	none := e.srv.touchedIndex(touchedLimitDefault, 1)
	if !none.Truncated || len(none.Sessions) != 0 {
		t.Fatalf("none %+v", none)
	}
	// The order and the fields hold through the budget: the first session is whole.
	if part.Sessions[0].ID != full.Sessions[0].ID || len(part.Sessions[0].Touched) != 3 {
		t.Fatalf("the first session is whole: %+v", part.Sessions[0])
	}
}

// A hosted session keeps no activity log on the server, but the file entries
// its host reports feed an index of its own, listed like a server session's,
// the paths made absolute under the session's working directory.
func TestTouchedIndexListsHostedSessions(t *testing.T) {
	e := newTestEnv(t, nil)
	host := dialFakeHost(t, e, "hosted-agent-token")
	host.send(hostActivity(session.ActivityFile, func(a *proto.Activity) { a.Op = "edit"; a.Path = "lib/x.go"; a.Tool = "Edit"; a.ByName = "codex" }))
	host.send(hostActivity(session.ActivityFile, func(a *proto.Activity) { a.Op = "burn"; a.Path = "lib/y.go" })) // an op this server does not know: no touch
	host.send(hostActivity(session.ActivityProgress, func(a *proto.Activity) { a.Message = "not a file entry" })) // no touch
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, out := getTouched(t, e, "")
		if status != http.StatusOK {
			t.Fatalf("status %d", status)
		}
		var hosted *touchedSession
		for i := range out.Sessions {
			if out.Sessions[i].ID == host.sessionID {
				hosted = &out.Sessions[i]
			}
		}
		if hosted != nil {
			if hosted.Kind != "hosted" || len(hosted.Touched) != 1 || hosted.Touched[0].Path != filepath.Join(e.root, "lib/x.go") || hosted.Touched[0].Op != "edit" || hosted.Touched[0].Tool != "Edit" || hosted.Touched[0].By != "codex" {
				t.Fatalf("hosted %+v", hosted)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the hosted session's file never listed: %+v", out.Sessions)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
