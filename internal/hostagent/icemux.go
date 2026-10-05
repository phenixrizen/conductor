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

// applyICE configures se as c says.
func applyICE(se *webrtc.SettingEngine, c ICE) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.UDPPort > 0 {
		m, err := udpMuxFor(c.UDPPort)
		if err != nil {
			return err
		}
		se.SetICEUDPMux(m)
	}
	if c.PublicIP != "" {
		// The host candidates carry the forwarder's address in place of the
		// interfaces' own (pion's NAT 1:1 as a rewrite rule).
		return se.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{External: []string{c.PublicIP}, AsCandidateType: webrtc.ICECandidateTypeHost, Mode: webrtc.ICEAddressRewriteReplace})
	}
	return nil
}
