package certs

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenewalIsAThirdOfTheLifetimeForShortLivedCertificates(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	leaf := &x509.Certificate{NotBefore: now, NotAfter: now.Add(6 * 24 * time.Hour)}
	if at := renewAt(leaf, now); !at.Equal(now.Add(4 * 24 * time.Hour)) {
		t.Fatalf("six days: renew at %s", at)
	}
	leaf = &x509.Certificate{NotBefore: now, NotAfter: now.Add(90 * 24 * time.Hour)}
	if at := renewAt(leaf, now); !at.Equal(now.Add(60 * 24 * time.Hour)) {
		t.Fatalf("ninety days: renew at %s", at)
	}
}

func TestRenewalIsThirtyDaysBeforeExpiryForLongCertificates(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	leaf := &x509.Certificate{NotBefore: now, NotAfter: now.Add(365 * 24 * time.Hour)}
	if at := renewAt(leaf, now); !at.Equal(now.Add(335 * 24 * time.Hour)) {
		t.Fatalf("a year: renew at %s", at)
	}
	if at := renewAt(leaf, now.Add(360*24*time.Hour)); !at.Equal(now.Add(360 * 24 * time.Hour)) {
		t.Fatalf("past the renewal time: renew now, got %s", at)
	}
}

func TestManagerIssuesOnceAndRenewsAtTheRenewalTime(t *testing.T) {
	m, fi, cl := newManagerForTest(t, Options{Identifiers: []string{"203.0.113.9"}, Profile: "shortlived"}, 6*24*time.Hour)
	if m.Ready() {
		t.Fatal("ready before any issuance")
	}
	runManager(t, m)
	waitFor(t, "the first issuance", m.Ready)
	st := m.Status()
	if fi.count() != 1 || !st.Ready || st.Mode != ModeACME || st.Identifiers[0] != "203.0.113.9" || st.LastError != "" {
		t.Fatalf("status %+v issued=%d", st, fi.count())
	}
	if want := cl.now().Add(-time.Minute).Add(4 * 24 * time.Hour); !st.RenewAt.Equal(want) {
		t.Fatalf("renewAt %s, want %s", st.RenewAt, want)
	}
	first := m.Status().NotAfter
	cl.advance(3 * 24 * time.Hour)
	m.poke()
	time.Sleep(50 * time.Millisecond)
	if fi.count() != 1 {
		t.Fatal("renewed before the renewal time")
	}
	cl.advance(1*24*time.Hour + time.Minute)
	m.poke()
	waitFor(t, "the renewal", func() bool { return fi.count() == 2 })
	waitFor(t, "the new certificate", func() bool { return m.Status().NotAfter.After(first) })
	if _, ok := m.store.load([]string{"203.0.113.9"}); !ok {
		t.Fatal("the certificate was not kept")
	}
}

func TestManagerReissuesWhenTheIdentifierChanges(t *testing.T) {
	m, fi, _ := newManagerForTest(t, Options{}, 6*24*time.Hour)
	runManager(t, m)
	time.Sleep(50 * time.Millisecond)
	if fi.count() != 0 || m.Ready() {
		t.Fatal("ordered without identifiers")
	}
	m.SetIdentifiers([]string{"203.0.113.9"})
	waitFor(t, "the first issuance", m.Ready)
	m.SetIdentifiers([]string{"198.51.100.4"})
	if m.Ready() {
		t.Fatal("the old address's certificate is still served for the new one")
	}
	waitFor(t, "the second issuance", func() bool { return fi.count() == 2 && m.Ready() })
	if ids := m.Status().Identifiers; len(ids) != 1 || ids[0] != "198.51.100.4" {
		t.Fatalf("identifiers %v", ids)
	}
	// Back to the first address: its certificate is kept and served at once.
	m.SetIdentifiers([]string{"203.0.113.9"})
	if !m.Ready() || fi.count() != 2 {
		t.Fatalf("the kept certificate was not served: ready=%v issued=%d", m.Ready(), fi.count())
	}
	m.SetIdentifiers(nil)
	if m.Ready() {
		t.Fatal("still ready with no identifiers")
	}
}

