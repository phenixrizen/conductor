package api

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

// fakeDNS is a DNS server over UDP that answers A and AAAA questions from a
// table, and NXDOMAIN for a name it does not have: enough for the Go
// resolver, pointed at it with resolver, to look names up in a test.
type fakeDNS struct {
	pc    net.PacketConn
	mu    sync.Mutex
	hosts map[string][]netip.Addr // by name, with the final dot
}

func newFakeDNS(t *testing.T) *fakeDNS {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	d := &fakeDNS{pc: pc, hosts: map[string][]netip.Addr{}}
	t.Cleanup(func() { pc.Close() })
	go d.serve()
	return d
}

// set makes name resolve to addrs, in that order.
func (d *fakeDNS) set(name string, addrs ...string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	list := []netip.Addr{}
	for _, a := range addrs {
		list = append(list, netip.MustParseAddr(a))
	}
	d.hosts[strings.ToLower(name)+"."] = list
}

// resolver is a Go resolver that asks this server, whatever the system's
// resolver configuration names.
func (d *fakeDNS) resolver() *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "udp", d.pc.LocalAddr().String())
	}}
}

func (d *fakeDNS) serve() {
	buf := make([]byte, 1500)
	for {
		n, from, err := d.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if resp := d.answer(buf[:n]); resp != nil {
			_, _ = d.pc.WriteTo(resp, from)
		}
	}
}

// answer is the response to the query q: the header with the query's id, the
// question as asked, and an answer record for each address of its type.
func (d *fakeDNS) answer(q []byte) []byte {
	if len(q) < 12 || binary.BigEndian.Uint16(q[4:]) != 1 {
		return nil
	}
	i := 12
	var labels []string
	for i < len(q) && q[i] != 0 {
		n := int(q[i])
		if n > 63 || i+1+n > len(q) {
			return nil
		}
		labels = append(labels, string(q[i+1:i+1+n]))
		i += 1 + n
	}
	if i+5 > len(q) {
		return nil
	}
	qtype, end := binary.BigEndian.Uint16(q[i+1:]), i+5
	d.mu.Lock()
	addrs, known := d.hosts[strings.ToLower(strings.Join(labels, "."))+"."]
	d.mu.Unlock()
	var records [][]byte
	for _, a := range addrs {
		typ := uint16(1) // A
		if a.Is6() {
			typ = 28 // AAAA
		}
		if typ != qtype {
			continue
		}
		rr := []byte{0xc0, 0x0c} // the name of the question
		rr = binary.BigEndian.AppendUint16(rr, typ)
		rr = binary.BigEndian.AppendUint16(rr, 1) // IN
		rr = binary.BigEndian.AppendUint32(rr, 60)
		rr = binary.BigEndian.AppendUint16(rr, uint16(len(a.AsSlice())))
		records = append(records, append(rr, a.AsSlice()...))
	}
	// A response, authoritative, recursion available, the query's RD bit.
	flags := uint16(0x8480) | binary.BigEndian.Uint16(q[2:])&0x0100
	if !known {
		flags |= 3 // NXDOMAIN
	}
	resp := []byte{q[0], q[1]}
	resp = binary.BigEndian.AppendUint16(resp, flags)
	resp = binary.BigEndian.AppendUint16(resp, 1)
	resp = binary.BigEndian.AppendUint16(resp, uint16(len(records)))
	resp = binary.BigEndian.AppendUint32(resp, 0) // no authority, no additional records
	resp = append(resp, q[12:end]...)
	for _, rr := range records {
		resp = append(resp, rr...)
	}
	return resp
}

// useResolver makes the webhooks of servers the test starts from now on dial
// with r.
func useResolver(t *testing.T, r *net.Resolver) {
	t.Helper()
	old := webhookResolver
	webhookResolver = r
	t.Cleanup(func() { webhookResolver = old })
}
