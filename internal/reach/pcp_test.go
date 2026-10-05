package reach

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPCPMapKeepsTheNonce(t *testing.T) {
	g := newFakeGateway(t, true)
	c := g.pcpClient()
	nonce := newNonce()
	m, err := c.mapTCP(context.Background(), nonce, 8443, 443, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if m.Method != MethodPCP || m.ExternalPort != 443 || m.InternalPort != 8443 || m.Lifetime != time.Hour || m.nonce != nonce {
		t.Fatalf("mapping %+v", m)
	}
	if m.ExternalIP != netip.MustParseAddr("203.0.113.7") {
		t.Fatalf("external ip %s", m.ExternalIP)
	}
	if _, err := c.mapTCP(context.Background(), m.nonce, 8443, 443, 30*time.Minute); err != nil {
		t.Fatalf("renewal with the mapping's nonce: %v", err)
	}
	if _, err := c.mapTCP(context.Background(), newNonce(), 8443, 443, time.Hour); err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("another nonce must be refused, got %v", err)
	}
	if err := c.unmapTCP(context.Background(), m.nonce, 8443); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.mapping(8443); ok {
		t.Fatal("still mapped after unmap")
	}
	var ips []net.IP
	g.set(func() { ips = g.clientIPs })
	if len(ips) == 0 || !ips[0].Equal(net.IPv4(127, 0, 0, 1)) || len(ips[0]) != 16 {
		t.Fatalf("client address in the request: %v", ips)
	}
}

func TestPCPUnsupportedVersionIsReported(t *testing.T) {
	g := newFakeGateway(t, false) // NAT-PMP only: answers a PCP request in NAT-PMP
	_, err := g.pcpClient().mapTCP(context.Background(), newNonce(), 8443, 443, time.Hour)
	if !errors.Is(err, errUnsupportedVersion) {
		t.Fatalf("err = %v, want unsupported version", err)
	}
}

func TestPCPReportsARefusal(t *testing.T) {
	g := newFakeGateway(t, true)
	g.set(func() { g.pcpResult = 11 })
	_, err := g.pcpClient().mapTCP(context.Background(), newNonce(), 8443, 443, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "cannot provide the external port") {
		t.Fatalf("err = %v", err)
	}
}

func TestPCPGivesUp(t *testing.T) {
	g := newFakeGateway(t, true)
	g.set(func() { g.drop = 1 << 20 })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := g.pcpClient().mapTCP(ctx, newNonce(), 8443, 443, time.Hour)
	if !errors.Is(err, errNoAnswer) {
		t.Fatalf("err = %v", err)
	}
}
