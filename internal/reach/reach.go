// Package reach finds out how the server can be reached from outside its
// network and, when a router allows it, makes it so: the public address by
// STUN, a port mapping on the gateway through UPnP IGD, PCP or NAT-PMP, and a
// self-check through the public URL.
//
// STUN and ICE connect the terminal channel of a hosted session once a browser
// has loaded the join page; nothing in them makes the server's own page
// reachable. This package is what does, and it is honest about what it knows:
// a mapping is reported as mapped, never as reachable, because a router that
// does not hairpin refuses the server's own check while a phone on mobile
// data gets through.
package reach

import (
	"net/netip"
	"time"
)

// Modes of the mapper (config reach.mode).
const (
	ModeOff    = "off"    // never ask the network anything
	ModeAuto   = "auto"   // discover the address and map the TLS port on the gateway
	ModeManual = "manual" // discover the address only; the person forwarded the port
)

// Methods a mapping was obtained with.
const (
	MethodUPnP   = "upnp"
	MethodPCP    = "pcp"
	MethodNATPMP = "nat-pmp"
	MethodManual = "manual"
)

// Status is what the server knows about its reach, as GET /api/reach reports it.
type Status struct {
	Mode   string `json:"mode"`
	Mapped bool   `json:"mapped"`
	// Method that obtained the mapping: upnp, pcp, nat-pmp, or manual when
	// the person forwarded the port; empty while nothing is mapped.
	Method string `json:"method,omitempty"`
	// PublicURL is https://<externalIp>[:port] once mapped (or manual), the
	// base share links take while a certificate for it is ready.
	PublicURL    string     `json:"publicUrl,omitempty"`
	ExternalIP   netip.Addr `json:"externalIp,omitempty"`
	ExternalPort uint16     `json:"externalPort,omitempty"`
	ListenPort   uint16     `json:"listenPort,omitempty"`
	ExpiresAt    time.Time  `json:"expiresAt,omitzero"`
	RenewedAt    time.Time  `json:"renewedAt,omitzero"`
	// Verified is "ok" when the self-check got through, "unverified" when it
	// did not (common from inside the network), empty before it ran.
	Verified string `json:"verified,omitempty"`
	// Err is the last failure, in words a person can act on.
	Err string `json:"error,omitempty"`
}
