package hostagent

import (
	"encoding/json"
	"os"
	"path/filepath"
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
// connectPaste answers a viewer's pasted offer for a cat session in a
// directory of the test's, with role, and returns the pasted peer once it
// sees the viewer's data channel, the channel, what the viewer receives on
// it, and the session's directory.
func connectPaste(t *testing.T, role session.Role) (*PastePeer, *webrtc.DataChannel, chan []byte, string) {
	t.Helper()
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
	pp, answerSDP, err := AnswerPaste(t.Context(), local, offerSDP, PasteOptions{Role: role, Name: "guest"})
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
	// The viewer's side opens as it sends the channel's open; the peer
	// sees the channel once that open has reached it.
	for seen := time.Now().Add(10 * time.Second); !pp.Connected(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(seen) {
			t.Fatal("the peer does not see the channel")
		}
	}
	return pp, dc, frames, dir
}

func TestAnswerPasteConnectsAViewerWithNoSignaling(t *testing.T) {
	pp, dc, frames, _ := connectPaste(t, session.RoleControl)
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

// A save part a paste invite's viewer sends that cannot be read is a
// protocol error there too, with no server to let the viewer go: it is told
// bad_frame on its data channel, the channel is closed, and a save under way
// is dropped with it, nothing written.
func TestAMalformedSavePartClosesAPastedViewer(t *testing.T) {
	pp, dc, frames, dir := connectPaste(t, session.RoleControl)
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	dc.OnClose(func() { close(closed) })
	dc.Send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 100, Rows: 40, Name: "guest"}))
	control := func(want string) map[string]any {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case raw := <-frames:
				f, err := proto.Decode(raw)
				if err != nil || f.Type != proto.TypeControl {
					continue
				}
				var m map[string]any
				if json.Unmarshal(f.Payload, &m) == nil && m["t"] == want {
					return m
				}
			case <-deadline:
				t.Fatalf("no %s", want)
			}
		}
	}
	if w := control(proto.CtlWelcome); w["fileEdit"] != true {
		t.Fatalf("welcome %v", w)
	}
	// A save under way: the first of two parts.
	first, err := proto.EncodeFileWrite(proto.FileWrite{ReqID: "w1", Path: "notes.txt", Total: 12, Force: true}, []byte("alpha\n"))
	if err != nil {
		t.Fatal(err)
	}
	dc.Send(first)
	// Then a part that cannot be read: a header said to be 5 KiB long.
	part := make([]byte, 20)
	copy(part[8:12], []byte{0, 0, 0x14, 0})
	dc.Send(proto.Encode(proto.TypeFileWrite, part))
	if m := control(proto.CtlError); m["code"] != proto.ErrCodeBadFrame || m["message"] != "malformed file write" {
		t.Fatalf("the viewer was told %v", m)
	}
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the data channel stayed open")
	}
	if pp.Connected() {
		t.Fatal("the peer still holds the channel")
	}
	hasBody(t, file, "alpha\n")
}
