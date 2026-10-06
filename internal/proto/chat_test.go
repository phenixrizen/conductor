package proto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// A chat message on the wire: every field in its place, the empty ones left
// out, and the same message back from its JSON.
func TestChatMessageWireShape(t *testing.T) {
	msg := ChatMessage{T: CtlChat, ID: "0123456789abcdef", At: "2026-10-06T08:32:40Z", Scope: ChatScopeSession, Kind: ChatKindMessage,
		By: ChatBy{ID: "fedcba9876543210", Name: "Nate", Role: "control"}, Text: "Yes, trust this folder", Nonce: "n1"}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"t":"chat","id":"0123456789abcdef","at":"2026-10-06T08:32:40Z","scope":"session","kind":"message","by":{"id":"fedcba9876543210","name":"Nate","role":"control"},"text":"Yes, trust this folder","nonce":"n1"}`
	if string(b) != want {
		t.Fatalf("wire shape:\n%s\n%s", b, want)
	}
	var back ChatMessage
	if err := json.Unmarshal(b, &back); err != nil || back != msg {
		t.Fatalf("round trip: %v %+v", err, back)
	}
	marker := ChatMessage{T: CtlChat, ID: "1", At: "2026-10-06T08:32:41Z", Scope: ChatScopeSession, Kind: ChatKindSentToAgent, By: msg.By, Ref: msg.ID}
	b, _ = json.Marshal(marker)
	if string(b) != `{"t":"chat","id":"1","at":"2026-10-06T08:32:41Z","scope":"session","kind":"sent_to_agent","by":{"id":"fedcba9876543210","name":"Nate","role":"control"},"ref":"0123456789abcdef"}` {
		t.Fatalf("marker: %s", b)
	}
	system := ChatMessage{T: CtlChat, ID: "2", At: "2026-10-06T08:31:00Z", Scope: ChatScopeSession, Kind: ChatKindSystem, By: ChatBy{ID: "x", Name: "Jane", Role: "control"}, Event: "join"}
	b, _ = json.Marshal(system)
	if !strings.Contains(string(b), `"event":"join"`) || strings.Contains(string(b), `"text"`) {
		t.Fatalf("system line: %s", b)
	}
}

// A post and a send, as the client writes them: the owner reads a scope of
// its own when none is given.
func TestChatPostAndSendShapes(t *testing.T) {
	var post ChatPost
	if err := json.Unmarshal([]byte(`{"t":"chat","text":"hi","nonce":"a"}`), &post); err != nil || post.Text != "hi" || post.Scope != "" || post.To != "" {
		t.Fatalf("post: %v %+v", err, post)
	}
	b, _ := json.Marshal(ChatPost{T: CtlChat, Text: "x", To: ChatToAgent})
	if string(b) != `{"t":"chat","text":"x","to":"agent"}` {
		t.Fatalf("post wire: %s", b)
	}
	b, _ = json.Marshal(ChatSend{T: CtlChatSend, Ref: "r"})
	if string(b) != `{"t":"chat_send","ref":"r"}` {
		t.Fatalf("send wire: %s", b)
	}
	b, _ = json.Marshal(ChatHistory{T: CtlChatHistory, Scope: ChatScopeSession, Messages: []ChatMessage{}, More: true})
	if string(b) != `{"t":"chat_history","scope":"session","messages":[],"more":true}` {
		t.Fatalf("history wire: %s", b)
	}
}

// The frame of a message of MaxChatText bytes fits MaxControl whatever the
// text: the raw encoder keeps `<` one byte (the HTML-escaping one makes it
// six, past the frame), and the quote, the longest raw escape, doubles only.
func TestChatFramesFitWithoutHTMLEscaping(t *testing.T) {
	by := ChatBy{ID: strings.Repeat("f", 16), Name: strings.Repeat("N", 40), Role: "control"}
	for _, text := range []string{strings.Repeat("<", MaxChatText), strings.Repeat(`"`, MaxChatText), strings.Repeat(" ", MaxChatText/3), strings.Repeat("é", MaxChatText/2)} {
		msg := ChatMessage{T: CtlChat, ID: strings.Repeat("0", 16), At: "2026-10-06T08:32:40.123456789Z", Scope: ChatScopeSession, Kind: ChatKindMessage, By: by, Text: text, Nonce: strings.Repeat("n", MaxChatNonce), On: strings.Repeat("m", 40)}
		raw := MustControlRaw(msg)
		if len(raw) > MaxControl {
			t.Fatalf("raw frame of %q…: %d bytes", text[:1], len(raw))
		}
		f, err := Decode(raw)
		if err != nil || f.Type != TypeControl {
			t.Fatalf("decode: %v", err)
		}
		var back ChatMessage
		if err := json.Unmarshal(f.Payload, &back); err != nil || back.Text != text {
			t.Fatalf("back: %v", err)
		}
	}
	angled := MustControlRaw(ChatMessage{T: CtlChat, Text: "<b>&</b>"})
	if !bytes.Contains(angled, []byte("<b>&</b>")) {
		t.Fatalf("raw kept no angle brackets: %s", angled)
	}
	escaped := MustControl(ChatMessage{T: CtlChat, Text: strings.Repeat("<", MaxChatText)})
	if len(escaped) <= MaxControl {
		t.Fatalf("the escaping encoder would have fitted too (%d bytes): the raw one is not needed", len(escaped))
	}
}

// A run's roster names each person once with the member they look at; the
// list stops at MaxChatRoster while the count goes on, and a full list of
// the longest names fits the control frame.
func TestChatRosterShapeAndBound(t *testing.T) {
	r := ChatRoster{T: CtlChatRoster, Scope: ChatScopeRun, Count: 2, List: []ChatPerson{{ID: "s1", Name: "Nate", Role: "control", On: "core"}, {ID: "s2", Name: "Priya", Role: "view"}}}
	want := `{"t":"chat_roster","scope":"run","count":2,"list":[{"id":"s1","name":"Nate","role":"control","on":"core"},{"id":"s2","name":"Priya","role":"view"}]}`
	f, err := Decode(MustControl(r))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(f.Payload); got != want {
		t.Fatalf("roster\n got %s\nwant %s", got, want)
	}
	full := ChatRoster{T: CtlChatRoster, Scope: ChatScopeRun, Count: 1000}
	for i := range MaxChatRoster {
		full.List = append(full.List, ChatPerson{ID: fmt.Sprintf("%032x", i), Name: strings.Repeat("名", MaxNameLen), Role: "control", On: strings.Repeat("m", 40)})
	}
	if n := len(MustControl(full)); n > MaxControl {
		t.Fatalf("a full roster is %d bytes, over %d", n, MaxControl)
	}
}
