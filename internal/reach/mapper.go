package reach

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// PortMap is one port to open on the gateway: External on the public
// address, Internal on this machine.
type PortMap struct {
	External uint16
	Internal uint16
}

// Options configure a Mapper.
type Options struct {
	// Mode is off, auto or manual.
	Mode string
	// Ports are the mappings auto makes, or, in manual, the ports the person
	// forwarded (only the first's External names the public URL). Auto with
	// no ports only discovers the address.
	Ports []PortMap
	// STUNServer answers the public address ("stun:host:port" or host:port).
	STUNServer string
	// Scheme of the public URL the status reports: https unless told otherwise.
	Scheme string
	// Lease asked of the gateway; renewed at half of it. One hour unless set.
	Lease time.Duration
	// SSDPAddr is where M-SEARCH goes: the SSDP group unless a test gives a
	// loopback address.
	SSDPAddr string
	// Gateway is used instead of the default route's gateway when set, with
	// GatewayPort (5351 unless set) for PCP and NAT-PMP.
	Gateway     netip.Addr
	GatewayPort uint16
	// Verify checks the public URL once mapped; nil leaves Verified empty.
	Verify func(ctx context.Context, publicURL string) string
	// Log receives what happens; nil discards.
	Log *slog.Logger
	// Retry is the wait after a failed attempt (30 s, doubling to RetryMax
	// 5 min, unless set); STUNEvery the address re-check (5 min unless set).
	Retry, RetryMax, STUNEvery time.Duration
}

// Mapper keeps the public address known and the ports mapped.
type Mapper struct {
	o   Options
	log *slog.Logger

	mu       sync.Mutex
	status   Status
	onAddr   []func(netip.Addr)
	onChange []func(Status)
	mappings []liveMapping
	igd      *igd
	closed   bool
}

type liveMapping struct {
	port PortMap
	m    Mapping
}

