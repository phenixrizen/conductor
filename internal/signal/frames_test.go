package signal

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/proto"
)

// A signaling message encoded anew past what a SIGNAL frame holds is never
// made: the frame a reader would refuse (proto.Decode, 64 KiB) is refused
// here with proto.ErrTooLarge, at the very byte, and one within it is a
// frame any reader takes. The largest ICE candidate the server accepts,
// all '<' (six bytes each encoded), stays far within. The error a viewer
// is sent in place of a refused frame is a CONTROL error naming what was
// not sent and why.
func TestSignalFramesStayWithinWhatAReaderTakes(t *testing.T) {
	f, err := AnswerFrame(strings.Repeat("<", 10<<10))
	if err != nil {
		t.Fatal(err)
	}
	if d, err := proto.Decode(f); err != nil || d.Type != proto.TypeSignal || len(d.Payload) != 60<<10+len(`{"t":"answer","sdp":""}`) {
		t.Fatalf("10 KiB of '<' encoded: %d bytes, %v", len(f), err)
	}
	_, err = AnswerFrame(strings.Repeat("<", 11<<10))
	if !errors.Is(err, proto.ErrTooLarge) {
		t.Fatalf("11 KiB of '<' encoded (66 KiB): %v", err)
	}
	// At the limit, and one byte past it.
	room := proto.MaxSignal - len(`{"t":"answer","sdp":""}`)
	if f, err := AnswerFrame(strings.Repeat("a", room)); err != nil || len(f)-1 != proto.MaxSignal {
		t.Fatalf("an answer filling the frame: %d bytes, %v", len(f), err)
	}
	if _, err := AnswerFrame(strings.Repeat("a", room+1)); !errors.Is(err, proto.ErrTooLarge) {
		t.Fatalf("an answer one byte past the frame: %v", err)
	}
	c := proto.ICECandidate{Candidate: strings.Repeat("<", proto.MaxICECandidate)}
	if f, err := ICEFrame(c); err != nil || len(f)-1 > proto.MaxSignal/4 {
		t.Fatalf("the largest candidate of '<': %d bytes, %v", len(f), err)
	}

	e := RefusedFrame("answer", err)
	d, derr := proto.Decode(e)
	if derr != nil || d.Type != proto.TypeControl {
		t.Fatalf("the refusal: %v %v", d, derr)
	}
	var m proto.ErrorMsg
	if json.Unmarshal(d.Payload, &m) != nil || m.T != proto.CtlError || m.Code != proto.ErrCodeBadFrame {
		t.Fatalf("the refusal: %s", d.Payload)
	}
	for _, want := range []string{"answer", "not sent", "exceeds limit"} {
		if !strings.Contains(m.Message, want) {
			t.Fatalf("the refusal's message %q says nothing of %q", m.Message, want)
		}
	}
}
