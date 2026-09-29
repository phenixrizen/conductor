package proto

import (
	"encoding/json"
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