func TestManagerBacksOffAfterAFailedOrder(t *testing.T) {
	m, fi, cl := newManagerForTest(t, Options{Identifiers: []string{"home.example.net"}, Backoff: time.Hour, BackoffMax: 4 * time.Hour}, 90*24*time.Hour)
	fi.set(func() { fi.fail = errFakeOrder })
	runManager(t, m)
	waitFor(t, "the failure", func() bool { return m.Status().LastError != "" })
	st := m.Status()
	if st.Ready || !strings.Contains(st.LastError, "the CA said no") || !st.NextTry.Equal(cl.now().Add(time.Hour)) {
		t.Fatalf("status %+v", st)
	}
	cl.advance(61 * time.Minute)
	m.poke()
	waitFor(t, "the second failure", func() bool { return m.Status().NextTry.After(cl.now().Add(time.Hour)) })
	if st := m.Status(); !st.NextTry.Equal(cl.now().Add(2 * time.Hour)) {
		t.Fatalf("second backoff: next try %s, now %s", st.NextTry, cl.now())
	}
	fi.set(func() { fi.fail = nil })
	cl.advance(3 * time.Hour)
	m.poke()
	waitFor(t, "the issuance", m.Ready)
	if st := m.Status(); st.LastError != "" || !st.NextTry.IsZero() {
		t.Fatalf("status after success %+v", st)
	}
}

func TestGetCertificateServesTheChallengeOnTheACMEALPN(t *testing.T) {
	m, fi, _ := newManagerForTest(t, Options{Identifiers: []string{"203.0.113.9"}}, 6*24*time.Hour)
	var seen *tls.Certificate
	var seenErr error
	fi.set(func() {
		fi.alpnFor = "203.0.113.9"
		fi.observed = func() {
			// A validator's hello: ALPN acme-tls/1, no SNI (an IP identifier), on the listener's address.
			conn := fakeConn{local: &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 443}}
			seen, seenErr = m.GetCertificate(&tls.ClientHelloInfo{SupportedProtos: []string{acmeTLSProto}, Conn: conn})
			if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "203.0.113.9", SupportedProtos: []string{acmeTLSProto}}); err != nil {
				t.Errorf("by SNI: %v", err)
			}
			if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "other.example", SupportedProtos: []string{acmeTLSProto}}); err != nil {
				t.Errorf("a lone open challenge answers any name: %v", err)
			}
		}
	})
	runManager(t, m)
	waitFor(t, "the issuance", m.Ready)
	if seenErr != nil || seen == nil {
		t.Fatalf("challenge certificate: %v", seenErr)
	}
	leaf, _ := x509.ParseCertificate(seen.Certificate[0])
	if len(leaf.IPAddresses) != 1 || leaf.IPAddresses[0].String() != "203.0.113.9" {
		t.Fatalf("challenge certificate names %v", leaf.IPAddresses)
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{SupportedProtos: []string{acmeTLSProto}}); !errors.Is(err, errNoChallenge) {
		t.Fatalf("after the order: %v", err)
	}
	cur, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "203.0.113.9", SupportedProtos: []string{"http/1.1"}})
	if err != nil || cur.Leaf == nil || cur.Leaf.IPAddresses[0].String() != "203.0.113.9" {
		t.Fatalf("the real certificate: %v %v", err, cur)
	}
}

type fakeConn struct {
	net.Conn
	local net.Addr
}

func (c fakeConn) LocalAddr() net.Addr { return c.local }

func TestGetCertificateServesNothingBeforeTheFirstIssuance(t *testing.T) {
	m, fi, _ := newManagerForTest(t, Options{Identifiers: []string{"203.0.113.9"}}, 6*24*time.Hour)
	fi.set(func() { fi.fail = errFakeOrder })
	runManager(t, m)
	waitFor(t, "the failure", func() bool { return m.Status().LastError != "" })
	if c, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "203.0.113.9"}); !errors.Is(err, errNoCertificate) || c != nil {
		t.Fatalf("got %v, %v: nothing self-signed may be served", c, err)
	}
}

