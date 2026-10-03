package reach

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// An Internet Gateway Device's description names its services; the WAN
// connection service takes the SOAP actions that map a port (UPnP IGD
// WANIPConnection:1/2, WANPPPConnection:1).
const (
	igdMaxDescription = 256 << 10
	igdMaxSOAPReply   = 64 << 10
	igdLease          = time.Hour
	igdDescription    = "Conductor"

	svcWANIP2  = "urn:schemas-upnp-org:service:WANIPConnection:2"
	svcWANIP1  = "urn:schemas-upnp-org:service:WANIPConnection:1"
	svcWANPPP1 = "urn:schemas-upnp-org:service:WANPPPConnection:1"
)

var (
	errPortConflict   = errors.New("the external port is mapped to another machine")
	errPermanentOnly  = errors.New("the gateway grants permanent leases only")
	errNoWANService   = errors.New("the gateway describes no WAN connection service")
	errDescriptionBig = errors.New("the gateway's description is larger than allowed")
)

// igd is one gateway's WAN connection service.
type igd struct {
	client      *http.Client
	control     *url.URL
	serviceType string
	localIP     netip.Addr // this machine's address on the gateway's network
	permanent   bool       // the gateway refused leases: the mapping must be deleted on close
}

type upnpService struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

type upnpDevice struct {
	DeviceType   string        `xml:"deviceType"`
	FriendlyName string        `xml:"friendlyName"`
	Services     []upnpService `xml:"serviceList>service"`
	Devices      []upnpDevice  `xml:"deviceList>device"`
}

type upnpRoot struct {
	URLBase string     `xml:"URLBase"`
	Device  upnpDevice `xml:"device"`
}

func (d *upnpDevice) services(out *[]upnpService) {
	*out = append(*out, d.Services...)
	for i := range d.Devices {
		d.Devices[i].services(out)
	}
}

// openIGD fetches a candidate's description and picks its best WAN service.
func openIGD(ctx context.Context, client *http.Client, c igdCandidate) (*igd, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Location.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("description %s: HTTP %d", c.Location, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, igdMaxDescription+1))
	if err != nil {
		return nil, err
	}
	if len(body) > igdMaxDescription {
		return nil, errDescriptionBig
	}
	var root upnpRoot
	if err := xml.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("description %s: %w", c.Location, err)
	}
	base := c.Location
	if root.URLBase != "" {
		if u, err := url.Parse(strings.TrimSpace(root.URLBase)); err == nil && u.Scheme == "http" {
			base = u
		}
	}
	var all []upnpService
	root.Device.services(&all)
	for _, want := range []string{svcWANIP2, svcWANIP1, svcWANPPP1} {
		for _, s := range all {
			if strings.TrimSpace(s.ServiceType) != want {
				continue
			}
			ctl, err := base.Parse(strings.TrimSpace(s.ControlURL))
			if err != nil {
				continue
			}
			if _, ok := safeLocation(ctl.String(), c.From); !ok {
				return nil, fmt.Errorf("the control URL %s is not on the gateway", ctl)
			}
			local, err := localIPTo(ctl.Host)
			if err != nil {
				return nil, err
			}
			return &igd{client: client, control: ctl, serviceType: want, localIP: local}, nil
		}
	}
	return nil, errNoWANService
}

// localIPTo is this machine's address on the way to hostport.
func localIPTo(hostport string) (netip.Addr, error) {
	conn, err := net.Dial("udp", hostport)
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()
	a, ok := netip.AddrFromSlice(localIPFor(conn))
	if !ok {
		return netip.Addr{}, errors.New("no local address toward the gateway")
	}
	return a.Unmap(), nil
}

// externalIP asks the gateway for its WAN address.
func (d *igd) externalIP(ctx context.Context) (netip.Addr, error) {
	body, err := d.soap(ctx, "GetExternalIPAddress", nil)
	if err != nil {
		return netip.Addr{}, err
	}
	v := soapValue(body, "NewExternalIPAddress")
	ip, err := netip.ParseAddr(strings.TrimSpace(v))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("the gateway reported no external address (%q)", v)
	}
	return ip, nil
}

