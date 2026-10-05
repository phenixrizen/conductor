package reach

import (
	"encoding/binary"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

type fakeMapping struct {
	external uint16
	lifetime uint32
	nonce    [12]byte
}

// fakeGateway speaks NAT-PMP and, when pcp is set, PCP on a loopback UDP
// port. It records every request and keeps the mappings it granted.
type fakeGateway struct {
	t    *testing.T
	conn *net.UDPConn
	addr netip.AddrPort

	mu        sync.Mutex
	external  netip.Addr
	pcp       bool
	drop      int // requests to drop before answering
	pmpResult uint16
	pcpResult byte
	grantPort func(suggested uint16) uint16
	requests  []string
	clientIPs []net.IP // PCP: the client address each request named
	mappings  map[uint16]fakeMapping
}

func newFakeGateway(t *testing.T, pcp bool) *fakeGateway {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	g := &fakeGateway{t: t, conn: conn, external: netip.MustParseAddr("203.0.113.7"), pcp: pcp, mappings: map[uint16]fakeMapping{}}
	g.addr = conn.LocalAddr().(*net.UDPAddr).AddrPort()
	t.Cleanup(func() { conn.Close() })
	go g.serve()
	return g
}

func (g *fakeGateway) natpmp() natpmp { return natpmp{addr: g.addr, rto: 50 * time.Millisecond} }
func (g *fakeGateway) pcpClient() pcp { return pcp{addr: g.addr, rto: 50 * time.Millisecond} }

// set changes the fake's behaviour under its lock, after serve has started.
func (g *fakeGateway) set(f func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f()
}

func (g *fakeGateway) seen() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.requests...)
}

func (g *fakeGateway) mapping(internal uint16) (fakeMapping, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m, ok := g.mappings[internal]
	return m, ok
}

func (g *fakeGateway) serve() {
	buf := make([]byte, 1500)
	for {
		n, from, err := g.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if reply := g.handle(buf[:n], from); reply != nil {
			_, _ = g.conn.WriteToUDP(reply, from)
		}
	}
}

func (g *fakeGateway) handle(b []byte, from *net.UDPAddr) []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.drop > 0 {
		g.drop--
		g.requests = append(g.requests, "dropped")
		return nil
	}
	if len(b) < 2 {
		return nil
	}
	switch b[0] {
	case pmpVersion:
		return g.handlePMP(b)
	case pcpVersion:
		if !g.pcp {
			g.requests = append(g.requests, "pcp refused (nat-pmp only)")
			r := make([]byte, 8)
			r[1] = pmpResponseBit
			binary.BigEndian.PutUint16(r[2:4], 1)
			return r
		}
		return g.handlePCP(b, from)
	}
	return nil
}

func (g *fakeGateway) handlePMP(b []byte) []byte {
	switch b[1] {
	case pmpOpExternal:
		g.requests = append(g.requests, "pmp external")
		r := make([]byte, 12)
		r[1] = pmpResponseBit + pmpOpExternal
		binary.BigEndian.PutUint16(r[2:4], g.pmpResult)
		copy(r[8:12], g.external.AsSlice())
		return r
	case pmpOpMapTCP:
		if len(b) < 12 {
			return nil
		}
		internal := binary.BigEndian.Uint16(b[4:6])
		suggested := binary.BigEndian.Uint16(b[6:8])
		lifetime := binary.BigEndian.Uint32(b[8:12])
		r := make([]byte, 16)
		r[1] = pmpResponseBit + pmpOpMapTCP
		binary.BigEndian.PutUint16(r[2:4], g.pmpResult)
		binary.BigEndian.PutUint16(r[8:10], internal)
		if lifetime == 0 {
			g.requests = append(g.requests, "pmp unmap "+itoa(internal))
			delete(g.mappings, internal)
			return r
		}
		granted := suggested
		if g.grantPort != nil {
			granted = g.grantPort(suggested)
		}
		g.requests = append(g.requests, "pmp map "+itoa(internal)+"<-"+itoa(granted)+" "+itoa(uint16(lifetime)))
		if g.pmpResult == 0 {
			g.mappings[internal] = fakeMapping{external: granted, lifetime: lifetime}
		}
		binary.BigEndian.PutUint16(r[10:12], granted)
		binary.BigEndian.PutUint32(r[12:16], lifetime)
		return r
	}
	return nil
}

func (g *fakeGateway) handlePCP(b []byte, from *net.UDPAddr) []byte {
	if len(b) < pcpHeaderLen+pcpMapLen || b[1] != pcpOpMAP {
		return nil
	}
	lifetime := binary.BigEndian.Uint32(b[4:8])
	client := net.IP(b[8:24])
	g.clientIPs = append(g.clientIPs, append(net.IP(nil), client...))
	nonce := [12]byte(b[24:36])
	internal := binary.BigEndian.Uint16(b[40:42])
	suggested := binary.BigEndian.Uint16(b[42:44])
	r := make([]byte, pcpHeaderLen+pcpMapLen)
	r[0], r[1] = pcpVersion, pcpResponseBit|pcpOpMAP
	copy(r[24:36], nonce[:])
	r[36] = pcpProtoTCP
	binary.BigEndian.PutUint16(r[40:42], internal)
	result := g.pcpResult
	if !client.Equal(from.IP) {
		result = 12
	}
	existing, has := g.mappings[internal]
	if has && existing.nonce != nonce && result == 0 {
		result = 2 // RFC 6887 §15.1: a nonce that does not match alters nothing
	}
	r[3] = result
	if result != 0 {
		g.requests = append(g.requests, "pcp refused "+itoa(uint16(result)))
		return r
	}
	if lifetime == 0 {
		g.requests = append(g.requests, "pcp unmap "+itoa(internal))
		delete(g.mappings, internal)
		return r
	}
	granted := suggested
	if g.grantPort != nil {
		granted = g.grantPort(suggested)
	}
	g.requests = append(g.requests, "pcp map "+itoa(internal)+"<-"+itoa(granted)+" "+itoa(uint16(lifetime)))
	g.mappings[internal] = fakeMapping{external: granted, lifetime: lifetime, nonce: nonce}
	binary.BigEndian.PutUint32(r[4:8], lifetime)
	binary.BigEndian.PutUint16(r[42:44], granted)
	ext := g.external.As16()
	copy(r[44:60], ext[:])
	return r
}

func itoa(n uint16) string {
	if n == 0 {
		return "0"
	}
	var d [5]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}
