package session

import (
	"bytes"
	"testing"
)

func TestRingWrapAndSnapshot(t *testing.T) {
	r := NewRing(10)
	r.Write([]byte("abc"))
	if got := r.Snapshot(); string(got) != "abc" {
		t.Fatalf("got %q", got)
	}
	r.Write([]byte("defgh"))
	r.Write([]byte("ij"))
	if got := r.Snapshot(); string(got) != "abcdefghij" || r.Len() != 10 {
		t.Fatalf("got %q len %d", got, r.Len())
	}
	r.Write([]byte("kl"))
	// wrapped without newline: full content minus the two oldest bytes
	if got := r.Snapshot(); string(got) != "cdefghijkl" {
		t.Fatalf("got %q", got)
	}
}

func TestRingNewlineTrimAfterWrap(t *testing.T) {
	r := NewRing(8)
	r.Write([]byte("12\n34\n56\n78"))
	if got := r.Snapshot(); string(got) != "56\n78" {
		t.Fatalf("got %q", got)
	}
}

func TestRingOversizedWrite(t *testing.T) {
	r := NewRing(4)
	r.Write(bytes.Repeat([]byte("x"), 100))
	r.Write([]byte("yz"))
	if got := r.Snapshot(); string(got) != "xxyz" {
		t.Fatalf("got %q", got)
	}
	if NewRing(0).Cap() != 1 {
		t.Fatal("zero capacity must be clamped")
	}
}