// mapTCP maps external → internal on TCP for igdLease. On a conflict (718)
// it tries 8443 and then three random high ports; on a gateway that grants
// permanent leases only (725) it asks for one and remembers to delete it.
func (d *igd) mapTCP(ctx context.Context, internal, external uint16) (Mapping, error) {
	candidates := []uint16{external}
	if external != 8443 {
		candidates = append(candidates, 8443)
	}
	for range 3 {
		candidates = append(candidates, uint16(40000+rand.IntN(20000)))
	}
	var last error
	for _, ext := range candidates {
		m, err := d.addPortMapping(ctx, ext, internal, igdLease)
		if errors.Is(err, errPermanentOnly) {
			d.permanent = true
			m, err = d.addPortMapping(ctx, ext, internal, 0)
		}
		if err == nil {
			return m, nil
		}
		last = err
		if !errors.Is(err, errPortConflict) {
			break
		}
	}
	return Mapping{}, last
}

func (d *igd) addPortMapping(ctx context.Context, external, internal uint16, lease time.Duration) (Mapping, error) {
	args := [][2]string{
		{"NewRemoteHost", ""},
		{"NewExternalPort", strconv.Itoa(int(external))},
		{"NewProtocol", "TCP"},
		{"NewInternalPort", strconv.Itoa(int(internal))},
		{"NewInternalClient", d.localIP.String()},
		{"NewEnabled", "1"},
		{"NewPortMappingDescription", igdDescription},
		{"NewLeaseDuration", strconv.Itoa(int(lease / time.Second))},
	}
	if _, err := d.soap(ctx, "AddPortMapping", args); err != nil {
		return Mapping{}, err
	}
	return Mapping{Method: MethodUPnP, ExternalPort: external, InternalPort: internal, Lifetime: lease}, nil
}

// unmapTCP deletes the mapping of external.
func (d *igd) unmapTCP(ctx context.Context, external uint16) error {
	args := [][2]string{
		{"NewRemoteHost", ""},
		{"NewExternalPort", strconv.Itoa(int(external))},
		{"NewProtocol", "TCP"},
	}
	_, err := d.soap(ctx, "DeletePortMapping", args)
	return err
}

// soap posts one action and returns the response body, or the fault as an
// error named by its UPnP code.
func (d *igd) soap(ctx context.Context, action string, args [][2]string) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:`)
	b.WriteString(action)
	b.WriteString(` xmlns:u="`)
	b.WriteString(d.serviceType)
	b.WriteString(`">`)
	for _, a := range args {
		b.WriteString("<" + a[0] + ">")
		_ = xml.EscapeText(&b, []byte(a[1]))
		b.WriteString("</" + a[0] + ">")
	}
	b.WriteString(`</u:` + action + `></s:Body></s:Envelope>`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.control.String(), &b)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+d.serviceType+"#"+action+`"`)
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, igdMaxSOAPReply))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", action, err)
	}
	if resp.StatusCode == http.StatusOK {
		return body, nil
	}
	if code := soapValue(body, "errorCode"); code != "" {
		desc := strings.TrimSpace(soapValue(body, "errorDescription"))
		switch code {
		case "718":
			return nil, fmt.Errorf("%s: %w", action, errPortConflict)
		case "725":
			return nil, fmt.Errorf("%s: %w", action, errPermanentOnly)
		case "606", "401", "402":
			return nil, fmt.Errorf("%s: the gateway refused (%s %s): port mapping may be turned off", action, code, desc)
		case "729":
			return nil, fmt.Errorf("%s: the gateway refused (%s %s): another mechanism holds the port", action, code, desc)
		}
		return nil, fmt.Errorf("%s: the gateway answered %s %s", action, code, desc)
	}
	return nil, fmt.Errorf("%s: HTTP %d", action, resp.StatusCode)
}

// soapValue returns the text of the first element named name, namespace
// aside, or "".
func soapValue(body []byte, name string) string {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != name {
			continue
		}
		var v string
		if err := dec.DecodeElement(&v, &se); err != nil {
			return ""
		}
		return v
	}
}
