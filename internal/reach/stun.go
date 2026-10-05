package reach

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/pion/stun/v4"
)

// Timing of one STUN Binding exchange (RFC 5389 §7.2.1: RTO 500 ms, doubled
// per retransmission; the whole exchange is bounded by stunTimeout).
const (
	stunRTO     = 500 * time.Millisecond
	stunTimeout = 3 * time.Second
	stunPort    = "3478"
)

var errSTUNNoAnswer = errors.New("no answer from the STUN server")

// PublicAddr asks one STUN server for the address and port this machine shows
// to the internet: one Binding request over UDP, retransmitted on RFC 5389's
// schedule, answered within stunTimeout unless ctx ends first. server is a
// "stun:host[:port]" URL as the config's iceServers hold them, or "host[:port]";
// the port defaults to 3478. STUNS, TURN and TURNS servers are refused: the
// lookup needs plain UDP, which is what a browser's ICE uses too.
func PublicAddr(ctx context.Context, server string) (netip.AddrPort, error) {
	return publicAddr(ctx, server, stunRTO)
}

func publicAddr(ctx context.Context, server string, rto time.Duration) (netip.AddrPort, error) {
	hostport, err := parseSTUNServer(server)
	if err != nil {
		return netip.AddrPort{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, stunTimeout)
	defer cancel()
	req := stun.MustBuild(stun.TransactionID, stun.BindingRequest, stun.Fingerprint)
	var addr netip.AddrPort
	err = udpExchange(ctx, hostport, req.Raw, rto, func(b []byte) (bool, error) {
		a, ok, err := readBinding(req, b)
		addr = a
		return ok, err
	})
	if err != nil {
		if errors.Is(err, errNoAnswer) {
			err = fmt.Errorf("%w (%w)", errSTUNNoAnswer, ctx.Err())
		}
		return netip.AddrPort{}, fmt.Errorf("stun %s: %w", hostport, err)
	}
	return addr, nil
}

// readBinding reads one datagram as the answer to req. ok is false for a
// datagram that is not the answer (not STUN, another transaction): the caller
// keeps waiting. An error response is an error.
func readBinding(req *stun.Message, b []byte) (addr netip.AddrPort, ok bool, err error) {
	if !stun.IsMessage(b) {
		return addr, false, nil
	}
	m := new(stun.Message)
	if err := stun.Decode(b, m); err != nil {
		return addr, false, nil
	}
	if m.TransactionID != req.TransactionID {
		return addr, false, nil
	}
	switch m.Type {
	case stun.BindingSuccess:
	case stun.BindingError:
		var code stun.ErrorCodeAttribute
		if err := code.GetFrom(m); err == nil {
			return addr, false, fmt.Errorf("the server answered %d %s", code.Code, strings.TrimSpace(string(code.Reason)))
		}
		return addr, false, errors.New("the server answered with an error")
	default:
		return addr, false, nil
	}
	var xor stun.XORMappedAddress
	if err := xor.GetFrom(m); err == nil {
		return toAddrPort(xor.IP, xor.Port)
	}
	var mapped stun.MappedAddress
	if err := mapped.GetFrom(m); err == nil {
		return toAddrPort(mapped.IP, mapped.Port)
	}
	return addr, false, errors.New("the answer carries no mapped address")
}

func toAddrPort(ip net.IP, port int) (netip.AddrPort, bool, error) {
	a, ok := netip.AddrFromSlice(ip)
	if !ok || port <= 0 || port > 65535 {
		return netip.AddrPort{}, false, errors.New("the answer carries a malformed mapped address")
	}
	return netip.AddrPortFrom(a.Unmap(), uint16(port)), true, nil
}

// parseSTUNServer turns "stun:host[:port]" or "host[:port]" into host:port.
func parseSTUNServer(server string) (string, error) {
	s := strings.TrimSpace(server)
	if s == "" {
		return "", errors.New("no STUN server configured")
	}
	if i := strings.Index(s, ":"); i > 0 {
		switch strings.ToLower(s[:i]) {
		case "stun":
			s = s[i+1:]
		case "stuns", "turn", "turns":
			return "", fmt.Errorf("%s is not a STUN server over UDP", server)
		}
	}
	s = strings.TrimSpace(strings.SplitN(s, "?", 2)[0])
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		host, port = s, stunPort
	}
	if host == "" {
		return "", fmt.Errorf("%q names no STUN host", server)
	}
	if _, err := net.LookupPort("udp", port); err != nil {
		return "", fmt.Errorf("%q: %w", server, err)
	}
	return net.JoinHostPort(host, port), nil
}
