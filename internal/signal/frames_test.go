package signal

import (
	"errors"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/proto"
)

// A signaling message encoded anew past what a SIGNAL frame holds is never
// made: the frame a reader would refuse (proto.Decode, 64 KiB) is refused
// here with proto.ErrTooLarge, at the very byte, and one within it is a
// frame any reader takes. The largest ICE candidate the server accepts,
// all '<' (six bytes each encoded), stays far within. What a viewer is
// told in place of a refused frame names what was not sent and why.
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

	msg := RefusedMessage("answer", err)
	for _, want := range []string{"answer", "not sent", "exceeds limit", "65536"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal %q says nothing of %q", msg, want)
		}
	}
}
