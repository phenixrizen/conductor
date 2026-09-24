package proto

import (
	"bytes"
	"errors"
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
