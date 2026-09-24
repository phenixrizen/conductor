package hostagent

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
)

// TestPeerDataChannelLoopback runs the real host peer code against an
// in-process browser-like pion peer: the viewer creates the data channel and
// the offer, the host answers, ICE trickles through channels.
func TestPeerDataChannelLoopback(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "big.txt"), bytes.Repeat([]byte("k"), 200<<10), 0o600)
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: dir, Env: hostEnv(nil)})
	if err != nil {
		t.Fatal(err)
	}
	local := session.NewLocal(session.Info{ID: "s", Cwd: dir, Cols: 80, Rows: 24}, proc, session.Options{})
	t.Cleanup(func() { proc.Stop(t.Context(), time.Second) })

	out := make(chan any, 64)
	a := &agent{opts: Options{}, local: local, proc: proc, peers: map[string]*peer{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.sendHook = func(v any) { out <- v }

	p := newPeer(a, "0123456789abcdef", session.RoleControl, "")
	if err := p.startWebRTC(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)

	viewerPC, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { viewerPC.Close() })
	frames := make(chan []byte, 1024)
	dc, err := viewerPC.CreateDataChannel("term", nil)
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	dc.OnOpen(func() { close(opened) })
	dc.OnMessage(func(m webrtc.DataChannelMessage) { frames <- m.Data })
	viewerPC.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		p.handleICE(proto.ICECandidate{Candidate: init.Candidate, SDPMid: init.SDPMid, SDPMLineIndex: init.SDPMLineIndex})
	})
	offer, err := viewerPC.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := viewerPC.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	if err := p.handleOffer(offer.SDP); err != nil {
		t.Fatal(err)
	}
	// Pump host signaling back into the viewer peer.
	go func() {
		for v := range out {
			switch m := v.(type) {
			case proto.ViewerSDP:
				viewerPC.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: m.SDP})
			case proto.ViewerICE:
				viewerPC.AddICECandidate(webrtc.ICECandidateInit{Candidate: m.Candidate.Candidate, SDPMid: m.Candidate.SDPMid, SDPMLineIndex: m.Candidate.SDPMLineIndex})
			}
		}
	}()
	select {
	case <-opened:
	case <-time.After(15 * time.Second):
		t.Fatal("data channel did not open")
	}
	dc.Send(proto.MustControl(proto.Hello{T: proto.CtlHello, Proto: 1, Cols: 100, Rows: 40}))
	next := func() proto.Frame {
		t.Helper()
		select {
		case raw := <-frames:
			f, err := proto.Decode(raw)
			if err != nil {
				t.Fatal(err)
			}
			return f
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for frame")
		}
		return proto.Frame{}
	}
	control := func(f proto.Frame) map[string]any {
		var m map[string]any
		json.Unmarshal(f.Payload, &m)
		return m
	}
	if m := control(next()); m["t"] != proto.CtlWelcome || m["transport"] != "webrtc" || m["cols"] != float64(100) {
		t.Fatalf("welcome %v", m)
	}
	var seenReady bool
	for !seenReady {
		f := next()
		if f.Type == proto.TypeControl && control(f)["t"] == proto.CtlReady {
			seenReady = true
		}
	}
	dc.Send(proto.Encode(proto.TypeInput, []byte("over webrtc\n")))
	var acc []byte
	for !bytes.Contains(acc, []byte("over webrtc")) {
		f := next()
		if f.Type == proto.TypeOutput {
			acc = append(acc, f.Payload...)
		}
	}
	// A 200 KiB file arrives fragmented and reassembles.
	dc.Send(proto.MustControl(proto.FileGet{T: proto.CtlFileGet, ReqID: "big", Path: "big.txt"}))
	assembled := map[uint16][]byte{}
	var got []byte
	for got == nil {
		f := next()
		switch f.Type {
		case proto.TypeChunk:
			id, total, off, data, err := proto.ChunkInfo(f.Payload)
			if err != nil {
				t.Fatal(err)
			}
			buf := assembled[id]
			if buf == nil {
				buf = make([]byte, total)
				assembled[id] = buf
			}
			copy(buf[off:], data)
			if int(off)+len(data) == int(total) {
				got = buf
			}
		case proto.TypeFile:
			got = proto.Encode(proto.TypeFile, f.Payload)
		}
	}
	full, err := proto.Decode(got)
	if err != nil || full.Type != proto.TypeFile {
		t.Fatalf("reassembled frame: %v", err)
	}
	h, body, err := proto.DecodeFile(full.Payload)
	if err != nil || h.ReqID != "big" || len(body) != 200<<10 {
		t.Fatalf("file %+v %d %v", h, len(body), err)
	}
}
