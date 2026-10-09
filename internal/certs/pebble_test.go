//go:build pebble

package certs

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Pebble is Let's Encrypt's test CA. CONDUCTOR_PEBBLE names the binary
// (else `pebble` on the PATH; `go install
// github.com/letsencrypt/pebble/v2/cmd/pebble@v2.10.1`). The test starts
// one with the validation sleeps off, a fake DNS that answers 127.0.0.1 for
// every name, and orders certificates through the manager the way the
// server does: for an IP address over tls-alpn-01 (what the public-address
// path does), for a name over tls-alpn-01 and over http-01, and a renewal.
func pebbleBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("CONDUCTOR_PEBBLE"); p != "" {
		return p
	}
	if p, err := exec.LookPath("pebble"); err == nil {
		return p
	}
	t.Skip("no pebble: set CONDUCTOR_PEBBLE or put it on the PATH")
	return ""
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// fakeDNS answers every A question with 127.0.0.1 and everything else with
// an empty answer, over UDP and TCP (Pebble's resolver dials TCP), so
// Pebble resolves conductor.test to this machine.
func fakeDNS(t *testing.T) string {
	t.Helper()
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := udp.LocalAddr().(*net.UDPAddr).Port
	tcp, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { udp.Close(); tcp.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := udp.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if out := dnsAnswer(buf[:n]); out != nil {
				_, _ = udp.WriteToUDP(out, from)
			}
		}
	}()
	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				var hdr [2]byte
				for {
					if _, err := io.ReadFull(c, hdr[:]); err != nil {
						return
					}
					msg := make([]byte, int(hdr[0])<<8|int(hdr[1]))
					if _, err := io.ReadFull(c, msg); err != nil {
						return
					}
					out := dnsAnswer(msg)
					if out == nil {
						return
					}
					_, _ = c.Write(append([]byte{byte(len(out) >> 8), byte(len(out))}, out...))
				}
			}()
		}
	}()
	return udp.LocalAddr().String()
}

func dnsAnswer(msg []byte) []byte {
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		return nil
	}
	qs, _ := p.AllQuestions()
	b := dnsmessage.NewBuilder(nil, dnsmessage.Header{ID: h.ID, Response: true, Authoritative: true, RecursionAvailable: true})
	_ = b.StartQuestions()
	for _, q := range qs {
		_ = b.Question(q)
	}
	_ = b.StartAnswers()
	for _, q := range qs {
		if q.Type == dnsmessage.TypeA {
			_ = b.AResource(dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}})
		}
	}
	out, err := b.Finish()
	if err != nil {
		return nil
	}
	return out
}

// lockedBuffer collects Pebble's output while the test reads it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type pebble struct {
	dir     string
	httpCli *http.Client
	root    *x509.CertPool
	// The ports Pebble validates against are the test's own: bound before
	// Pebble starts and held, so no other socket can take them meanwhile
	// (a port picked, closed and bound again later once lost that race in
	// CI: "bind: address already in use").
	tlsLn  net.Listener
	httpLn net.Listener
}

// listenLocal binds a port of its own on 127.0.0.1 and keeps it until the
// test ends.
func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}

func portOf(ln net.Listener) int { return ln.Addr().(*net.TCPAddr).Port }

// pebbleStartTries bounds the starts with fresh ports when Pebble finds one
// of its own two taken by the time it binds it.
const pebbleStartTries = 5

// pebblePorts picks Pebble's directory and management ports for one start;
// a test replaces it to hand Pebble a taken port.
var pebblePorts = func(t *testing.T) (listen, mgmt int) { return freePort(t), freePort(t) }

func startPebble(t *testing.T) *pebble {
	t.Helper()
	bin := pebbleBinary(t)
	certPEM, _ := os.ReadFile(filepath.Join("testdata", "pebble", "https-cert.pem"))
	trust := x509.NewCertPool()
	trust.AppendCertsFromPEM(certPEM)
	cli := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: trust}}}
	p := &pebble{httpCli: cli, tlsLn: listenLocal(t), httpLn: listenLocal(t)}
	dns := fakeDNS(t)
	for try := 1; ; try++ {
		mgmt, ok, out := p.start(t, bin, dns)
		if ok {
			p.fetchRoot(t, mgmt)
			return p
		}
		// Pebble's own two ports are bound by Pebble, which the test cannot
		// hold for it: a start that lost one to another socket is retried on
		// fresh ones; anything else fails at once.
		if try == pebbleStartTries || !strings.Contains(out, "address already in use") {
			t.Fatalf("pebble did not come up (try %d):\n%s", try, out)
		}
		t.Logf("pebble lost a port to another socket, starting again (try %d)", try)
	}
}

