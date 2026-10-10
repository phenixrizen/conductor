package hostagent

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A server on this machine is the owner's; any other is not.
func TestLoopbackServer(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://localhost:8080":             true,
		"http://LOCALHOST":                  true,
		"http://127.0.0.1:8080":             true,
		"https://[::1]:8443":                true,
		"https://switchyard.example":        false,
		"https://192.168.1.20:8080":         false,
		"http://localhost.example.com:8080": false,
		"::not a url":                       false,
	} {
		if got := LoopbackServer(raw); got != want {
			t.Errorf("LoopbackServer(%q) = %v, want %v", raw, got, want)
		}
	}
}

// A host that takes its server for the owner's never follows that server
// off this machine: a redirect elsewhere is refused before it is dialled, so
// another server's no-link viewers are never taken for the owner's windows.
func TestAnOwnersServerIsNotFollowedOffThisMachine(t *testing.T) {
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://switchyard.example.invalid"+r.URL.Path, http.StatusFound)
	}))
	defer away.Close()
	wsURL, err := controlURL(away.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, mine := range []bool{true, false} {
		a := &agent{opts: Options{ServerIsOwners: mine}, wsURL: wsURL, peers: map[string]*peer{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		_, _, err := a.dialAndRegister(context.Background())
		refused := err != nil && strings.Contains(err.Error(), "refusing a redirect off this machine")
		if refused != mine {
			t.Fatalf("server is the owner's %v: dial error %v", mine, err)
		}
	}
}

// The client for a server taken for the owner's goes through no proxy and
// dials only this machine, whatever the name says: an address elsewhere is
// refused before a packet is sent.
func TestAnOwnersServerIsDialledOnlyOnThisMachine(t *testing.T) {
	c := ownersServerClient()
	tr := c.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("the owner's server is reached through a proxy")
	}
	// 192.0.2.1 is TEST-NET-1: never this machine.
	a := &agent{opts: Options{ServerIsOwners: true}, wsURL: "ws://192.0.2.1:9/ws/host", peers: map[string]*peer{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_, _, err := a.dialAndRegister(context.Background())
	if err == nil || !strings.Contains(err.Error(), "is not this machine") {
		t.Fatalf("dial elsewhere: %v", err)
	}
	// This machine's server is reached, under any spelling of its name.
	here := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusTeapot) }))
	defer here.Close()
	_, port, _ := strings.Cut(strings.TrimPrefix(here.URL, "http://"), ":")
	for _, host := range []string{"127.0.0.1", "LOCALHOST", "localhost"} {
		a.wsURL = "ws://" + host + ":" + port + "/ws/host"
		_, _, err := a.dialAndRegister(context.Background())
		if err == nil || strings.Contains(err.Error(), "is not this machine") {
			t.Fatalf("%s: %v", host, err)
		}
	}
}
