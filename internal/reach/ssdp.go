package reach

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

// SSDP (UPnP Device Architecture §1.3): an M-SEARCH to the multicast group,
// answered by each root device with the LOCATION of its description.
const (
	ssdpMulticast = "239.255.255.250:1900"
	ssdpMX        = 2 // seconds a device may wait before answering
	ssdpMaxReply  = 4096
	igdDevice1    = "urn:schemas-upnp-org:device:InternetGatewayDevice:1"
	igdDevice2    = "urn:schemas-upnp-org:device:InternetGatewayDevice:2"
)

// igdCandidate is one SSDP answer worth following up.
type igdCandidate struct {
	Location *url.URL
	ST       string
	From     netip.Addr
}

// ssdpListen is how long discovery collects answers (MX plus slack); a test
// shortens it.
var ssdpListen = 2500 * time.Millisecond

var errNoIGD = errors.New("no UPnP gateway answered")

// discoverIGD searches for Internet Gateway Devices (version 2, then 1) at
// multicast (the SSDP group unless a test gives a loopback address) and
// returns the candidates whose LOCATION is safe to fetch: an http URL with an
// IP literal for a host, the same address the answer came from, and that
// address private, link-local or loopback. Anything else is an attacker on
// the network pointing the server somewhere, and is dropped.
func discoverIGD(ctx context.Context, multicast string) ([]igdCandidate, error) {
	ctx, cancel := context.WithTimeout(ctx, ssdpListen+time.Second)
	defer cancel()
	raddr, err := net.ResolveUDPAddr("udp4", multicast)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	for _, st := range []string{igdDevice2, igdDevice1} {
		msg := "M-SEARCH * HTTP/1.1\r\nHOST: " + multicast + "\r\nMAN: \"ssdp:discover\"\r\nMX: " +
			fmt.Sprint(ssdpMX) + "\r\nST: " + st + "\r\n\r\n"
		if _, err := conn.WriteToUDP([]byte(msg), raddr); err != nil {
			return nil, err
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(ssdpListen))
	var out []igdCandidate
	seen := map[string]bool{}
	buf := make([]byte, ssdpMaxReply)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		fromAddr, _ := netip.AddrFromSlice(from.IP)
		c, ok := parseSSDPReply(buf[:n], fromAddr.Unmap())
		if !ok || seen[c.Location.String()] {
			continue
		}
		seen[c.Location.String()] = true
		out = append(out, c)
	}
	if len(out) == 0 {
		if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ctx.Err()
		}
		return nil, errNoIGD
	}
	return out, nil
}

// parseSSDPReply reads one answer. ok is false for anything that is not a
// 200 answer naming an IGD with a safe LOCATION.
func parseSSDPReply(b []byte, from netip.Addr) (igdCandidate, bool) {
	r := textproto.NewReader(bufio.NewReader(strings.NewReader(string(b))))
	status, err := r.ReadLine()
	if err != nil || !strings.HasPrefix(status, "HTTP/1.1 200") && !strings.HasPrefix(status, "HTTP/1.0 200") {
		return igdCandidate{}, false
	}
	// An answer without the closing blank line ends in io.EOF with the
	// headers read so far, which are kept.
	h, err := r.ReadMIMEHeader()
	if err != nil && len(h) == 0 {
		return igdCandidate{}, false
	}
	st := h.Get("St")
	if st != igdDevice1 && st != igdDevice2 {
		return igdCandidate{}, false
	}
	loc, ok := safeLocation(h.Get("Location"), from)
	if !ok {
		return igdCandidate{}, false
	}
	return igdCandidate{Location: loc, ST: st, From: from}, true
}

// safeLocation accepts an http URL whose host is the IP literal the answer
// came from, when that address is private, link-local or loopback.
func safeLocation(raw string, from netip.Addr) (*url.URL, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return nil, false
	}
	host, err := netip.ParseAddr(u.Hostname())
	if err != nil {
		return nil, false
	}
	host = host.Unmap()
	if host != from || !privateAddr(host) {
		return nil, false
	}
	if u.Port() == "" {
		u.Host = net.JoinHostPort(u.Hostname(), "80")
	}
	return u, true
}

// privateAddr reports whether a is one a gateway on this network would have.
func privateAddr(a netip.Addr) bool {
	return a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLoopback()
}

// lanClient is the HTTP client for a gateway: short timeouts, no redirects
// (a redirect could leave the network), no proxy.
func lanClient() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("the gateway redirected, which is refused")
		},
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true},
	}
}
