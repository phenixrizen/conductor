package reach

import (
	"net/netip"
	"os"
)

// defaultGateway reads the default route from /proc/net/route.
func defaultGateway() (netip.Addr, error) {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return netip.Addr{}, err
	}
	defer f.Close()
	return parseProcNetRoute(f)
}
