package reach

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

var errNoAnswer = errors.New("no answer")

// udpExchange sends req to addr and reads datagrams until accept takes one,
// retransmitting on a doubling schedule from rto (the first send is
// immediate) while ctx lasts. accept returns done for the answer, false for a
// datagram to ignore, or an error to stop with. The exchange is bounded by
// ctx, which the caller gives a deadline.
//
// IPv4 only: reach is about the address a NAT shows and the gateway protocols,
// all of them IPv4. On a dual-stack host a plain "udp" dial reaches the STUN
// server over IPv6 and brings back the host's IPv6 address, and a certificate
// ordered for it matches no IPv4 link (seen on a Lightsail instance, 2026-10-04).
func udpExchange(ctx context.Context, addr string, req []byte, rto time.Duration, accept func(b []byte) (done bool, err error)) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "udp4", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	return udpExchangeOn(ctx, conn, req, rto, accept)
}

func udpExchangeOn(ctx context.Context, conn net.Conn, req []byte, rto time.Duration, accept func(b []byte) (done bool, err error)) error {
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	buf := make([]byte, 1500)
	next := time.Now()
	for {
		if !time.Now().Before(next) {
			if _, err := conn.Write(req); err != nil {
				return err
			}
			next = time.Now().Add(rto)
			rto *= 2
		}
		_ = conn.SetReadDeadline(next)
		n, err := conn.Read(buf)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w (%w)", errNoAnswer, ctx.Err())
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		done, err := accept(buf[:n])
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

// localIPFor reports the address this machine would send from to reach addr.
func localIPFor(conn net.Conn) net.IP {
	if a, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return a.IP
	}
	return nil
}
