package proto

import (
	"encoding/json"
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
