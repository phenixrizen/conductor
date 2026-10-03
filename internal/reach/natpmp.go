package reach

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// NAT-PMP (RFC 6886): a gateway answers on UDP 5351 with its external
// address (opcode 0) and maps a TCP (2) or UDP (1) port (lifetime 0 deletes).
// Requests are retransmitted from pmpRTO, doubling, while ctx lasts.
const (
	gatewayPort = 5351
	pmpRTO      = 250 * time.Millisecond
	pmpTimeout  = 8 * time.Second // five sends: 250 ms, 500 ms, 1 s, 2 s, 4 s

	pmpVersion     = 0
	pmpOpExternal  = 0
	pmpOpMapUDP    = 1
	pmpOpMapTCP    = 2
	pmpResponseBit = 128
)

var errUnsupportedVersion = errors.New("the gateway does not speak this version")

// pmpResults are RFC 6886 §3.5's result codes.
var pmpResults = map[uint16]string{
	0: "success",
	1: "unsupported version",
	2: "not authorized: port mapping is turned off on the gateway",
	3: "network failure: the gateway has no external address",
	4: "out of resources on the gateway",
	5: "unsupported opcode",
}

// Mapping is a port mapping a gateway granted.
type Mapping struct {
	Method       string
	ExternalIP   netip.Addr
	ExternalPort uint16
	InternalPort uint16
	Lifetime     time.Duration
	nonce        [12]byte // PCP: the mapping's nonce, needed to renew and delete it
}

// natpmp talks to one gateway. addr is its address and port (5351 unless a
// test fakes it).
type natpmp struct {
	addr netip.AddrPort
	rto  time.Duration
}

func newNATPMP(gateway netip.Addr) natpmp {
	return natpmp{addr: netip.AddrPortFrom(gateway, gatewayPort), rto: pmpRTO}
}

// externalAddress asks the gateway for its public address.
func (c natpmp) externalAddress(ctx context.Context) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, pmpTimeout)
	defer cancel()
	var ip netip.Addr
	err := udpExchange(ctx, c.addr.String(), []byte{pmpVersion, pmpOpExternal}, c.rto, func(b []byte) (bool, error) {
		if len(b) < 12 || b[0] != pmpVersion || b[1] != pmpResponseBit+pmpOpExternal {
			return false, nil
		}
		if err := pmpResult(binary.BigEndian.Uint16(b[2:4])); err != nil {
			return false, err
		}
		ip = netip.AddrFrom4([4]byte(b[8:12]))
		return true, nil
	})
	if err != nil {
		return netip.Addr{}, fmt.Errorf("nat-pmp %s: %w", c.addr, err)
	}
	return ip, nil
}

// mapTCP asks for external → internal on TCP for lifetime (0 with external 0
// deletes the mapping of internal). The gateway may grant another external
// port; the mapping says which.
func (c natpmp) mapTCP(ctx context.Context, internal, external uint16, lifetime time.Duration) (Mapping, error) {
	ctx, cancel := context.WithTimeout(ctx, pmpTimeout)
	defer cancel()
	req := make([]byte, 12)
	req[0], req[1] = pmpVersion, pmpOpMapTCP
	binary.BigEndian.PutUint16(req[4:6], internal)
	binary.BigEndian.PutUint16(req[6:8], external)
	binary.BigEndian.PutUint32(req[8:12], uint32(lifetime/time.Second))
	var m Mapping
	err := udpExchange(ctx, c.addr.String(), req, c.rto, func(b []byte) (bool, error) {
		if len(b) < 16 || b[0] != pmpVersion || b[1] != pmpResponseBit+pmpOpMapTCP {
			return false, nil
		}
		if err := pmpResult(binary.BigEndian.Uint16(b[2:4])); err != nil {
			return false, err
		}
		if binary.BigEndian.Uint16(b[8:10]) != internal {
			return false, nil // another mapping's answer
		}
		m = Mapping{Method: MethodNATPMP, InternalPort: internal,
			ExternalPort: binary.BigEndian.Uint16(b[10:12]),
			Lifetime:     time.Duration(binary.BigEndian.Uint32(b[12:16])) * time.Second}
		return true, nil
	})
	if err != nil {
		return Mapping{}, fmt.Errorf("nat-pmp %s: %w", c.addr, err)
	}
	return m, nil
}

// unmapTCP deletes the mapping of internal.
func (c natpmp) unmapTCP(ctx context.Context, internal uint16) error {
	_, err := c.mapTCP(ctx, internal, 0, 0)
	return err
}

func pmpResult(code uint16) error {
	switch code {
	case 0:
		return nil
	case 1:
		return errUnsupportedVersion
	}
	if s, ok := pmpResults[code]; ok {
		return errors.New(s)
	}
	return fmt.Errorf("result code %d", code)
}
