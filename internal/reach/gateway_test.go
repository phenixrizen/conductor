package reach

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

const procNetRoute = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0101A8C0	0003	0	0	100	00000000	0	0	0
wlan0	00000000	0102A8C0	0003	0	0	600	00000000	0	0	0
eth0	0001A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
docker0	000011AC	00000000	0001	0	0	0	0000F0FF	0	0	0
`

func TestDefaultGatewayParsesProcNetRoute(t *testing.T) {
	gw, err := parseProcNetRoute(strings.NewReader(procNetRoute))
	if err != nil {
		t.Fatal(err)
	}
	if gw != netip.MustParseAddr("192.168.1.1") {
		t.Fatalf("gateway %s, want the lowest-metric default route 192.168.1.1", gw)
	}
	_, err = parseProcNetRoute(strings.NewReader("Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\neth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"))
	if !errors.Is(err, errNoGateway) {
		t.Fatalf("err = %v, want no gateway", err)
	}
}

func TestParseRouteGet(t *testing.T) {
	out := "   route to: default\ndestination: default\n       mask: default\n    gateway: 10.0.0.1\n  interface: en0\n"
	gw, err := parseRouteGet(out)
	if err != nil || gw != netip.MustParseAddr("10.0.0.1") {
		t.Fatalf("got %s, %v", gw, err)
	}
	if _, err := parseRouteGet("route to: default\n"); !errors.Is(err, errNoGateway) {
		t.Fatalf("err = %v", err)
	}
}
