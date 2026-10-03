package reach

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

func init() { ssdpListen = 300 * time.Millisecond }

// fakeSSDP answers M-SEARCH on a loopback UDP port with the LOCATIONs it is
// given, one answer per LOCATION per search.
type fakeSSDP struct {
	conn      *net.UDPConn
	locations []string
	st        string
}

func newFakeSSDP(t *testing.T, st string, locations ...string) *fakeSSDP {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSSDP{conn: conn, locations: locations, st: st}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			req := string(buf[:n])
			if !strings.HasPrefix(req, "M-SEARCH * HTTP/1.1\r\n") || !strings.Contains(req, "ST: "+f.st+"\r\n") {
				continue
			}
			for _, loc := range f.locations {
				reply := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=120\r\nST: " + f.st + "\r\nUSN: uuid:1::" + f.st + "\r\nLOCATION: " + loc + "\r\nSERVER: fake/1.0 UPnP/1.1\r\n\r\n"
				_, _ = conn.WriteToUDP([]byte(reply), from)
			}
		}
	}()
	return f
}

func (f *fakeSSDP) addr() string { return f.conn.LocalAddr().String() }

// fakeIGD serves a device description and the WAN connection SOAP actions.
type fakeIGD struct {
	srv      *httptest.Server
	services []string // service types in the description, in order
	external string
	urlBase  bool

	mu        sync.Mutex
	mappings  map[uint16]fakeIGDMapping // external port → mapping
	conflicts map[uint16]bool           // external ports held by another machine (718)
	permanent bool                      // 725 on any lease but 0
	refuse    string                    // a UPnP error code for every AddPortMapping
	actions   []string
	bigDesc   bool
}

type fakeIGDMapping struct {
	internal uint16
	client   string
	lease    int
	desc     string
}

func newFakeIGD(t *testing.T, services ...string) *fakeIGD {
	t.Helper()
	f := &fakeIGD{services: services, external: "203.0.113.9", mappings: map[uint16]fakeIGDMapping{}, conflicts: map[uint16]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /rootDesc.xml", f.describe)
	mux.HandleFunc("POST /ctl/{svc}", f.control)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIGD) location() string { return f.srv.URL + "/rootDesc.xml" }

func (f *fakeIGD) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeIGD) describe(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "text/xml")
	if f.bigDesc {
		_, _ = w.Write([]byte(strings.Repeat("x", igdMaxDescription+10)))
		return
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0">`)
	if f.urlBase {
		b.WriteString("<URLBase>" + f.srv.URL + "/</URLBase>")
	}
	b.WriteString(`<device><deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType><friendlyName>Fake Router</friendlyName>` +
		`<deviceList><device><deviceType>urn:schemas-upnp-org:device:WANDevice:1</deviceType>` +
		`<deviceList><device><deviceType>urn:schemas-upnp-org:device:WANConnectionDevice:1</deviceType><serviceList>`)
	for i, s := range f.services {
		fmt.Fprintf(&b, `<service><serviceType>%s</serviceType><serviceId>urn:upnp-org:serviceId:svc%d</serviceId><controlURL>/ctl/svc%d</controlURL><eventSubURL>/evt/svc%d</eventSubURL><SCPDURL>/svc%d.xml</SCPDURL></service>`, s, i, i, i, i)
	}
	b.WriteString(`</serviceList></device></deviceList></device></deviceList></device></root>`)
	_, _ = io.WriteString(w, b.String())
}

