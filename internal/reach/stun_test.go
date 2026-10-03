package reach

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/stun/v4"
)

// fakeSTUN answers Binding requests on loopback with what reply builds for
// the nth request (1-based); a nil reply drops the request.
type fakeSTUN struct {
	t        *testing.T
	conn     *net.UDPConn
	requests atomic.Int32
	reply    func(n int, req *stun.Message) *stun.Message
}

func newFakeSTUN(t *testing.T, reply func(n int, req *stun.Message) *stun.Message) *fakeSTUN {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSTUN{t: t, conn: conn, reply: reply}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			req := new(stun.Message)
			if err := stun.Decode(buf[:n], req); err != nil || req.Type != stun.BindingRequest {
				continue
			}
			k := int(f.requests.Add(1))
			if m := f.reply(k, req); m != nil {
				_, _ = conn.WriteToUDP(m.Raw, from)
			}
		}
	}()
	return f
}

func (f *fakeSTUN) url() string { return "stun:" + f.conn.LocalAddr().String() }

func success(req *stun.Message, ip string, port int) *stun.Message {
	return stun.MustBuild(stun.NewTransactionIDSetter(req.TransactionID), stun.BindingSuccess,
		&stun.XORMappedAddress{IP: net.ParseIP(ip), Port: port}, stun.Fingerprint)
}

func TestPublicAddrReadsTheMappedAddress(t *testing.T) {
	f := newFakeSTUN(t, func(_ int, req *stun.Message) *stun.Message { return success(req, "203.0.113.5", 40000) })
	got, err := PublicAddr(context.Background(), f.url())
	if err != nil {
		t.Fatal(err)
	}
	if want := netip.MustParseAddrPort("203.0.113.5:40000"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if n := f.requests.Load(); n != 1 {
		t.Fatalf("sent %d requests, want 1", n)
	}
}

func TestPublicAddrFallsBackToMappedAddress(t *testing.T) {
	f := newFakeSTUN(t, func(_ int, req *stun.Message) *stun.Message {
		return stun.MustBuild(stun.NewTransactionIDSetter(req.TransactionID), stun.BindingSuccess,
			&stun.MappedAddress{IP: net.ParseIP("198.51.100.9"), Port: 5000})
	})
	got, err := PublicAddr(context.Background(), f.url())
	if err != nil {
		t.Fatal(err)
	}
	if want := netip.MustParseAddrPort("198.51.100.9:5000"); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestPublicAddrTimesOutAndRetransmits(t *testing.T) {
	f := newFakeSTUN(t, func(int, *stun.Message) *stun.Message { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := publicAddr(ctx, f.url(), 50*time.Millisecond)
	if !errors.Is(err, errSTUNNoAnswer) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want no answer with the deadline", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s, want the context's 400 ms", d)
	}
	if n := f.requests.Load(); n < 2 {
		t.Fatalf("sent %d requests, want retransmissions", n)
	}
}

func TestPublicAddrIgnoresAnotherTransaction(t *testing.T) {
	f := newFakeSTUN(t, func(n int, req *stun.Message) *stun.Message {
		if n == 1 {
			other := stun.NewTransactionID()
			return stun.MustBuild(stun.NewTransactionIDSetter(other), stun.BindingSuccess,
				&stun.XORMappedAddress{IP: net.ParseIP("192.0.2.66"), Port: 1}, stun.Fingerprint)
		}
		return success(req, "203.0.113.5", 40001)
	})
	got, err := publicAddr(context.Background(), f.url(), 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if want := netip.MustParseAddrPort("203.0.113.5:40001"); got != want {
		t.Fatalf("got %s, want %s (the wrong transaction's address must be ignored)", got, want)
	}
}

func TestPublicAddrReportsABindingError(t *testing.T) {
	f := newFakeSTUN(t, func(_ int, req *stun.Message) *stun.Message {
		return stun.MustBuild(stun.NewTransactionIDSetter(req.TransactionID), stun.BindingError,
			stun.ErrorCodeAttribute{Code: stun.CodeServerError, Reason: []byte("Server Error")}, stun.Fingerprint)
	})
	_, err := PublicAddr(context.Background(), f.url())
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v, want the server's 500", err)
	}
}

func TestPublicAddrRefusesWhatIsNotSTUNOverUDP(t *testing.T) {
	for _, s := range []string{"", "turn:relay.example.net:3478", "stuns:stun.example.net", "stun:", "stun:host:notaport"} {
		if _, err := PublicAddr(context.Background(), s); err == nil {
			t.Errorf("%q: want an error", s)
		}
	}
}

func TestParseSTUNServer(t *testing.T) {
	cases := map[string]string{
		"stun:stun.l.google.com:19302":               "stun.l.google.com:19302",
		"stun.example.net":                           "stun.example.net:3478",
		"stun:stun.example.net":                      "stun.example.net:3478",
		" stun:stun.example.net:3478?transport=udp ": "stun.example.net:3478",
		"stun:[2001:db8::1]:3478":                    "[2001:db8::1]:3478",
	}
	for in, want := range cases {
		got, err := parseSTUNServer(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, want)
		}
	}
}
