//go:build !linux && !darwin

package api

import "net"

// limitUnsent does nothing where the system has no TCP_NOTSENT_LOWAT: the
// socket's buffer holds what it holds.
func limitUnsent(net.Conn, int) {}