func (f *fakeIGD) control(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	action := strings.Trim(strings.SplitN(r.Header.Get("SOAPAction"), "#", 2)[1], `"`)
	svc := strings.Trim(strings.SplitN(r.Header.Get("SOAPAction"), "#", 2)[0], `"`)
	f.actions = append(f.actions, action)
	fault := func(code, desc string) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>%s</errorCode><errorDescription>%s</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`, code, desc)
	}
	ok := func(inner string) {
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:%sResponse xmlns:u="%s">%s</u:%sResponse></s:Body></s:Envelope>`, action, svc, inner, action)
	}
	ext := uint16(atoi(soapValue(body, "NewExternalPort")))
	switch action {
	case "GetExternalIPAddress":
		ok("<NewExternalIPAddress>" + f.external + "</NewExternalIPAddress>")
	case "AddPortMapping":
		if f.refuse != "" {
			fault(f.refuse, "Refused")
			return
		}
		lease := atoi(soapValue(body, "NewLeaseDuration"))
		if f.permanent && lease != 0 {
			fault("725", "OnlyPermanentLeasesSupported")
			return
		}
		if f.conflicts[ext] {
			fault("718", "ConflictInMappingEntry")
			return
		}
		f.mappings[ext] = fakeIGDMapping{internal: uint16(atoi(soapValue(body, "NewInternalPort"))), client: soapValue(body, "NewInternalClient"), lease: lease, desc: soapValue(body, "NewPortMappingDescription")}
		ok("")
	case "DeletePortMapping":
		if _, has := f.mappings[ext]; !has {
			fault("714", "NoSuchEntryInArray")
			return
		}
		delete(f.mappings, ext)
		ok("")
	default:
		fault("401", "Invalid Action")
	}
}

func atoi(s string) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func openFake(t *testing.T, f *fakeIGD) *igd {
	t.Helper()
	ssdp := newFakeSSDP(t, igdDevice1, f.location())
	cands, err := discoverIGD(context.Background(), ssdp.addr())
	if err != nil {
		t.Fatal(err)
	}
	d, err := openIGD(context.Background(), lanClient(), cands[0])
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDiscoverFindsTheGateway(t *testing.T) {
	f := newFakeIGD(t, svcWANIP1)
	ssdp := newFakeSSDP(t, igdDevice1, f.location())
	start := time.Now()
	cands, err := discoverIGD(context.Background(), ssdp.addr())
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].Location.String() != f.location() || cands[0].ST != igdDevice1 {
		t.Fatalf("candidates %+v", cands)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("discovery took %s", d)
	}
}

func TestDiscoverIgnoresAPublicLocation(t *testing.T) {
	ssdp := newFakeSSDP(t, igdDevice2, "http://203.0.113.1/rootDesc.xml", "http://127.0.0.2:1900/desc.xml", "http://router.example/desc.xml", "https://127.0.0.1:1/desc.xml")
	_, err := discoverIGD(context.Background(), ssdp.addr())
	if !errors.Is(err, errNoIGD) {
		t.Fatalf("err = %v, want no gateway: a LOCATION off the answering address or outside private space must be dropped", err)
	}
}

func TestIGDAddsRenewsAndDeletesTheMapping(t *testing.T) {
	f := newFakeIGD(t, svcWANIP1)
	d := openFake(t, f)
	ip, err := d.externalIP(context.Background())
	if err != nil || ip != netip.MustParseAddr("203.0.113.9") {
		t.Fatalf("external ip %s, %v", ip, err)
	}
	m, err := d.mapTCP(context.Background(), 8443, 443)
	if err != nil {
		t.Fatal(err)
	}
	if m.Method != MethodUPnP || m.ExternalPort != 443 || m.InternalPort != 8443 || m.Lifetime != igdLease {
		t.Fatalf("mapping %+v", m)
	}
	f.mu.Lock()
	got := f.mappings[443]
	f.mu.Unlock()
	if got.internal != 8443 || got.client != "127.0.0.1" || got.lease != 3600 || got.desc != igdDescription {
		t.Fatalf("gateway holds %+v", got)
	}
	if _, err := d.mapTCP(context.Background(), 8443, 443); err != nil {
		t.Fatalf("renewal: %v", err)
	}
	if err := d.unmapTCP(context.Background(), 443); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	_, still := f.mappings[443]
	actions := strings.Join(f.actions, " ")
	f.mu.Unlock()
	if still {
		t.Fatal("still mapped after delete")
	}
	if actions != "GetExternalIPAddress AddPortMapping AddPortMapping DeletePortMapping" {
		t.Fatalf("actions %q", actions)
	}
}