// start runs Pebble once with fresh ports for its directory and management
// listeners, the test's held ports for the challenges. ok says its
// directory answered; out is what it printed.
func (p *pebble) start(t *testing.T, bin, dns string) (mgmt int, ok bool, out string) {
	t.Helper()
	dir := t.TempDir()
	listen, mgmt := pebblePorts(t)
	cfg := map[string]any{"pebble": map[string]any{
		"listenAddress": "127.0.0.1:" + strconv.Itoa(listen), "managementListenAddress": "127.0.0.1:" + strconv.Itoa(mgmt),
		"certificate": filepath.Join("testdata", "pebble", "https-cert.pem"), "privateKey": filepath.Join("testdata", "pebble", "https-key.pem"),
		"httpPort": portOf(p.httpLn), "tlsPort": portOf(p.tlsLn), "ocspResponderURL": "", "externalAccountBindingRequired": false,
		"retryAfter": map[string]int{"authz": 1, "order": 1}, "keyAlgorithm": "ecdsa",
		"profiles": map[string]any{
			"default":    map[string]any{"description": "ninety days", "validityPeriod": 7776000},
			"shortlived": map[string]any{"description": "six days", "validityPeriod": 518400},
		},
	}}
	b, _ := json.Marshal(cfg)
	cfgPath := filepath.Join(dir, "pebble.json")
	if err := os.WriteFile(cfgPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-config", cfgPath, "-dnsserver", dns, "-strict")
	cmd.Env = append(os.Environ(), "PEBBLE_VA_NOSLEEP=1")
	buf := &lockedBuffer{}
	cmd.Stdout, cmd.Stderr = buf, buf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	stop := func() {
		_ = cmd.Process.Kill()
		<-exited
	}
	p.dir = "https://127.0.0.1:" + strconv.Itoa(listen) + "/dir"
	deadline := time.Now().Add(20 * time.Second)
	for {
		select {
		case <-exited:
			return mgmt, false, buf.String()
		default:
		}
		resp, err := p.httpCli.Get(p.dir)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Cleanup(func() {
					stop()
					if t.Failed() {
						t.Logf("pebble output:\n%s", buf.String())
					}
				})
				return mgmt, true, ""
			}
		}
		if time.Now().After(deadline) {
			stop()
			return mgmt, false, buf.String()
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// fetchRoot reads Pebble's root from its management listener.
func (p *pebble) fetchRoot(t *testing.T, mgmt int) {
	t.Helper()
	resp, err := p.httpCli.Get("https://127.0.0.1:" + strconv.Itoa(mgmt) + "/roots/0")
	if err != nil {
		t.Fatal(err)
	}
	rootPEM, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	p.root = x509.NewCertPool()
	if !p.root.AppendCertsFromPEM(rootPEM) {
		t.Fatalf("no root from pebble: %s", rootPEM)
	}
}

// serveTLS runs a TLS listener on the port Pebble validates against, with
// the manager's GetCertificate, until the test ends.
func (p *pebble) serveTLS(t *testing.T, m *Manager) {
	t.Helper()
	tl := tls.NewListener(p.tlsLn, &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1", acmeTLSProto}, GetCertificate: m.GetCertificate})
	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = c.(*tls.Conn).Handshake()
				_ = c.Close()
			}()
		}
	}()
}

