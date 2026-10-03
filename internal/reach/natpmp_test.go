package reach

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestNATPMPReportsTheExternalAddress(t *testing.T) {
	g := newFakeGateway(t, false)
	ip, err := g.natpmp().externalAddress(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip != netip.MustParseAddr("203.0.113.7") {
		t.Fatalf("got %s", ip)
	}
}

func TestNATPMPMapsAndRenews(t *testing.T) {
	g := newFakeGateway(t, false)
	c := g.natpmp()
	m, err := c.mapTCP(context.Background(), 8443, 443, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if m.Method != MethodNATPMP || m.ExternalPort != 443 || m.InternalPort != 8443 || m.Lifetime != time.Hour {
		t.Fatalf("mapping %+v", m)
	}
	if fm, ok := g.mapping(8443); !ok || fm.external != 443 || fm.lifetime != 3600 {
		t.Fatalf("gateway holds %+v, %v", fm, ok)
	}
	if _, err := c.mapTCP(context.Background(), 8443, 443, 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	if fm, _ := g.mapping(8443); fm.lifetime != 1800 {
		t.Fatalf("renewal did not shorten the lease: %+v", fm)
	}
	if err := c.unmapTCP(context.Background(), 8443); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.mapping(8443); ok {
		t.Fatal("the mapping is still there after unmap")
	}
	want := []string{"pmp map 8443<-443 3600", "pmp map 8443<-443 1800", "pmp unmap 8443"}
	if got := g.seen(); strings.Join(got, ";") != strings.Join(want, ";") {
		t.Fatalf("requests %v, want %v", got, want)
	}
}

func TestNATPMPTakesTheGrantedPort(t *testing.T) {
	g := newFakeGateway(t, false)
	g.set(func() { g.grantPort = func(uint16) uint16 { return 44300 } })
	m, err := g.natpmp().mapTCP(context.Background(), 8443, 443, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if m.ExternalPort != 44300 {
		t.Fatalf("external port %d, want the gateway's 44300", m.ExternalPort)
	}
}

func TestNATPMPReportsARefusal(t *testing.T) {
	g := newFakeGateway(t, false)
	g.set(func() { g.pmpResult = 2 })
	_, err := g.natpmp().mapTCP(context.Background(), 8443, 443, time.Hour)
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err = %v", err)
	}
}

func TestNATPMPGivesUpAfterRetries(t *testing.T) {
	g := newFakeGateway(t, false)
	g.set(func() { g.drop = 1 << 20 })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := g.natpmp().externalAddress(ctx)
	if !errors.Is(err, errNoAnswer) {
		t.Fatalf("err = %v, want no answer", err)
	}
	if n := len(g.seen()); n < 2 {
		t.Fatalf("%d requests, want retransmissions", n)
	}
}
