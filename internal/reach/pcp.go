package reach

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// PCP (RFC 6887): version 2 on the same UDP port as NAT-PMP. A MAP request
// carries the client's own address (the gateway checks it against the
// packet's source), a nonce that renewals and the deletion must repeat, the
// protocol, the internal port and a suggested external port and address.
const (
	pcpVersion     = 2
	pcpOpMAP       = 1
	pcpResponseBit = 0x80
	pcpProtoTCP    = 6
	pcpHeaderLen   = 24
	pcpMapLen      = 36
	pcpRTO         = 1 * time.Second // RFC 6887 §8.1.1 starts at 3 s; a bounded exchange starts sooner
	pcpTimeout     = 8 * time.Second
)

// pcpResults are RFC 6887 §7.4's result codes.
var pcpResults = map[byte]string{
	0:  "success",
	1:  "unsupported version",
	2:  "not authorized: port mapping is turned off on the gateway",
	3:  "malformed request",
	4:  "unsupported opcode",
	5:  "unsupported option",
	6:  "malformed option",
	7:  "network failure on the gateway",
	8:  "no resources on the gateway",
	9:  "unsupported protocol",
	10: "the gateway's quota for this client is used up",
	11: "the gateway cannot provide the external port asked for",
	12: "address mismatch: the request did not come from the address it names",
	13: "excessive remote peers",
}

type pcp struct {
	addr netip.AddrPort
	rto  time.Duration
}

func newPCP(gateway netip.Addr) pcp {
	return pcp{addr: netip.AddrPortFrom(gateway, gatewayPort), rto: pcpRTO}
}

func newNonce() (n [12]byte) {
	_, _ = rand.Read(n[:])
	return n
}

// mapTCP asks for external → internal on TCP for lifetime under nonce (a new
// one for a new mapping, the mapping's own to renew it; lifetime 0 deletes).
// A gateway that answers with another version (a NAT-PMP-only one answers in
// NAT-PMP) gives errUnsupportedVersion, and the caller falls back.
func (c pcp) mapTCP(ctx context.Context, nonce [12]byte, internal, external uint16, lifetime time.Duration) (Mapping, error) {
	ctx, cancel := context.WithTimeout(ctx, pcpTimeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp", c.addr.String())
	if err != nil {
		return Mapping{}, fmt.Errorf("pcp %s: %w", c.addr, err)
	}
	defer conn.Close()
	req := make([]byte, pcpHeaderLen+pcpMapLen)
	req[0], req[1] = pcpVersion, pcpOpMAP
	binary.BigEndian.PutUint32(req[4:8], uint32(lifetime/time.Second))
	copy(req[8:24], localIPFor(conn).To16())
	copy(req[24:36], nonce[:])
	req[36] = pcpProtoTCP
	binary.BigEndian.PutUint16(req[40:42], internal)
	binary.BigEndian.PutUint16(req[42:44], external)
	var m Mapping
	err = udpExchangeOn(ctx, conn, req, c.rto, func(b []byte) (bool, error) {
		if len(b) < 4 {
			return false, nil
		}
		if b[0] != pcpVersion {
			// RFC 6887 §9: a server that does not support the version answers
			// UNSUPP_VERSION with the highest it supports; a NAT-PMP-only
			// gateway answers in NAT-PMP (version 0, result 1).
			if b[0] == pmpVersion && len(b) >= 4 && binary.BigEndian.Uint16(b[2:4]) == 1 {
				return false, errUnsupportedVersion
			}
			if len(b) >= pcpHeaderLen && b[3] == 1 {
				return false, errUnsupportedVersion
			}
			return false, nil
		}
		if b[1] != pcpResponseBit|pcpOpMAP || len(b) < pcpHeaderLen+pcpMapLen {
			return false, nil
		}
		if [12]byte(b[24:36]) != nonce {
			return false, nil // another mapping's answer
		}
		if code := b[3]; code != 0 {
			if code == 1 {
				return false, errUnsupportedVersion
			}
			if s, ok := pcpResults[code]; ok {
				return false, errors.New(s)
			}
			return false, fmt.Errorf("result code %d", code)
		}
		ip, _ := netip.AddrFromSlice(b[44:60])
		m = Mapping{Method: MethodPCP, nonce: nonce,
			ExternalIP:   ip.Unmap(),
			ExternalPort: binary.BigEndian.Uint16(b[42:44]),
			InternalPort: binary.BigEndian.Uint16(b[40:42]),
			Lifetime:     time.Duration(binary.BigEndian.Uint32(b[4:8])) * time.Second}
		return true, nil
	})
	if err != nil {
		return Mapping{}, fmt.Errorf("pcp %s: %w", c.addr, err)
	}
	return m, nil
}

// unmapTCP deletes the mapping made under nonce.
func (c pcp) unmapTCP(ctx context.Context, nonce [12]byte, internal uint16) error {
	_, err := c.mapTCP(ctx, nonce, internal, 0, 0)
	return err
}