// serveHTTP answers the http-01 route on the port Pebble validates against.
func (p *pebble) serveHTTP(t *testing.T, m *Manager) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/acme-challenge/{token}", func(w http.ResponseWriter, r *http.Request) {
		if ka, ok := m.HTTP01(r.PathValue("token")); ok {
			_, _ = w.Write([]byte(ka))
			return
		}
		http.NotFound(w, r)
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(p.httpLn) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func (p *pebble) verify(t *testing.T, m *Manager, id string) *x509.Certificate {
	t.Helper()
	c, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: id, SupportedProtos: []string{"http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(c.Certificate[0])
	inter := x509.NewCertPool()
	for _, der := range c.Certificate[1:] {
		if ic, err := x509.ParseCertificate(der); err == nil {
			inter.AddCert(ic)
		}
	}
	opts := x509.VerifyOptions{Roots: p.root, Intermediates: inter, CurrentTime: time.Now().Add(time.Minute)}
	if ip := net.ParseIP(id); ip == nil {
		opts.DNSName = id
	}
	if _, err := leaf.Verify(opts); err != nil {
		t.Fatalf("the issued certificate does not verify against pebble's root: %v", err)
	}
	if ip := net.ParseIP(id); ip != nil {
		if err := leaf.VerifyHostname(id); err != nil {
			t.Fatalf("ip SAN: %v", err)
		}
	}
	return leaf
}

func TestPebbleIssuesForTheIPAddressOverTLSALPN(t *testing.T) {
	p := startPebble(t)
	cl := &clock{t: time.Now()}
	m, err := New(Options{Dir: t.TempDir(), Identifiers: []string{"127.0.0.1"}, Profile: "shortlived", Challenge: ChallengeTLSALPN, Now: cl.now,
		ACME: &ACMEOptions{Email: "test@conductor.test", Directory: p.dir, Challenge: ChallengeTLSALPN, HTTPClient: p.httpCli}})
	if err != nil {
		t.Fatal(err)
	}
	p.serveTLS(t, m)
	runManager(t, m)
	waitLong(t, "the ip certificate", m.Ready, m)
	leaf := p.verify(t, m, "127.0.0.1")
	if life := leaf.NotAfter.Sub(leaf.NotBefore); life > 7*24*time.Hour || life < 5*24*time.Hour {
		t.Fatalf("shortlived profile: lifetime %s", life)
	}
	st := m.Status()
	if !st.RenewAt.After(cl.now()) || st.RenewAt.After(leaf.NotAfter) {
		t.Fatalf("renewAt %s for notAfter %s", st.RenewAt, leaf.NotAfter)
	}
	// Renewal: the clock passes the renewal time; a new certificate replaces the old.
	cl.advance(st.RenewAt.Sub(cl.now()) + time.Minute)
	m.poke()
	waitLong(t, "the renewal", func() bool {
		c, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "127.0.0.1"})
		if err != nil {
			return false
		}
		l, _ := x509.ParseCertificate(c.Certificate[0])
		return l.SerialNumber.Cmp(leaf.SerialNumber) != 0
	}, m)
	p.verify(t, m, "127.0.0.1")
	// The account and the certificate are kept with private modes.
	for _, f := range []string{"account.key", "account.json"} {
		if fi, err := os.Stat(filepath.Join(m.store.dir, f)); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v", f, err)
		}
	}
}

func TestPebbleIssuesForANameOverTLSALPNAndHTTP01(t *testing.T) {
	p := startPebble(t)
	for _, ch := range []string{ChallengeTLSALPN, ChallengeHTTP} {
		t.Run(ch, func(t *testing.T) {
			// Pebble picks a random profile when none is asked for; "default" is its ninety-day one.
			m, err := New(Options{Dir: t.TempDir(), Identifiers: []string{"conductor.test"}, Challenge: ch, Profile: "default",
				ACME: &ACMEOptions{Directory: p.dir, Challenge: ch, HTTPClient: p.httpCli}})
			if err != nil {
				t.Fatal(err)
			}
			if ch == ChallengeTLSALPN {
				p.serveTLS(t, m)
			} else {
				p.serveHTTP(t, m)
			}
			runManager(t, m)
			waitLong(t, "the certificate", m.Ready, m)
			leaf := p.verify(t, m, "conductor.test")
			if life := leaf.NotAfter.Sub(leaf.NotBefore); life < 80*24*time.Hour {
				t.Fatalf("default profile: lifetime %s", life)
			}
			if st := m.Status(); st.Challenge != ch || st.Identifiers[0] != "conductor.test" {
				t.Fatalf("status %+v", st)
			}
		})
	}
}

func waitLong(t *testing.T, what string, cond func() bool, m *Manager) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s (status %+v)", what, m.Status())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

var _ = fmt.Sprint
var _ = pem.Decode
var _ = strings.TrimSpace
var _ = context.Background

// A port Pebble binds itself can be taken between its pick and Pebble's
// bind (CI once lost the race); the start is tried again on fresh ports.
// Here the first start is handed a port already in use.
func TestPebbleStartsAgainWhenItsPortIsTaken(t *testing.T) {
	taken := listenLocal(t)
	first := true
	orig := pebblePorts
	pebblePorts = func(t *testing.T) (int, int) {
		if first {
			first = false
			return portOf(taken), freePort(t)
		}
		return orig(t)
	}
	t.Cleanup(func() { pebblePorts = orig })
	p := startPebble(t)
	if first || p.root == nil || strings.Contains(p.dir, ":"+strconv.Itoa(portOf(taken))+"/") {
		t.Fatalf("not started again on fresh ports: %s", p.dir)
	}
}
