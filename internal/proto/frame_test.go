package proto

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	f, err := Decode(Encode(TypeOutput, []byte("hi")))
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != TypeOutput || string(f.Payload) != "hi" {
		t.Fatalf("unexpected frame %+v", f)
	}
}

func TestDecodeRejects(t *testing.T) {
	if _, err := Decode(nil); !errors.Is(err, ErrEmptyFrame) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := Decode([]byte{0x77}); !errors.Is(err, ErrUnknownType) {
		t.Fatalf("unknown: %v", err)
	}
	big := make([]byte, MaxInput+2)
	big[0] = TypeInput
	if _, err := Decode(big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	if _, err := Decode(make([]byte, MaxControl+2)); err == nil {
		t.Fatal("expected error for oversized control")
	}
}

func TestRelayEnvelope(t *testing.T) {
	inner := Encode(TypeInput, []byte("x"))
	env, err := EncodeRelay("0123456789abcdef", inner)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Decode(env)
	if err != nil {
		t.Fatal(err)
	}
	id, in, err := DecodeRelay(f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if id != "0123456789abcdef" || in.Type != TypeInput || string(in.Payload) != "x" {
		t.Fatalf("bad decode: %q %+v", id, in)
	}
	if _, err := EncodeRelay("short", inner); err == nil {
		t.Fatal("expected short id error")
	}
	if _, _, err := DecodeRelay(f.Payload[:5]); !errors.Is(err, ErrBadEnvelope) {
		t.Fatalf("short payload: %v", err)
	}
	nested, _ := EncodeRelay("0123456789abcdef", env)
	if _, _, err := DecodeRelay(nested[1:]); err == nil {
		t.Fatal("nested relay must be rejected")
	}
	sig, _ := EncodeRelay("0123456789abcdef", Encode(TypeSignal, []byte("{}")))
	if _, _, err := DecodeRelay(sig[1:]); err == nil {
		t.Fatal("signal must not be relayable")
	}
	// A file save relays like a file read: the editor saves over the relay
	// on a connection that is not offered Neovim.
	fw, _ := EncodeFileWrite(FileWrite{ReqID: "w1", Path: "a.txt", Total: 1}, []byte("x"))
	wenv, _ := EncodeRelay("0123456789abcdef", fw)
	if _, in, err := DecodeRelay(wenv[1:]); err != nil || in.Type != TypeFileWrite {
		t.Fatalf("file write must be relayable: %v %+v", err, in)
	}
}

func TestFileFrame(t *testing.T) {
	body := bytes.Repeat([]byte("a"), 100)
	fr, err := EncodeFile(FileHeader{ReqID: "req-1234567", Path: "/x", Kind: "file", Size: 100, Exists: true}, body)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Decode(fr)
	if err != nil {
		t.Fatal(err)
	}
	h, b, err := DecodeFile(f.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if h.ReqID != "req-1234567" || h.Path != "/x" || !bytes.Equal(b, body) {
		t.Fatalf("bad file decode: %+v %d", h, len(b))
	}
	if _, err := EncodeFile(FileHeader{}, make([]byte, MaxFileBytes+1)); err == nil {
		t.Fatal("expected body too large")
	}
	if _, _, err := DecodeFile([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0xff}); err == nil {
		t.Fatal("expected bad header length")
	}
}

func TestParseHeader(t *testing.T) {
	if tt, err := ParseHeader([]byte(`{"t":"hello","cols":1}`)); err != nil || tt != CtlHello {
		t.Fatalf("%q %v", tt, err)
	}
	if _, err := ParseHeader([]byte(`{}`)); err == nil {
		t.Fatal("missing t must fail")
	}
	if _, err := ParseHeader([]byte(`nope`)); err == nil {
		t.Fatal("bad json must fail")
	}
}

func TestChunkRoundTrip(t *testing.T) {
	small := Encode(TypeOutput, []byte("x"))
	if parts := Chunk(1, small); len(parts) != 1 || &parts[0][0] != &small[0] {
		t.Fatal("small frames must pass through")
	}
	big := Encode(TypeFile, bytes.Repeat([]byte("q"), ChunkSize*2+5))
	parts := Chunk(7, big)
	if len(parts) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(parts))
	}
	assembled := make([]byte, len(big))
	for _, p := range parts {
		f, err := Decode(p)
		if err != nil || f.Type != TypeChunk {
			t.Fatalf("chunk decode: %v", err)
		}
		id, total, off, data, err := ChunkInfo(f.Payload)
		if err != nil || id != 7 || int(total) != len(big) {
			t.Fatalf("chunk info: %d %d %v", id, total, err)
		}
		copy(assembled[off:], data)
	}
	if !bytes.Equal(assembled, big) {
		t.Fatal("reassembly mismatch")
	}
}

// A status header with the most changes of long paths still fits the FILE
// frame's header bound, as the session's byte budget keeps it.
func TestStatusHeaderAtTheBoundEncodes(t *testing.T) {
	h := FileHeader{ReqID: "r", Path: "/work", Kind: "status", Exists: true, Branch: "main", Base: "abc1234"}
	used := 0
	for i := 0; used+200+72 <= MaxFileHeader-4096; i++ {
		p := strings.Repeat("d/", 95) + "file" + strconv.Itoa(i) + ".go"
		h.Changes = append(h.Changes, Change{Path: p, Status: "M", Added: 1234567, Removed: 7654321})
		used += len(p) + 72
	}
	frame, err := EncodeFile(h, nil)
	if err != nil {
		t.Fatalf("%d changes: %v", len(h.Changes), err)
	}
	back, _, err := DecodeFile(frame[1:])
	if err != nil || len(back.Changes) != len(h.Changes) || back.Changes[0].Status != "M" {
		t.Fatalf("back: %v %d", err, len(back.Changes))
	}
}

// A save part round-trips; the part and the header are bounded; the frame
// type's limit admits the largest part.
func TestFileWriteFrame(t *testing.T) {
	part := []byte("package api\n")
	frame, err := EncodeFileWrite(FileWrite{ReqID: "w1", Path: "internal/api/users.go", Offset: 0, Total: int64(len(part)), BaseSha256: "ab", Force: true}, part)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Decode(frame)
	if err != nil || f.Type != TypeFileWrite {
		t.Fatalf("decode: %v %v", f.Type, err)
	}
	h, body, err := DecodeFileWrite(f.Payload)
	if err != nil || h.ReqID != "w1" || h.Path != "internal/api/users.go" || h.Total != int64(len(part)) || !h.Force || h.BaseSha256 != "ab" || string(body) != string(part) {
		t.Fatalf("round trip: %+v %q %v", h, body, err)
	}
	big := make([]byte, MaxWritePart)
	if frame, err := EncodeFileWrite(FileWrite{ReqID: "w2", Path: "a", Total: 1 << 20}, big); err != nil {
		t.Fatal(err)
	} else if _, err := Decode(frame); err != nil {
		t.Fatalf("the largest part: %v", err)
	}
	if _, err := EncodeFileWrite(FileWrite{ReqID: "w3", Path: "a"}, make([]byte, MaxWritePart+1)); err == nil {
		t.Fatal("an oversized part")
	}
	if _, _, err := DecodeFileWrite([]byte{1, 2, 3}); err == nil {
		t.Fatal("a short payload")
	}
}

// The size's messages (round 14): a resize asks to take the size only with
// take; the welcome says the owner sizes by one viewer and which one; the
// fields stay out when unset, so an older owner's frames read as before.
func TestResizeAndWelcomeSizerShapes(t *testing.T) {
	enc := func(v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := enc(Resize{T: CtlResize, Cols: 120, Rows: 40}); got != `{"t":"resize","cols":120,"rows":40}` {
		t.Fatalf("a passive resize: %s", got)
	}
	if got := enc(Resize{T: CtlResize, Cols: 120, Rows: 40, Take: true}); got != `{"t":"resize","cols":120,"rows":40,"take":true}` {
		t.Fatalf("a take: %s", got)
	}
	if got := enc(Resize{T: CtlResize, Cols: 120, Rows: 40, By: "s1"}); got != `{"t":"resize","cols":120,"rows":40,"by":"s1"}` {
		t.Fatalf("a broadcast: %s", got)
	}
	w := enc(Welcome{T: CtlWelcome, Cols: 80, Rows: 24, Sizer: true, SizedBy: "s1"})
	if !strings.Contains(w, `"sizer":true,"sizedBy":"s1"`) {
		t.Fatalf("welcome: %s", w)
	}
	if w := enc(Welcome{T: CtlWelcome, Cols: 80, Rows: 24}); strings.Contains(w, "sizer") || strings.Contains(w, "sizedBy") {
		t.Fatalf("an older owner's welcome: %s", w)
	}
	var back Resize
	if err := json.Unmarshal([]byte(`{"t":"resize","cols":90,"rows":30,"take":true}`), &back); err != nil || !back.Take || back.Cols != 90 {
		t.Fatalf("round trip %+v %v", back, err)
	}
}
