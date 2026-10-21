package hostagent

import (
	"fmt"
	"net"
	"net/netip"
	"sync"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// ICE is how the host's peers gather their candidates. With UDPPort every
// peer connection of this process multiplexes on that one UDP port, so a
// forwarder in front of it (the desktop app on Windows, carrying ICE into
// WSL through Hyper-V's NAT) has one port to carry; with PublicIP the host
// candidates advertise that address instead of the interfaces' own, the
// address the forwarder listens on. Zero values gather as pion does by
// default: every interface, an ephemeral port per connection.
type ICE struct {
	UDPPort  int
	PublicIP string
}

// Validate checks the port and the address.
func (c ICE) Validate() error {
	if c.UDPPort < 0 || c.UDPPort > 65535 {
		return fmt.Errorf("ice udp port %d is out of range", c.UDPPort)
	}
	if c.PublicIP != "" {
		if _, err := netip.ParseAddr(c.PublicIP); err != nil {
			return fmt.Errorf("ice public ip %q: %w", c.PublicIP, err)
		}
	}
	return nil
}

// The UDP muxes of this process, one per port: conductor host sessions and
// a server's published sessions share it, as a forwarder carries one port.
var (
	muxMu sync.Mutex
	muxes = map[int]ice.UDPMux{}
)

// udpMuxFor returns the mux on port, listening on every IPv4 and IPv6
// interface, opened once per process and never closed (the port is the
// process's for its life).
func udpMuxFor(port int) (ice.UDPMux, error) {
	muxMu.Lock()
	defer muxMu.Unlock()
	if m, ok := muxes[port]; ok {
		return m, nil
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{Port: port})
	if err != nil {
		return nil, fmt.Errorf("ice udp port %d: %w", port, err)
	}
	m := webrtc.NewICEUDPMux(nil, conn)
	muxes[port] = m
	return m, nil
}

// applyICE configures se as c says. Without PublicIP the peers gather on
// IPv4 and IPv6 alike. With it they gather on its family alone: the rewrite
// rule replaces the host candidates of that family with the address, and
// one of the other family would pass through as the interface's own
// address, which the option is there to keep out of the SDP. The mux lists
// the addresses of both families whatever the network types say, so with
// PublicIP it is shown through familyMux.
func applyICE(se *webrtc.SettingEngine, c ICE) error {
	if err := c.Validate(); err != nil {
		return err
	}
	types := []webrtc.NetworkType{webrtc.NetworkTypeUDP4, webrtc.NetworkTypeUDP6}
	var public netip.Addr
	if c.PublicIP != "" {
		public = netip.MustParseAddr(c.PublicIP).Unmap()
		types = []webrtc.NetworkType{webrtc.NetworkTypeUDP6}
		if public.Is4() {
			types = []webrtc.NetworkType{webrtc.NetworkTypeUDP4}
		}
	}
	se.SetNetworkTypes(types)
	if c.UDPPort > 0 {
		m, err := udpMuxFor(c.UDPPort)
		if err != nil {
			return err
		}
		if public.IsValid() {
			m = familyMux{UDPMux: m, v4: public.Is4()}
		}
		se.SetICEUDPMux(m)
	}
	if public.IsValid() {
		// The host candidates carry the forwarder's address in place of the
		// interfaces' own (pion's NAT 1:1 as a rewrite rule).
		return se.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{External: []string{public.String()}, AsCandidateType: webrtc.ICECandidateTypeHost, Mode: webrtc.ICEAddressRewriteReplace})
	}
	return nil
}

// familyMux is a mux as one address family sees it: its listen addresses
// of that family alone, so the peers gather host candidates on no other.
// The socket beneath stays as it is, open to both.
type familyMux struct {
	ice.UDPMux
	v4 bool
}

func (m familyMux) GetListenAddresses() []net.Addr {
	var out []net.Addr
	for _, a := range m.UDPMux.GetListenAddresses() {
		if u, ok := a.(*net.UDPAddr); ok && (u.IP.To4() != nil) == m.v4 {
			out = append(out, a)
		}
	}
	return out
}
