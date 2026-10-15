package hostagent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
)

// namedDialer dials a test's names to its own servers: each "host:port" in
// to is dialed at the address it maps to, and every address asked for is
// recorded. A name it does not know is not dialed at all.
type namedDialer struct {
	to    map[string]string
	mu    sync.Mutex
	asked []string
}

func (d *namedDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	d.mu.Lock()
	d.asked = append(d.asked, addr)
	d.mu.Unlock()
	target, ok := d.to[addr]
	if !ok {
		return nil, errors.New("test: no such server " + addr)
	}
	var nd net.Dialer
	return nd.DialContext(ctx, network, target)
}

func (d *namedDialer) dialed() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.asked...)
}

// transport is base (the standard transport when nil) dialing through d.
func (d *namedDialer) transport(base *http.Transport) *http.Transport {
	if base == nil {
		base = http.DefaultTransport.(*http.Transport)
	}
	t := base.Clone()
	t.Proxy = nil
	t.DialContext = d.dial
	return t
}

// syncLog is a log the host writes to from its goroutines.
type syncLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// runUntilRegistered runs the host with opts until it registers, then stops
// it and waits for it to end; it fails the test when Run returns first.
func runUntilRegistered(t *testing.T, opts Options) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registered := make(chan string, 1)
	opts.Registered = func(id, _ string) {
		select {
		case registered <- id:
		default:
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, opts)
		done <- err
	}()
	select {
	case <-registered:
	case err := <-done:
		t.Fatalf("%s: run returned before registering: %v", opts.ServerURL, err)
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: the host did not register", opts.ServerURL)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("%s: run did not end", opts.ServerURL)
	}
}

