//go:build !linux

package api

import "net"

// limitUnsent does nothing but on Linux: macOS has TCP_NOTSENT_LOWAT, but
// there it only says when a socket is writable, and a write the socket's
// buffer has room for is taken whatever it holds unsent. The buffer holds
// what it holds, and a viewer that reads nothing is counted behind later.
func limitUnsent(net.Conn, int) {}
