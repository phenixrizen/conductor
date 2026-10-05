package hostagent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/paste"
	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
)

// A viewer's offer with every candidate gathered gets an answer with every
// candidate gathered; the viewer pastes it and the data channel opens, with
// the hello and the welcome over it as with a host's peer.
func TestAnswerPasteConnectsAViewerWithNoSignaling(t *testing.T) {
	dir := t.TempDir()
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: dir, Env: hostEnv(nil)})
	if err != nil {
		t.Fatal(err)
	}
	local := session.NewLocal(session.Info{ID: "s", Name: "pasted", Cwd: dir, Cols: 80, Rows: 24}, proc, session.Options{})
	t.Cleanup(func() { proc.Stop(t.Context(), time.Second) })

	viewer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { viewer.Close() })
	dc, err := viewer.CreateDataChannel("term", nil)
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	frames := make(chan []byte, 64)
	dc.OnOpen(func() { close(opened) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) { frames <- m.Data })
	offer, err := viewer.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(viewer)
	if err := viewer.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered
	offerBlob, _ := paste.EncodeBlob("offer", viewer.LocalDescription().SDP)

	offerSDP, err := paste.DecodeBlob(offerBlob, "offer")
	if err != nil {
		t.Fatal(err)
	}
	pp, answerSDP, err := AnswerPaste(t.Context(), local, offerSDP, PasteOptions{Role: session.RoleControl, Name: "guest"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pp.Close)
	if !strings.Contains(answerSDP, "a=candidate:") || !strings.Contains(answerSDP, "a=end-of-candidates") {
		t.Fatalf("the answer has no gathered candidates:\n%s", answerSDP)
	}
	answerBlob, _ := paste.EncodeBlob("answer", answerSDP)
	sdp, err := paste.DecodeBlob(answerBlob, "answer")
	if err != nil {
		t.Fatal(err)
	}
	if err := viewer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-opened:
	case <-time.After(15 * time.Second):
		t.Fatal("the data channel did not open")
	}
	if !pp.Connected() {
		t.Fatal("the peer does not see the channel")
	}
	dc.Send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 100, Rows: 40, Name: "guest"}))
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-frames:
			f, err := proto.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			if f.Type != proto.TypeControl {
				continue
			}
			var m map[string]any
			json.Unmarshal(f.Payload, &m)
			if m["t"] == proto.CtlWelcome {
				if m["transport"] != "webrtc" || m["role"] != "control" {
					t.Fatalf("welcome %v", m)
				}
				pp.Close()
				return
			}
		case <-deadline:
			t.Fatal("no welcome")
		}
	}
}

// A bad offer and a bad role are refused before anything is kept.
func TestAnswerPasteRefusesWhatItCannotAnswer(t *testing.T) {
	dir := t.TempDir()
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: dir, Env: hostEnv(nil)})
	if err != nil {
		t.Fatal(err)
	}
	local := session.NewLocal(session.Info{ID: "s", Cwd: dir, Cols: 80, Rows: 24}, proc, session.Options{})
	t.Cleanup(func() { proc.Stop(t.Context(), time.Second) })
	if _, _, err := AnswerPaste(t.Context(), local, "v=0 not an offer", PasteOptions{Role: session.RoleView}); err == nil {
		t.Fatal("a bad offer was answered")
	}
	if _, _, err := AnswerPaste(t.Context(), local, "v=0", PasteOptions{Role: "admin"}); err == nil || !strings.Contains(err.Error(), "role") {
		t.Fatalf("a bad role: %v", err)
	}
}
