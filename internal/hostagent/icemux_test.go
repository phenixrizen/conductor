package hostagent

import (
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
)

// freeUDPPort picks a port nothing listens on right now.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	c.Close()
	return port
}

// hostCandidates reads the host candidates a peer sends while it gathers:
// their address and port.
func hostCandidates(t *testing.T, out <-chan any) []string {
	t.Helper()
	var got []string
	deadline := time.After(10 * time.Second)
	for {
		select {
		case v := <-out:
			if m, ok := v.(proto.ViewerICE); ok {
				f := strings.Fields(m.Candidate.Candidate)
				// candidate:<foundation> <component> udp <priority> <ip> <port> typ <type> ...
				if len(f) >= 8 && f[7] == "host" {
					got = append(got, f[4]+":"+f[5])
				}
			}
		case <-deadline:
			t.Fatal("no host candidates")
		}
		if len(got) > 0 {
			// Gathering is quick on loopback: a short wait collects the rest.
			time.Sleep(200 * time.Millisecond)
			for {
				select {
				case v := <-out:
					if m, ok := v.(proto.ViewerICE); ok {
						f := strings.Fields(m.Candidate.Candidate)
						if len(f) >= 8 && f[7] == "host" {
							got = append(got, f[4]+":"+f[5])
						}
					}
					continue
				default:
				}
				return got
			}
		}
	}
}

func testAgent(t *testing.T, opts Options) (*agent, chan any) {
	t.Helper()
	dir := t.TempDir()
	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: dir, Env: hostEnv(nil)})
	if err != nil {
		t.Fatal(err)
	}
	local := session.NewLocal(session.Info{ID: "s", Cwd: dir, Cols: 80, Rows: 24}, proc, session.Options{})
	t.Cleanup(func() { proc.Stop(t.Context(), time.Second) })
	out := make(chan any, 256)
	a := &agent{opts: opts, local: local, proc: proc, peers: map[string]*peer{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.sendHook = func(v any) { out <- v }
	return a, out
}

// With a UDP port and a public address, every host candidate the peer
// advertises carries that address and that port, whatever the interfaces
// say; with the port alone, a viewer still connects through the mux, and
// two peers share it.
func TestICEOnOneUDPPortAdvertisesTheGivenAddress(t *testing.T) {
	port := freeUDPPort(t)
	a, out := testAgent(t, Options{ICE: ICE{UDPPort: port, PublicIP: "203.0.113.9"}})
	p := newPeer(a, "0123456789abcdef", session.RoleControl, "", "", true)
	if err := p.startWebRTC(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.close)
	// Gathering starts with the offer.
	viewer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { viewer.Close() })
	if _, err := viewer.CreateDataChannel("term", nil); err != nil {
		t.Fatal(err)
	}
	offer, _ := viewer.CreateOffer(nil)
	_ = viewer.SetLocalDescription(offer)
	if err := p.handleOffer(offer.SDP); err != nil {
		t.Fatal(err)
	}
	cands := hostCandidates(t, out)
	for _, c := range cands {
		if c != "203.0.113.9:"+strconv.Itoa(port) {
			t.Fatalf("host candidate %s, want 203.0.113.9:%d (all: %v)", c, port, cands)
		}
	}

	// The mux alone: the loopback viewer connects, on the one port.
	b, out2 := testAgent(t, Options{ICE: ICE{UDPPort: port}})
	q := newPeer(b, "fedcba9876543210", session.RoleControl, "", "", true)
	if err := q.startWebRTC(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(q.close)
	dc, _ := loopbackViewer(t, q, out2)
	if dc.ReadyState() != 2 { // open
		t.Fatalf("data channel %v", dc.ReadyState())
	}
	m1, _ := udpMuxFor(port)
	m2, _ := udpMuxFor(port)
	if m1 != m2 {
		t.Fatal("two muxes for one port")
	}
	if m3, err := udpMuxFor(freeUDPPort(t)); err != nil || m3 == m1 {
		t.Fatalf("another port's mux: %v %v", m3 == m1, err)
	}
}

// A port in use, a bad port and a bad address are refused before any peer
// starts.
func TestICERefusesWhatItCannotUse(t *testing.T) {
	for _, c := range []ICE{{UDPPort: -1}, {UDPPort: 70000}, {PublicIP: "not-an-ip"}, {PublicIP: "203.0.113.9/32"}} {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v passed", c)
		}
	}
	if err := (ICE{}).Validate(); err != nil {
		t.Fatal(err)
	}
	busy, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	a, _ := testAgent(t, Options{ICE: ICE{UDPPort: busy.LocalAddr().(*net.UDPAddr).Port}})
	p := newPeer(a, "0123456789abcdef", session.RoleControl, "", "", true)
	if err := p.startWebRTC(nil); err == nil || !strings.Contains(err.Error(), "ice udp port") {
		t.Fatalf("a busy port: %v", err)
	}
}