func TestIGDPrefersWANIPConnection2(t *testing.T) {
	f := newFakeIGD(t, svcWANPPP1, svcWANIP1, svcWANIP2)
	d := openFake(t, f)
	if d.serviceType != svcWANIP2 || !strings.HasSuffix(d.control.Path, "/ctl/svc2") {
		t.Fatalf("picked %s at %s", d.serviceType, d.control)
	}
	f2 := newFakeIGD(t, svcWANPPP1)
	f2.urlBase = true
	if d := openFake(t, f2); d.serviceType != svcWANPPP1 {
		t.Fatalf("picked %s", d.serviceType)
	}
	f3 := newFakeIGD(t, "urn:schemas-upnp-org:service:Layer3Forwarding:1")
	ssdp := newFakeSSDP(t, igdDevice1, f3.location())
	cands, _ := discoverIGD(context.Background(), ssdp.addr())
	if _, err := openIGD(context.Background(), lanClient(), cands[0]); !errors.Is(err, errNoWANService) {
		t.Fatalf("err = %v", err)
	}
}

func TestIGDTriesAnotherPortOnConflict(t *testing.T) {
	f := newFakeIGD(t, svcWANIP2)
	f.set(func() { f.conflicts[443] = true })
	d := openFake(t, f)
	m, err := d.mapTCP(context.Background(), 8443, 443)
	if err != nil {
		t.Fatal(err)
	}
	if m.ExternalPort != 8443 {
		t.Fatalf("external port %d, want 8443 as the next candidate", m.ExternalPort)
	}
	f.set(func() { f.conflicts[8443] = true; f.conflicts[443] = true })
	m, err = d.mapTCP(context.Background(), 8443, 443)
	if err != nil {
		t.Fatal(err)
	}
	if m.ExternalPort < 40000 || m.ExternalPort >= 60000 {
		t.Fatalf("external port %d, want a random high port", m.ExternalPort)
	}
	f.set(func() { f.refuse = "606" })
	if _, err := d.mapTCP(context.Background(), 8443, 443); err == nil || !strings.Contains(err.Error(), "turned off") {
		t.Fatalf("err = %v, want the refusal named", err)
	}
}

func TestIGDAsksForAPermanentLeaseWhenToldTo(t *testing.T) {
	f := newFakeIGD(t, svcWANIP1)
	f.set(func() { f.permanent = true })
	d := openFake(t, f)
	m, err := d.mapTCP(context.Background(), 8443, 443)
	if err != nil {
		t.Fatal(err)
	}
	if m.Lifetime != 0 || !d.permanent {
		t.Fatalf("mapping %+v permanent=%v", m, d.permanent)
	}
}

func TestIGDBoundsTheDescription(t *testing.T) {
	f := newFakeIGD(t, svcWANIP1)
	f.set(func() { f.bigDesc = true })
	ssdp := newFakeSSDP(t, igdDevice1, f.location())
	cands, err := discoverIGD(context.Background(), ssdp.addr())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := openIGD(context.Background(), lanClient(), cands[0]); !errors.Is(err, errDescriptionBig) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseSSDPReplyWithoutTheClosingLine(t *testing.T) {
	c, ok := parseSSDPReply([]byte("HTTP/1.1 200 OK\r\nST: "+igdDevice1+"\r\nLOCATION: http://127.0.0.1:5000/desc.xml"), netip.MustParseAddr("127.0.0.1"))
	if !ok || c.Location.Host != "127.0.0.1:5000" {
		t.Fatalf("%+v %v", c, ok)
	}
	if _, ok := parseSSDPReply([]byte("HTTP/1.1 404 Not Found\r\n\r\n"), netip.MustParseAddr("127.0.0.1")); ok {
		t.Fatal("a non-200 answer must be dropped")
	}
}
