package proto

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The host and the server are built apart and may differ by a release, so the
// field names of the host `activity` message are part of the contract.
func TestHostActivityMsgWireShape(t *testing.T) {
	msg := HostActivityMsg{T: HostActivity, SessionID: "0123456789abcdef", Entry: Activity{
		T: CtlActivity, At: "2026-09-29T12:00:00Z", Type: "artifact", By: "b1", ByName: "agent",
		Message: "PR opened", URL: "https://github.com/x/y/pull/1", To: "review", Tool: "gh",
	}}
	got, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"t":"activity","sessionId":"0123456789abcdef","entry":{"t":"activity","at":"2026-09-29T12:00:00Z","type":"artifact","by":"b1","byName":"agent","message":"PR opened","url":"https://github.com/x/y/pull/1","to":"review","tool":"gh"}}`
	if string(got) != want {
		t.Fatalf("host activity message\n got %s\nwant %s", got, want)
	}
	var back HostActivityMsg
	if err := json.Unmarshal(got, &back); err != nil || back != msg {
		t.Fatalf("round trip: %+v %v", back, err)
	}
}

// The server never sends a session id: the host has one session, and the
// connection says which.
func TestHostActivityMsgOmitsAnEmptySessionAndEmptyFields(t *testing.T) {
	got, err := json.Marshal(HostActivityMsg{T: HostActivity, Entry: Activity{T: CtlActivity, At: "2026-09-29T12:00:00Z", Type: "progress"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"t":"activity","entry":{"t":"activity","at":"2026-09-29T12:00:00Z","type":"progress"}}`
	if string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

// The register message carries the host's instance and the session's local
// id; registered always carries its links, [] when none, and a reply
// without the field (an older server) reads as nil.
func TestRegisterWireShapeCarriesInstanceAndLocalID(t *testing.T) {
	b, _ := json.Marshal(Register{T: HostRegister, Proto: ProtoVersion, Host: HostInfo{Name: "laptop", Instance: "secret"}, Session: HostSession{Command: []string{"bash"}, LocalID: "s1"}})
	if !strings.Contains(string(b), `"instance":"secret"`) || !strings.Contains(string(b), `"localId":"s1"`) {
		t.Fatalf("register %s", b)
	}
	b, _ = json.Marshal(Registered{T: HostRegistered, Links: []string{}})
	if !strings.Contains(string(b), `"links":[]`) {
		t.Fatalf("registered %s", b)
	}
	var old Registered
	if err := json.Unmarshal([]byte(`{"t":"registered","sessionId":"x"}`), &old); err != nil || old.Links != nil {
		t.Fatalf("an older server's registered: %+v %v", old, err)
	}
}

// A run group is checked against its bounds, and a full one fits a host message.
func TestRunGroupValidateBounds(t *testing.T) {
	member := func(i int) RunMember {
		return RunMember{Name: fmt.Sprintf("m%02d", i), AgentID: "claude", Status: "running", SessionID: strings.Repeat("a", 16)}
	}
	g := RunGroup{ID: strings.Repeat("r", MaxRunID), Name: strings.Repeat("n", MaxRunName)}
	for i := range MaxRunLinkMembers {
		g.Members = append(g.Members, member(i))
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("a full group: %v", err)
	}
	b, _ := json.Marshal(HostRunLinkMsg{T: HostRunLink, RequestID: "r", Role: "view", Label: strings.Repeat("l", MaxLinkLabel), Run: g})
	if len(b) > MaxHostMessage/4 {
		t.Fatalf("a full link_run is %d bytes", len(b))
	}
	bad := []func(*RunGroup){
		func(g *RunGroup) { g.Members = append(g.Members, member(99)) },
		func(g *RunGroup) { g.Members[0].Status = "dancing" },
		func(g *RunGroup) { g.Members[0].Name = strings.Repeat("x", MaxRunMemberName+1) },
		func(g *RunGroup) { g.Members[1].Name = g.Members[0].Name },
		func(g *RunGroup) { g.ID = strings.Repeat("r", MaxRunID+1) },
		func(g *RunGroup) { g.Members = nil },
	}
	for i, mutate := range bad {
		c := g
		c.Members = append([]RunMember(nil), g.Members...)
		mutate(&c)
		if c.Validate() == nil {
			t.Fatalf("bad group %d passed", i)
		}
	}
}

func TestHostRunLinkMsgWireShape(t *testing.T) {
	b, _ := json.Marshal(HostRunLinkMsg{T: HostRunLink, RequestID: "r", Role: "view", Run: RunGroup{ID: "api-1", Name: "api", Members: []RunMember{{Name: "lead", AgentID: "claude", Status: "pending"}}}})
	if !strings.Contains(string(b), `"t":"link_run"`) || !strings.Contains(string(b), `"members":[{"name":"lead","agentId":"claude","status":"pending"}]`) {
		t.Fatalf("link_run %s", b)
	}
	b, _ = json.Marshal(LinkCreated{T: HostLinkCreated, RunID: "api-1"})
	if !strings.Contains(string(b), `"runId":"api-1"`) {
		t.Fatalf("link_created %s", b)
	}
}
