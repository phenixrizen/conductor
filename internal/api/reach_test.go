package api

import (
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/phenixrizen/conductor/internal/certs"
	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/reach"
)

type stubReach struct{ st reach.Status }

func (s stubReach) Status() reach.Status { return s.st }

type stubCerts struct {
	ready  bool
	tokens map[string]string
}

func (s stubCerts) Ready() bool { return s.ready }
func (s stubCerts) Status() certs.Status {
	return certs.Status{Mode: certs.ModeACME, Ready: s.ready, Identifiers: []string{"203.0.113.9"}, Challenge: certs.ChallengeTLSALPN}
}
func (s stubCerts) HTTP01(token string) (string, bool) {
	v, ok := s.tokens[token]
	return v, ok
}

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
	e.srv.SetReach(stubReach{mapped()}, stubCerts{ready: false})
	_, out = e.do("GET", "/api/reach", adminToken, nil)
	if out["mode"] != "auto" || out["mapped"] != true || out["method"] != "upnp" || out["publicUrl"] != "https://203.0.113.9" || out["externalIp"] != "203.0.113.9" || out["externalPort"] != float64(443) {
		t.Fatalf("%v", out)
	}
	tls, _ := out["tls"].(map[string]any)
	if tls == nil || tls["ready"] != false || tls["mode"] != "acme" || tls["challenge"] != "tls-alpn-01" {
		t.Fatalf("tls %v", out["tls"])
	}
}

func TestACMEChallengeRouteAnswersOnlyAnOpenOrder(t *testing.T) {
	e := newTestEnv(t, nil)
	get := func(token string) (int, string) {
		t.Helper()
		resp, err := e.client.Get(e.http.URL + "/.well-known/acme-challenge/" + token)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if status, _ := get("abc"); status != http.StatusNotFound {
		t.Fatalf("without a manager: %d", status)
	}
	e.srv.SetReach(nil, stubCerts{tokens: map[string]string{"tok123": "tok123.thumbprint"}})
	if status, body := get("tok123"); status != http.StatusOK || body != "tok123.thumbprint" {
		t.Fatalf("open order: %d %q", status, body)
	}
	if status, _ := get("other"); status != http.StatusNotFound {
		t.Fatalf("unknown token: %d", status)
	}
	if status, _ := get(strings.Repeat("x", 129)); status != http.StatusNotFound {
		t.Fatalf("a long token: %d", status)
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
	e.srv.SetReach(stubReach{mapped()}, stubCerts{ready: true})
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "https://203.0.113.9/join/") {
		t.Fatalf("mapped and ready: %s", u)
	}
	manual := mapped()
	manual.Mapped, manual.Method, manual.PublicURL = false, reach.MethodManual, "https://203.0.113.9:8443"
	e.srv.SetReach(stubReach{manual}, stubCerts{ready: true})
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "https://203.0.113.9:8443/join/") {
		t.Fatalf("manual and ready: %s", u)
	}
}

func TestShareLinksIgnoreAMappingWithoutACertificate(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "http://localhost:8080" })
	id := e.createSession("cat")
	host := strings.TrimPrefix(e.http.URL, "http://")
	e.srv.SetReach(stubReach{mapped()}, stubCerts{ready: false})
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "http://"+host+"/join/") {
		t.Fatalf("mapped, no certificate: %s", u)
	}
	e.srv.SetReach(stubReach{mapped()}, nil)
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "http://"+host+"/join/") {
		t.Fatalf("mapped, no TLS at all: %s", u)
	}
	unmapped := mapped()
	unmapped.Mapped, unmapped.Method, unmapped.PublicURL = false, "", ""
	e.srv.SetReach(stubReach{unmapped}, stubCerts{ready: true})
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "http://"+host+"/join/") {
		t.Fatalf("address known but nothing mapped: %s", u)
	}
}

func TestExplicitPublicURLWinsOverTheDiscoveredAddress(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "https://team.example.net" })
	id := e.createSession("cat")
	e.srv.SetReach(stubReach{mapped()}, stubCerts{ready: true})
	if u := e.linkURL(t, id); !strings.HasPrefix(u, "https://team.example.net/join/") {
		t.Fatalf("explicit publicUrl: %s", u)
	}
}

func TestNotifyURLNeverTakesTheDiscoveredBase(t *testing.T) {
	e := newTestEnv(t, func(c *config.Config) { c.PublicURL = "http://localhost:8080" })
	e.srv.SetReach(stubReach{mapped()}, stubCerts{ready: true})
	if base := e.srv.publicBase(nil); base != "https://203.0.113.9" {
		t.Fatalf("publicBase %s", base)
	}
	if base := e.srv.notifyBase(); base != "http://localhost:8080" {
		t.Fatalf("notifyBase %s", base)
	}
}
