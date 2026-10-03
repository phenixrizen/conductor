package api

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/reach"
)

type stubReach struct{ st reach.Status }

func (s stubReach) Status() reach.Status { return s.st }

type stubCerts struct{ ready bool }

func (s stubCerts) Ready() bool { return s.ready }

func mapped() reach.Status {
	return reach.Status{Mode: reach.ModeAuto, Mapped: true, Method: reach.MethodUPnP, PublicURL: "https://203.0.113.9",
		ExternalIP: netip.MustParseAddr("203.0.113.9"), ExternalPort: 443, ListenPort: 8443}
}

func (e *testEnv) linkURL(t *testing.T, id string) string {
	t.Helper()
	resp, out := e.do("POST", "/api/sessions/"+id+"/links", adminToken, map[string]any{"role": "view"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("%d %v", resp.StatusCode, out)
	}
	u, _ := out["url"].(string)
	return u
}

func TestReachRouteNeedsAdminAndReportsTheStatus(t *testing.T) {
	e := newTestEnv(t, nil)
	if resp, _ := e.do("GET", "/api/reach", "", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a token: %d", resp.StatusCode)
	}
	resp, out := e.do("GET", "/api/reach", adminToken, nil)
	if resp.StatusCode != http.StatusOK || out["mode"] != "off" || out["mapped"] != false {
		t.Fatalf("before SetReach: %d %v", resp.StatusCode, out)
	}
	e.srv.SetReach(stubReach{mapped()}, stubCerts{false})
	_, out = e.do("GET", "/api/reach", adminToken, nil)
	if out["mode"] != "auto" || out["mapped"] != true || out["method"] != "upnp" || out["publicUrl"] != "https://203.0.113.9" || out["externalIp"] != "203.0.113.9" || out["externalPort"] != float64(443) {
		t.Fatalf("%v", out)
	}
	tls, _ := out["tls"].(map[string]any)
	if tls == nil || tls["ready"] != false {
		t.Fatalf("tls %v", out["tls"])
	}
}

func TestHealthCarriesAnInstance(t *testing.T) {
	e := newTestEnv(t, nil)
	_, out := e.do("GET", "/api/health", "", nil)
	inst, _ := out["instance"].(string)
	if len(inst) != 16 || inst != e.srv.Instance() {
		t.Fatalf("instance %q (server says %q)", inst, e.srv.Instance())
	}
}

func TestShareLinksTakeTheDiscoveredAddressWhenMappedAndTLSReady(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "http://localhost:8080" })
	id := e.createSession("cat")
	e.srv.SetReach(stubReach{mapped()}, stubCerts{true})
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "https://203.0.113.9/join/") {
		t.Fatalf("mapped and ready: %s", u)
	}
	manual := mapped()
	manual.Mapped, manual.Method, manual.PublicURL = false, reach.MethodManual, "https://203.0.113.9:8443"
	e.srv.SetReach(stubReach{manual}, stubCerts{true})
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "https://203.0.113.9:8443/join/") {
		t.Fatalf("manual and ready: %s", u)
	}
}

func TestShareLinksIgnoreAMappingWithoutACertificate(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "http://localhost:8080" })
	id := e.createSession("cat")
	host := strings.TrimPrefix(e.http.URL, "http://")
	e.srv.SetReach(stubReach{mapped()}, stubCerts{false})
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "http://"+host+"/join/") {
		t.Fatalf("mapped, no certificate: %s", u)
	}
	e.srv.SetReach(stubReach{mapped()}, nil)
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "http://"+host+"/join/") {
		t.Fatalf("mapped, no TLS at all: %s", u)
	}
	unmapped := mapped()
	unmapped.Mapped, unmapped.Method, unmapped.PublicURL = false, "", ""
	e.srv.SetReach(stubReach{unmapped}, stubCerts{true})
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "http://"+host+"/join/") {
		t.Fatalf("address known but nothing mapped: %s", u)
	}
}

func TestExplicitPublicURLWinsOverTheDiscoveredAddress(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://team.example.net" })
	id := e.createSession("cat")
	e.srv.SetReach(stubReach{mapped()}, stubCerts{true})
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "https://team.example.net/join/") {
		t.Fatalf("explicit publicUrl: %s", u)
	}
}

func TestNotifyURLNeverTakesTheDiscoveredBase(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "http://localhost:8080" })
	e.srv.SetReach(stubReach{mapped()}, stubCerts{true})
	if base := e.srv.publicBase(nil); base != "https://203.0.113.9" {
		t.Fatalf("publicBase %s", base)
	}
	if base := e.srv.notifyBase(); base != "http://localhost:8080" {
		t.Fatalf("notifyBase %s", base)
	}
}
