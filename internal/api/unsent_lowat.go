//go:build linux || darwin

package api

import (
	"net"
	"syscall"
)

// limitUnsent has c's socket hold at most n bytes it has not sent
// (TCP_NOTSENT_LOWAT): a write waits once the socket holds that much
// unsent, whatever its buffer could take. A connection that is not a
// socket, or a socket that refuses the option, is left as it is.
func limitUnsent(c net.Conn, n int) {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return
	}
	rc, err := sc.SyscallConn()
	if err != nil {
		return
	}
	_ = rc.Control(func(fd uintptr) {
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, tcpNotSentLowat, n)
	})
}
