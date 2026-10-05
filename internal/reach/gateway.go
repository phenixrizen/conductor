package reach

import (
	"bufio"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
)

var errNoGateway = errors.New("no default gateway")

// parseProcNetRoute reads Linux's /proc/net/route and returns the gateway of
// the default route with the lowest metric: destination 0, flags up and
// gateway (RTF_UP 1, RTF_GATEWAY 2), the address in little-endian hex.
func parseProcNetRoute(r io.Reader) (netip.Addr, error) {
	sc := bufio.NewScanner(r)
	var best netip.Addr
	bestMetric := int64(-1)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 7 || f[0] == "Iface" {
			continue
		}
		if f[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseInt(f[3], 16, 32)
		if err != nil || flags&0x3 != 0x3 {
			continue
		}
		raw, err := hex.DecodeString(f[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		gw := netip.AddrFrom4([4]byte{raw[3], raw[2], raw[1], raw[0]})
		metric, _ := strconv.ParseInt(f[6], 10, 32)
		if bestMetric < 0 || metric < bestMetric {
			best, bestMetric = gw, metric
		}
	}
	if err := sc.Err(); err != nil {
		return netip.Addr{}, err
	}
	if !best.IsValid() {
		return netip.Addr{}, errNoGateway
	}
	return best, nil
}

// parseRouteGet reads `route -n get default` (macOS, BSD) for its gateway line.
func parseRouteGet(out string) (netip.Addr, error) {
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(k) != "gateway" {
			continue
		}
		gw, err := netip.ParseAddr(strings.TrimSpace(v))
		if err != nil {
			return netip.Addr{}, fmt.Errorf("gateway %q: %w", strings.TrimSpace(v), err)
		}
		return gw, nil
	}
	return netip.Addr{}, errNoGateway
}
