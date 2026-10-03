package reach

import (
	"context"
	"net/netip"
	"os/exec"
	"time"
)

// defaultGateway asks the routing table through route(8), an argv with no
// user input in it.
func defaultGateway() (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "route", "-n", "get", "default").Output()
	if err != nil {
		return netip.Addr{}, err
	}
	return parseRouteGet(string(out))
}