// A plain server on another machine (http or ws) is refused before anything
// is written or dialed: no hook assets, no connection, the error naming the
// server's host alone and --insecure. The uplink refuses one the same way.
func TestRunRefusesPlainServerOffThisMachine(t *testing.T) {
	for server, host := range map[string]string{"http://yard.example:8080": "yard.example:8080", "ws://yard.example:8080": "yard.example:8080", "http://me:pw@yard.example:8080/": "yard.example:8080", "http://192.0.2.7:8080": "192.0.2.7:8080", "HTTP://yard.example": "yard.example", "http://LOCALHOST:8080": "LOCALHOST:8080"} {
		d := &namedDialer{}
		hooks := t.TempDir()
		_, err := Run(t.Context(), Options{
			ServerURL: server,
			Token:     hostToken,
			Argv:      []string{"/bin/cat"},
			Adapter:   "claude",
			HooksDir:  hooks,
			Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
			transport: d.transport(nil),
		})
		var plain *PlainServerError
		if !errors.As(err, &plain) || plain.Host != host {
			t.Errorf("%s: %v", server, err)
			continue
		}
		if !strings.Contains(err.Error(), "--insecure") || strings.Contains(err.Error(), "pw") {
			t.Errorf("%s: the words: %v", server, err)
		}
		if got := d.dialed(); len(got) != 0 {
			t.Errorf("%s: dialed %v", server, got)
		}
		if entries, _ := os.ReadDir(hooks); len(entries) != 0 {
			t.Errorf("%s: wrote hook assets before refusing: %v", server, entries)
		}
	}

	proc, err := pty.Start(pty.Spec{Argv: []string{"/bin/cat"}, Dir: t.TempDir(), Env: []string{"PATH=/usr/bin:/bin", "TERM=xterm"}, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	local := session.NewLocal(session.Info{ID: "local-plain", Name: "plain", Kind: session.KindServer, Command: []string{"/bin/cat"}, Cwd: t.TempDir(), Status: session.StatusRunning, Cols: 80, Rows: 24}, proc, session.Options{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	t.Cleanup(func() { _ = local.Stop(context.Background()) })
	u := &Uplink{ServerURL: "http://yard.example:8080", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var plain *PlainServerError
	if _, err := u.Publish(t.Context(), local); !errors.As(err, &plain) || plain.Host != "yard.example:8080" {
		t.Fatalf("publish: %v", err)
	}
}

// A plain server on this machine needs nothing: localhost by name and the
// loopback address register, with no warning in the log.
func TestRunAllowsPlainServerOnLoopback(t *testing.T) {
	_, hs := startServer(t)
	for _, server := range []string{hs.URL, strings.Replace(hs.URL, "127.0.0.1", "localhost", 1), strings.Replace(hs.URL, "http://", "ws://", 1)} {
		var logs syncLog
		runUntilRegistered(t, Options{ServerURL: server, Token: hostToken, Argv: []string{"/bin/cat"}, RelayOnly: true, Log: slog.New(slog.NewTextHandler(&logs, nil))})
		if strings.Contains(logs.String(), "insecure") {
			t.Errorf("%s: a warning for a server on this machine:\n%s", server, logs.String())
		}
	}
}

// With Insecure a plain server on another machine is dialed and registered
// with, and the log says once that what passes travels in the clear,
// naming the server's host alone.
func TestInsecureAllowsPlainServerAndWarns(t *testing.T) {
	_, hs := startServer(t)
	addr := strings.TrimPrefix(hs.URL, "http://")
	_, port, _ := net.SplitHostPort(addr)
	d := &namedDialer{to: map[string]string{"yard.example:" + port: addr}}
	var logs syncLog
	runUntilRegistered(t, Options{ServerURL: "http://me:pw@yard.example:" + port, Insecure: true, Token: hostToken, Argv: []string{"/bin/cat"}, RelayOnly: true, Log: slog.New(slog.NewTextHandler(&logs, nil)), transport: d.transport(nil)})
	if got := d.dialed(); len(got) == 0 || got[0] != "yard.example:"+port {
		t.Fatalf("dialed %v", got)
	}
	var warnings []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "level=WARN") && strings.Contains(line, "--insecure") {
			warnings = append(warnings, line)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "travels in the clear and unverified") || !strings.Contains(warnings[0], "server=yard.example:"+port) || strings.Contains(warnings[0], "pw") {
		t.Fatalf("want one warning naming the host:\n%s", logs.String())
	}
}

// A redirect the dial follows is held to the rule the URL given is: an
// https server that redirects the host to a plain one on another machine
// is refused as that server would be, before it is dialed; with Insecure
// the redirect is followed (the host registers there) and logged. A
// redirect to a plain server on this machine needs nothing.
func TestRedirectToPlainServerRefused(t *testing.T) {
	_, plain := startServer(t)
	plainAddr := strings.TrimPrefix(plain.URL, "http://")
	_, plainPort, _ := net.SplitHostPort(plainAddr)
	target := "http://example.com:" + plainPort
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Under /here, to the plain server by its loopback address.
		if rest, ok := strings.CutPrefix(r.URL.Path, "/here"); ok {
			http.Redirect(w, r, plain.URL+rest, http.StatusTemporaryRedirect)
			return
		}
		// Under /secret, to it with a password and a query in the URL.
		if rest, ok := strings.CutPrefix(r.URL.Path, "/secret"); ok {
			http.Redirect(w, r, "http://me:pw-secret@example.com:"+plainPort+rest+"?token=q-secret", http.StatusTemporaryRedirect)
			return
		}
		http.Redirect(w, r, target+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(redirector.Close)
	tlsAddr := strings.TrimPrefix(redirector.URL, "https://")
	_, tlsPort, _ := net.SplitHostPort(tlsAddr)
	// The test certificate is for example.com.
	server := "https://example.com:" + tlsPort
	names := map[string]string{"example.com:" + tlsPort: tlsAddr, "example.com:" + plainPort: plainAddr}
	base := redirector.Client().Transport.(*http.Transport)

	d := &namedDialer{to: names}
	_, err := Run(t.Context(), Options{ServerURL: server, Token: hostToken, Argv: []string{"/bin/cat"}, RelayOnly: true, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), transport: d.transport(base)})
	var refused *PlainServerError
	if !errors.As(err, &refused) || refused.Host != "example.com:"+plainPort {
		t.Fatalf("redirected to %s: %v", target, err)
	}
	for _, a := range d.dialed() {
		if a == "example.com:"+plainPort {
			t.Fatalf("dialed the plain server it was redirected to: %v", d.dialed())
		}
	}
	// The refusal's words are the whole error: nothing else of the URL the
	// redirect gave.
	_, err = Run(t.Context(), Options{ServerURL: server + "/secret", Token: hostToken, Argv: []string{"/bin/cat"}, RelayOnly: true, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), transport: (&namedDialer{to: names}).transport(base)})
	if !errors.As(err, &refused) || refused.Host != "example.com:"+plainPort || err.Error() != refused.Error() || strings.Contains(err.Error(), "secret") {
		t.Fatalf("redirected with a password and a query: %v", err)
	}

	d = &namedDialer{to: names}
	var logs syncLog
	runUntilRegistered(t, Options{ServerURL: server, Insecure: true, Token: hostToken, Argv: []string{"/bin/cat"}, RelayOnly: true, Log: slog.New(slog.NewTextHandler(&logs, nil)), transport: d.transport(base)})
	if !strings.Contains(logs.String(), "following a redirect to a plain http server that is not localhost or a loopback address") || !strings.Contains(logs.String(), "server=example.com:"+plainPort) {
		t.Fatalf("no warning for the redirect followed:\n%s", logs.String())
	}

	// To this machine: followed with nothing said. The standard library
	// keeps the host's token from a redirect to another name, so the server
	// there answers that it needs one: the answer of the server the
	// redirect led to.
	d = &namedDialer{to: map[string]string{"example.com:" + tlsPort: tlsAddr, plainAddr: plainAddr}}
	var quiet syncLog
	_, err = Run(t.Context(), Options{ServerURL: server + "/here", Token: hostToken, Argv: []string{"/bin/cat"}, RelayOnly: true, Log: slog.New(slog.NewTextHandler(&quiet, nil)), transport: d.transport(base)})
	var answered *RefusedError
	if !errors.As(err, &answered) || answered.Status != http.StatusUnauthorized {
		t.Fatalf("redirected to %s: %v", plain.URL, err)
	}
	if got := d.dialed(); len(got) != 2 || got[1] != plainAddr {
		t.Fatalf("dialed %v", got)
	}
	if strings.Contains(quiet.String(), "insecure") {
		t.Fatalf("a warning for a redirect to this machine:\n%s", quiet.String())
	}
}
