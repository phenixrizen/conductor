//go:build !linux && !darwin

package reach

import (
	"errors"
	"net/netip"
)

func defaultGateway() (netip.Addr, error) {
	return netip.Addr{}, errors.New("finding the default gateway is not supported on this platform")
}