// New makes a Mapper; Run drives it.
func New(o Options) *Mapper {
	if o.Scheme == "" {
		o.Scheme = "https"
	}
	if o.Lease <= 0 {
		o.Lease = igdLease
	}
	if o.SSDPAddr == "" {
		o.SSDPAddr = ssdpMulticast
	}
	if o.GatewayPort == 0 {
		o.GatewayPort = gatewayPort
	}
	if o.Retry <= 0 {
		o.Retry = 30 * time.Second
	}
	if o.RetryMax <= 0 {
		o.RetryMax = 5 * time.Minute
	}
	if o.STUNEvery <= 0 {
		o.STUNEvery = 5 * time.Minute
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if o.Mode == "" {
		o.Mode = ModeOff
	}
	return &Mapper{o: o, log: log, status: Status{Mode: o.Mode}}
}

// Status is what the mapper knows now.
func (m *Mapper) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// OnAddress registers fn, called (on the mapper's goroutine) when the
// public address becomes known or changes; a certificate manager subscribes.
func (m *Mapper) OnAddress(fn func(netip.Addr)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onAddr = append(m.onAddr, fn)
}

// OnChange registers fn, called (on the mapper's goroutine) after every
// change of the status: the address, a mapping made or lost, a failure.
func (m *Mapper) OnChange(fn func(Status)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = append(m.onChange, fn)
}

// changed tells the OnChange subscribers the status now.
func (m *Mapper) changed() {
	m.mu.Lock()
	fns := make([]func(Status), len(m.onChange))
	copy(fns, m.onChange)
	st := m.status
	m.mu.Unlock()
	for _, fn := range fns {
		fn(st)
	}
}

// Run drives the mapper until ctx ends: the address by STUN, the mappings,
// their renewal at half the lease, the address re-checked every STUNEvery,
// a failed attempt retried after Retry (doubling to RetryMax). It returns
// once ctx ends; Close deletes the mappings.
func (m *Mapper) Run(ctx context.Context) {
	if m.o.Mode == ModeOff {
		return
	}
	retry := m.o.Retry
	for {
		wait, err := m.attempt(ctx)
		m.changed()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			m.log.Warn("reach: "+err.Error(), "retryIn", retry.String())
			wait = retry
			retry = min(retry*2, m.o.RetryMax)
		} else {
			retry = m.o.Retry
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// attempt brings the state up to date once and says how long to wait before
// the next: the renewal of the soonest mapping or the next address check.
func (m *Mapper) attempt(ctx context.Context) (time.Duration, error) {
	ap, err := PublicAddr(ctx, m.o.STUNServer)
	if err != nil {
		m.fail("the public address is unknown: " + err.Error())
		return 0, err
	}
	ip := ap.Addr()
	changed := m.setAddress(ip)
	if changed {
		m.log.Info("reach: public address", "ip", ip.String())
	}
	if m.o.Mode == ModeManual || len(m.o.Ports) == 0 {
		m.setManual(ip)
		return m.o.STUNEvery, nil
	}
	if err := m.ensureMapped(ctx, ip); err != nil {
		return 0, err
	}
	return m.nextWait(), nil
}

// ensureMapped maps or renews every port, UPnP first, then PCP, then NAT-PMP.
func (m *Mapper) ensureMapped(ctx context.Context, ip netip.Addr) error {
	m.mu.Lock()
	have := len(m.mappings) == len(m.o.Ports) && len(m.mappings) > 0
	method := ""
	if have {
		method = m.mappings[0].m.Method
	}
	m.mu.Unlock()
	if have {
		if err := m.renew(ctx); err == nil {
			return nil
		} else {
			m.log.Warn("reach: renewal failed, mapping again", "method", method, "err", err.Error())
			m.mu.Lock()
			m.mappings, m.igd = nil, nil
			m.mu.Unlock()
		}
	}
	gw := m.o.Gateway
	if !gw.IsValid() {
		g, err := defaultGateway()
		if err != nil {
			m.fail("no gateway to ask: " + err.Error())
			return err
		}
		gw = g
	}
	var errs []error
	for _, try := range []func(context.Context, netip.Addr) ([]liveMapping, *igd, error){m.mapUPnP, m.mapPCP(gw), m.mapNATPMP(gw)} {
		maps, dev, err := try(ctx, ip)
		if err == nil {
			m.mu.Lock()
			m.mappings, m.igd = maps, dev
			m.mu.Unlock()
			m.setMapped(ip, maps)
			m.verify(ctx)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		errs = append(errs, err)
	}
	err := errors.Join(errs...)
	m.fail("no gateway mapped the port (UPnP, PCP and NAT-PMP were tried): " + err.Error())
	return err
}

func (m *Mapper) mapUPnP(ctx context.Context, ip netip.Addr) ([]liveMapping, *igd, error) {
	cands, err := discoverIGD(ctx, m.o.SSDPAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("upnp: %w", err)
	}
	client := lanClient()
	var last error
	for _, c := range cands {
		dev, err := openIGD(ctx, client, c)
		if err != nil {
			last = err
			continue
		}
		if ext, err := dev.externalIP(ctx); err == nil && ext.IsValid() && ext != ip && privateAddr(ext) {
			last = fmt.Errorf("the router's external address %s is private: it sits behind another NAT (double NAT or carrier-grade NAT), so a mapping on it cannot be reached from the internet", ext)
			continue
		}
		var out []liveMapping
		for _, p := range m.o.Ports {
			mp, err := dev.mapTCP(ctx, p.Internal, p.External)
			if err != nil {
				for _, done := range out {
					_ = dev.unmapTCP(ctx, done.m.ExternalPort)
				}
				last = err
				out = nil
				break
			}
			out = append(out, liveMapping{port: p, m: mp})
		}
		if out != nil {
			return out, dev, nil
		}
	}
	return nil, nil, fmt.Errorf("upnp: %w", last)
}

func (m *Mapper) mapPCP(gw netip.Addr) func(context.Context, netip.Addr) ([]liveMapping, *igd, error) {
	return func(ctx context.Context, ip netip.Addr) ([]liveMapping, *igd, error) {
		c := pcp{addr: netip.AddrPortFrom(gw, m.o.GatewayPort), rto: pcpRTO}
		var out []liveMapping
		for _, p := range m.o.Ports {
			mp, err := c.mapTCP(ctx, newNonce(), p.Internal, p.External, m.o.Lease)
			if err != nil {
				for _, done := range out {
					_ = c.unmapTCP(ctx, done.m.nonce, done.m.InternalPort)
				}
				return nil, nil, err
			}
			if mp.ExternalIP.IsValid() && mp.ExternalIP != ip && privateAddr(mp.ExternalIP) {
				_ = c.unmapTCP(ctx, mp.nonce, mp.InternalPort)
				return nil, nil, fmt.Errorf("pcp: the router's external address %s is private: it sits behind another NAT, so a mapping on it cannot be reached from the internet", mp.ExternalIP)
			}
			out = append(out, liveMapping{port: p, m: mp})
		}
		return out, nil, nil
	}
}

func (m *Mapper) mapNATPMP(gw netip.Addr) func(context.Context, netip.Addr) ([]liveMapping, *igd, error) {
	return func(ctx context.Context, ip netip.Addr) ([]liveMapping, *igd, error) {
		c := natpmp{addr: netip.AddrPortFrom(gw, m.o.GatewayPort), rto: pmpRTO}
		ext, err := c.externalAddress(ctx)
		if err != nil {
			return nil, nil, err
		}
		if ext.IsValid() && ext != ip && privateAddr(ext) {
			return nil, nil, fmt.Errorf("nat-pmp: the router's external address %s is private: it sits behind another NAT, so a mapping on it cannot be reached from the internet", ext)
		}
		var out []liveMapping
		for _, p := range m.o.Ports {
			mp, err := c.mapTCP(ctx, p.Internal, p.External, m.o.Lease)
			if err != nil {
				for _, done := range out {
					_ = c.unmapTCP(ctx, done.m.InternalPort)
				}
				return nil, nil, err
			}
			out = append(out, liveMapping{port: p, m: mp})
		}
		return out, nil, nil
	}
}

// renew asks the gateway again for every live mapping, by its method.
func (m *Mapper) renew(ctx context.Context) error {
	m.mu.Lock()
	maps := append([]liveMapping(nil), m.mappings...)
	dev := m.igd
	m.mu.Unlock()
	for i, lm := range maps {
		var err error
		switch lm.m.Method {
		case MethodUPnP:
			if dev == nil {
				return errors.New("upnp: the gateway is gone")
			}
			if dev.permanent {
				continue
			}
			_, err = dev.addPortMapping(ctx, lm.m.ExternalPort, lm.m.InternalPort, m.o.Lease)
		case MethodPCP:
			gw := netip.AddrPortFrom(m.gateway(), m.o.GatewayPort)
			var mp Mapping
			mp, err = pcp{addr: gw, rto: pcpRTO}.mapTCP(ctx, lm.m.nonce, lm.m.InternalPort, lm.m.ExternalPort, m.o.Lease)
			if err == nil {
				maps[i].m = mp
			}
		case MethodNATPMP:
			gw := netip.AddrPortFrom(m.gateway(), m.o.GatewayPort)
			var mp Mapping
			mp, err = natpmp{addr: gw, rto: pmpRTO}.mapTCP(ctx, lm.m.InternalPort, lm.m.ExternalPort, m.o.Lease)
			if err == nil {
				maps[i].m = mp
			}
		}
		if err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.mappings = maps
	ip := m.status.ExternalIP
	m.mu.Unlock()
	m.setMapped(ip, maps)
	return nil
}

func (m *Mapper) gateway() netip.Addr {
	if m.o.Gateway.IsValid() {
		return m.o.Gateway
	}
	gw, _ := defaultGateway()
	return gw
}

// nextWait is half the shortest lease, or the address check, whichever is sooner.
func (m *Mapper) nextWait() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	wait := m.o.STUNEvery
	for _, lm := range m.mappings {
		if lm.m.Lifetime > 0 {
			wait = min(wait, lm.m.Lifetime/2)
		}
	}
	return max(wait, time.Second)
}

// Close deletes the mappings; ctx bounds it (callers give a few seconds).
func (m *Mapper) Close(ctx context.Context) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	maps := m.mappings
	dev := m.igd
	m.mappings, m.igd = nil, nil
	m.status.Mapped, m.status.Method, m.status.PublicURL = false, "", ""
	m.mu.Unlock()
	for _, lm := range maps {
		var err error
		switch lm.m.Method {
		case MethodUPnP:
			if dev != nil {
				err = dev.unmapTCP(ctx, lm.m.ExternalPort)
			}
		case MethodPCP:
			err = pcp{addr: netip.AddrPortFrom(m.gateway(), m.o.GatewayPort), rto: pcpRTO}.unmapTCP(ctx, lm.m.nonce, lm.m.InternalPort)
		case MethodNATPMP:
			err = natpmp{addr: netip.AddrPortFrom(m.gateway(), m.o.GatewayPort), rto: pmpRTO}.unmapTCP(ctx, lm.m.InternalPort)
		}
		if err != nil {
			m.log.Warn("reach: could not delete the mapping", "method", lm.m.Method, "externalPort", lm.m.ExternalPort, "err", err.Error())
		}
	}
}

func (m *Mapper) setAddress(ip netip.Addr) (changed bool) {
	m.mu.Lock()
	changed = m.status.ExternalIP != ip
	m.status.ExternalIP = ip
	m.status.Err = ""
	fns := m.onAddr
	m.mu.Unlock()
	if changed {
		for _, fn := range fns {
			fn(ip)
		}
	}
	return changed
}

func (m *Mapper) setManual(ip netip.Addr) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Mapped = false
	m.status.Method = ""
	m.status.PublicURL = ""
	if m.o.Mode == ModeManual && len(m.o.Ports) > 0 {
		p := m.o.Ports[0]
		m.status.Method = MethodManual
		m.status.ExternalPort, m.status.ListenPort = p.External, p.Internal
		m.status.PublicURL = m.publicURL(ip, p.External)
	}
}

func (m *Mapper) setMapped(ip netip.Addr, maps []liveMapping) {
	m.mu.Lock()
	defer m.mu.Unlock()
	first := maps[0]
	m.status.Mapped = true
	m.status.Method = first.m.Method
	m.status.ExternalPort, m.status.ListenPort = first.m.ExternalPort, first.m.InternalPort
	m.status.PublicURL = m.publicURL(ip, first.m.ExternalPort)
	m.status.RenewedAt = time.Now().UTC()
	m.status.ExpiresAt = time.Time{}
	if first.m.Lifetime > 0 {
		m.status.ExpiresAt = m.status.RenewedAt.Add(first.m.Lifetime)
	}
	m.status.Err = ""
}

func (m *Mapper) fail(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Err = msg
	m.status.Mapped = false
	m.status.Method = ""
	m.status.PublicURL = ""
}

func (m *Mapper) verify(ctx context.Context) {
	if m.o.Verify == nil {
		return
	}
	m.mu.Lock()
	u := m.status.PublicURL
	m.mu.Unlock()
	if u == "" {
		return
	}
	v := m.o.Verify(ctx, u)
	m.mu.Lock()
	m.status.Verified = v
	m.mu.Unlock()
}

func (m *Mapper) publicURL(ip netip.Addr, port uint16) string {
	host := ip.String()
	if ip.Is6() {
		host = "[" + host + "]"
	}
	if (m.o.Scheme == "https" && port == 443) || (m.o.Scheme == "http" && port == 80) {
		return m.o.Scheme + "://" + host
	}
	return m.o.Scheme + "://" + net.JoinHostPort(ip.String(), strconv.Itoa(int(port)))
}
