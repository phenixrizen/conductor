package paste

import (
	"strings"
	"testing"
)

// A blob round-trips, carries its type, and refuses what is not one.
func TestBlob(t *testing.T) {
	sdp := strings.Repeat("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\na=candidate:1 1 udp 2130706431 192.168.1.20 7877 typ host\r\n", 20)
	blob, err := EncodeBlob("offer", sdp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(blob, BlobPrefix) || len(blob) >= len(sdp) || strings.ContainsAny(blob, "+/=\n ") {
		t.Fatalf("blob %q", blob[:40])
	}
	got, err := DecodeBlob(" "+blob+"\n", "offer")
	if err != nil || got != sdp {
		t.Fatalf("decode: %v", err)
	}
	if _, err := DecodeBlob(blob, "answer"); err == nil || !strings.Contains(err.Error(), "this is an offer") {
		t.Fatalf("wrong type: %v", err)
	}
	for _, bad := range []string{"", "hello", "cpi1.", "cpi1.!!!", BlobPrefix + strings.Repeat("A", MaxBlob+1)} {
		if _, err := DecodeBlob(bad, ""); err == nil {
			t.Errorf("%q passed", bad[:min(len(bad), 20)])
		}
	}
	if _, err := EncodeBlob("offer", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBlob(func() string { b, _ := EncodeBlob("offer", ""); return b }(), ""); err == nil {
		t.Fatal("an empty SDP passed")
	}
}