func TestManagerHonoursTheCAsRenewalWindow(t *testing.T) {
	m, fi, cl := newManagerForTest(t, Options{Identifiers: []string{"home.example.net"}}, 90*24*time.Hour)
	start := cl.now().Add(24 * time.Hour)
	fi.set(func() { fi.window = &[2]time.Time{start, start.Add(time.Hour)} })
	runManager(t, m)
	waitFor(t, "the issuance", m.Ready)
	if at := m.Status().RenewAt; at.Before(start) || at.After(start.Add(time.Hour)) {
		t.Fatalf("renewAt %s is outside the CA's window from %s", at, start)
	}
}

func TestStoreKeepsKeysPrivate(t *testing.T) {
	dir := t.TempDir()
	m, _, _ := newManagerForTest(t, Options{Dir: dir, Identifiers: []string{"203.0.113.9"}}, 6*24*time.Hour)
	runManager(t, m)
	waitFor(t, "the issuance", m.Ready)
	var keys []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			keys = append(keys, path)
		}
		return nil
	})
	if len(keys) == 0 {
		t.Fatal("nothing stored")
	}
	for _, p := range keys {
		fi, _ := os.Stat(p)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s has mode %o", p, fi.Mode().Perm())
		}
	}
	if _, ok := m.store.load([]string{"203.0.113.9"}); !ok {
		t.Fatal("not loadable")
	}
	// A second manager over the same directory serves the kept certificate without an order.
	m2, fi2, _ := newManagerForTest(t, Options{Dir: dir, Identifiers: []string{"203.0.113.9"}}, 6*24*time.Hour)
	if !m2.Ready() || fi2.count() != 0 {
		t.Fatalf("the kept certificate was not served: ready=%v issued=%d", m2.Ready(), fi2.count())
	}
}

func TestFilesReloadWhenTheyChange(t *testing.T) {
	ca := newFakeCA(t)
	dir := t.TempDir()
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	write := func(ids []string) {
		t.Helper()
		issued := ca.issue(t, ids, time.Now().Add(-time.Minute), time.Hour)
		if err := os.WriteFile(certPath, issued.CertPEM, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keyPath, issued.KeyPEM, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write([]string{"one.example"})
	m, err := New(Options{CertFile: certPath, KeyFile: keyPath, FileCheck: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); !st.Ready || st.Mode != ModeFiles || st.Identifiers[0] != "one.example" {
		t.Fatalf("status %+v", st)
	}
	runManager(t, m)
	time.Sleep(30 * time.Millisecond) // past the first check, so the next sees a newer mtime
	later := time.Now().Add(2 * time.Second)
	write([]string{"two.example"})
	_ = os.Chtimes(certPath, later, later)
	waitFor(t, "the reload", func() bool { return m.Status().Identifiers[0] == "two.example" })
	// A broken pair keeps the one served and names the error.
	_ = os.WriteFile(keyPath, []byte("not a key"), 0o600)
	later = later.Add(2 * time.Second)
	_ = os.Chtimes(keyPath, later, later)
	waitFor(t, "the error", func() bool { return m.Status().LastError != "" })
	if st := m.Status(); !st.Ready || st.Identifiers[0] != "two.example" {
		t.Fatalf("status %+v", st)
	}
	if _, err := New(Options{CertFile: certPath, KeyFile: keyPath}); err == nil {
		t.Fatal("a broken pair must refuse to start")
	}
}

func TestNewDNSProviderNeedsItsSettings(t *testing.T) {
	for name, env := range map[string]map[string]string{"cloudflare": {}, "exec": {}, "httpreq": {}, "route53": {"X": "1"}} {
		if _, err := newDNSProvider(name, env); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := newDNSProvider("exec", map[string]string{"EXEC_PATH": "/bin/true"}); err != nil {
		t.Fatal(err)
	}
	if _, err := newDNSProvider("httpreq", map[string]string{"HTTPREQ_ENDPOINT": "https://dns.example/api"}); err != nil {
		t.Fatal(err)
	}
	if _, err := newDNSProvider("cloudflare", map[string]string{"CLOUDFLARE_DNS_API_TOKEN": "tok"}); err != nil {
		t.Fatal(err)
	}
}
