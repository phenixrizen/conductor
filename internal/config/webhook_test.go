package config

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeLookup makes LookupWebhookHost answer from answers, by host name, for
// the rest of the test, and counts the lookups. A name it does not know fails
// to resolve.
func fakeLookup(t *testing.T, answers map[string][]string) *int {
	t.Helper()
	calls := 0
	old := LookupWebhookHost
	LookupWebhookHost = func(_ context.Context, host string) ([]netip.Addr, error) {
		calls++
		list, ok := answers[host]
		if !ok {
			return nil, errors.New("no such host")
		}
		var out []netip.Addr
		for _, a := range list {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
	t.Cleanup(func() { LookupWebhookHost = old })
	return &calls
}

// withWebhooks is the default config with the webhooks given.
func withWebhooks(hooks ...Webhook) *Config {
	cfg := Defaults()
	cfg.Webhooks = hooks
	return cfg
}

// A webhook may not reach the server itself: http://127.0.0.1:1 is refused
// unless the webhook allows private addresses.
func TestWebhookToLoopbackNeedsAllowPrivate(t *testing.T) {
	fakeLookup(t, nil)
	err := withWebhooks(Webhook{URL: "http://127.0.0.1:1", Events: []string{"progress"}}).Validate()
	if err == nil || !strings.Contains(err.Error(), "webhooks[0]") || !strings.Contains(err.Error(), "loopback") || !strings.Contains(err.Error(), "allowPrivate") {
		t.Fatalf("got %v, want a refusal of the loopback address", err)
	}
	if err := withWebhooks(Webhook{URL: "http://127.0.0.1:1", Events: []string{"progress"}, AllowPrivate: true}).Validate(); err != nil {
		t.Fatalf("with allowPrivate: %v", err)
	}
}

// The rule covers loopback, link-local, private and unique-local, and
// unspecified addresses, IPv4 and IPv6, IPv4 written as IPv6 too; every other
// address passes. An address in the URL is not looked up.
func TestWebhookAddressRule(t *testing.T) {
	calls := fakeLookup(t, nil)
	for host, kind := range map[string]string{
		"127.0.0.1":          "loopback",
		"127.8.9.10":         "loopback",
		"[::1]":              "loopback",
		"[::ffff:127.0.0.1]": "loopback",
		"169.254.169.254":    "link-local",
		"[fe80::1]":          "link-local",
		"10.0.0.1":           "private",
		"172.16.0.1":         "private",
		"172.31.255.255":     "private",
		"192.168.1.1":        "private",
		"[::ffff:10.1.2.3]":  "private",
		"[fc00::1]":          "unique-local",
		"[fd12:3456::1]":     "unique-local",
		"0.0.0.0":            "unspecified",
		"0.1.2.3":            "unspecified",
		"[::]":               "unspecified",
	} {
		err := withWebhooks(Webhook{URL: "https://" + host + ":8443/hook", Events: []string{"error"}}).Validate()
		if err == nil || !strings.Contains(err.Error(), kind) || !strings.Contains(err.Error(), "allowPrivate") {
			t.Errorf("%s: got %v, want a refusal of a %s address", host, err, kind)
		}
	}
	for _, host := range []string{"93.184.215.14", "8.8.8.8", "172.32.0.1", "100.64.0.1", "[2606:2800:21f:cb07:6820:80da:af6b:8b2c]", "[2001:4860:4860::8888]"} {
		if err := withWebhooks(Webhook{URL: "https://" + host + "/hook", Events: []string{"error"}}).Validate(); err != nil {
			t.Errorf("%s: %v", host, err)
		}
	}
	if *calls != 0 {
		t.Fatalf("looked up an address %d times", *calls)
	}
}

// A host name is resolved once, at validation: it passes when every address
// it resolves to passes, is refused when one of them does not or when it does
// not resolve, and is not looked up at all with allowPrivate.
func TestWebhookHostIsResolved(t *testing.T) {
	calls := fakeLookup(t, map[string][]string{
		"hooks.example.com": {"93.184.215.14", "2606:2800:21f:cb07:6820:80da:af6b:8b2c"},
		"internal.example":  {"93.184.215.14", "10.0.0.7"},
		"localhost":         {"127.0.0.1", "::1"},
	})
	hook := func(host string, allow bool) *Config {
		return withWebhooks(Webhook{URL: "https://" + host + "/conductor", Events: []string{"artifact"}, AllowPrivate: allow})
	}
	if err := hook("hooks.example.com", false).Validate(); err != nil || *calls != 1 {
		t.Fatalf("a public host: %v after %d lookups", err, *calls)
	}
	for host, want := range map[string]string{
		"internal.example": "10.0.0.7, a private address",
		"localhost":        "loopback",
		"nowhere.example":  "resolve nowhere.example",
	} {
		if err := hook(host, false).Validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", host, err, want)
		}
	}
	*calls = 0
	for _, host := range []string{"internal.example", "localhost", "nowhere.example"} {
		if err := hook(host, true).Validate(); err != nil {
			t.Errorf("%s with allowPrivate: %v", host, err)
		}
	}
	if *calls != 0 {
		t.Fatalf("allowPrivate looked the host up %d times", *calls)
	}
}

// WebhookAddrs is the rule the server dials by: it returns every address of
// a host that passes, unmapped, and refuses a host with one that does not.
func TestWebhookAddrs(t *testing.T) {
	fakeLookup(t, map[string][]string{
		"hooks.example.com": {"::ffff:93.184.215.14", "2606:2800:21f:cb07:6820:80da:af6b:8b2c"},
		"rebind.example":    {"127.0.0.1"},
	})
	ctx := t.Context()
	addrs, err := WebhookAddrs(ctx, "hooks.example.com", false)
	if err != nil || len(addrs) != 2 || addrs[0] != netip.MustParseAddr("93.184.215.14") {
		t.Fatalf("hooks.example.com: %v %v", addrs, err)
	}
	if _, err := WebhookAddrs(ctx, "rebind.example", false); err == nil || !strings.Contains(err.Error(), "127.0.0.1, a loopback address") {
		t.Fatalf("rebind.example: %v", err)
	}
	if addrs, err := WebhookAddrs(ctx, "rebind.example", true); err != nil || len(addrs) != 1 {
		t.Fatalf("rebind.example with allowPrivate: %v %v", addrs, err)
	}
	if addrs, err := WebhookAddrs(ctx, "::1", true); err != nil || len(addrs) != 1 || addrs[0] != netip.MustParseAddr("::1") {
		t.Fatalf("::1 with allowPrivate: %v %v", addrs, err)
	}
}

// A webhook is an http(s) URL of at most 2048 bytes with a host, listing at
// least one known event type: an entry type, an attention state, or
// exit_nonzero. A config holds at most 16. The messages name the webhook by
// its index and never quote its URL, whose query or user info may hold a
// token, or its secret.
func TestWebhookValidation(t *testing.T) {
	fakeLookup(t, map[string][]string{"hooks.example.com": {"93.184.215.14"}})
	ok := Webhook{URL: "https://hooks.example.com/c", Events: []string{"progress"}, Secret: "s3cret"}
	events := []string{"needs_input", "working", "done", "exit_nonzero", "attention", "input", "join", "leave", "link", "status",
		"progress", "artifact", "handoff", "tool_use", "tool_denied", "error"}
	if err := withWebhooks(Webhook{URL: ok.URL, Events: events}).Validate(); err != nil {
		t.Fatalf("every event type: %v", err)
	}
	for name, tc := range map[string]struct {
		hook Webhook
		want string
	}{
		"no url":            {Webhook{Events: []string{"progress"}}, "url must not be empty"},
		"ftp":               {Webhook{URL: "ftp://hooks.example.com/c?token=t0k3n", Events: []string{"progress"}}, "http or https"},
		"no scheme":         {Webhook{URL: "hooks.example.com/c", Events: []string{"progress"}}, "http or https"},
		"no host":           {Webhook{URL: "https:///c?token=t0k3n", Events: []string{"progress"}}, "host"},
		"opaque":            {Webhook{URL: "https:t0k3n", Events: []string{"progress"}}, "host"},
		"not a url":         {Webhook{URL: "https://hooks.example.com:t0k3n/c", Events: []string{"progress"}}, "not a valid URL"},
		"too long":          {Webhook{URL: "https://hooks.example.com/?t0k3n=" + strings.Repeat("a", 2048), Events: []string{"progress"}}, "2048 bytes"},
		"no events":         {Webhook{URL: ok.URL}, "at least one event"},
		"unknown event":     {Webhook{URL: ok.URL, Events: []string{"progress", "exit_zero"}}, `"exit_zero"`},
		"clear is no event": {Webhook{URL: ok.URL, Events: []string{"clear"}}, `"clear"`},
	} {
		tc.hook.Secret = "s3cret"
		err := withWebhooks(ok, tc.hook).Validate()
		if err == nil || !strings.Contains(err.Error(), "webhooks[1]: ") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
			continue
		}
		if strings.Contains(err.Error(), "t0k3n") || strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: the message quotes the url or the secret: %v", name, err)
		}
	}
	if err := withWebhooks(ok, Webhook{URL: "https://hooks.example.com/c", Events: []string{"progress"}}).Validate(); err != nil {
		t.Fatalf("a webhook without a secret: %v", err)
	}
	many := make([]Webhook, 17)
	for i := range many {
		many[i] = ok
	}
	if err := withWebhooks(many...).Validate(); err == nil || !strings.Contains(err.Error(), "at most 16") {
		t.Fatalf("17 webhooks: %v", err)
	}
	if err := withWebhooks(many[:16]...).Validate(); err != nil {
		t.Fatalf("16 webhooks: %v", err)
	}
}

// Webhooks come from the config file, or from CONDUCTOR_WEBHOOKS as a JSON
// array of the same objects, which replaces them. Unknown fields are refused
// in both, and the error does not quote the value.
func TestWebhooksFromFileAndEnvironment(t *testing.T) {
	fakeLookup(t, map[string][]string{"file.example.com": {"93.184.215.14"}, "env.example.com": {"93.184.215.15"}})
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	body := `{"allowedRoots":["` + dir + `"],"webhooks":[{"url":"https://file.example.com/h","events":["needs_input","exit_nonzero"],"secret":"s1"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUCTOR_WEBHOOKS", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Webhooks) != 1 || cfg.Webhooks[0].URL != "https://file.example.com/h" || cfg.Webhooks[0].Secret != "s1" || len(cfg.Webhooks[0].Events) != 2 || cfg.Webhooks[0].AllowPrivate {
		t.Fatalf("from the file: %+v", cfg.Webhooks)
	}
	t.Setenv("CONDUCTOR_WEBHOOKS", `[{"url":"https://env.example.com/h","events":["error"],"secret":"s2"},{"url":"http://127.0.0.1:9/h","events":["done"],"allowPrivate":true}]`)
	if cfg, err = Load(path); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Webhooks) != 2 || cfg.Webhooks[0].URL != "https://env.example.com/h" || cfg.Webhooks[0].Secret != "s2" || !cfg.Webhooks[1].AllowPrivate {
		t.Fatalf("from the environment: %+v", cfg.Webhooks)
	}
	t.Setenv("CONDUCTOR_WEBHOOKS", "[]")
	if cfg, err = Load(path); err != nil || len(cfg.Webhooks) != 0 {
		t.Fatalf("an empty array clears them: %+v %v", cfg.Webhooks, err)
	}
	for _, v := range []string{
		`[{"url":"https://env.example.com/h","events":["error"],"token":"s3cret"}]`,
		`{"url":"https://env.example.com/h","events":["error"],"secret":"s3cret"}`,
		`[{"url":"https://env.example.com/h","events":["error"],"secret":"s3cret"}] [`,
		`[{"url":"https://env.example.com/h","events":["error"],"secret":"s3cret"`,
	} {
		t.Setenv("CONDUCTOR_WEBHOOKS", v)
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), "CONDUCTOR_WEBHOOKS") || strings.Contains(err.Error(), "s3cret") {
			t.Errorf("%s: %v", v, err)
		}
	}
	if err := os.WriteFile(path, []byte(`{"webhooks":[{"url":"https://file.example.com/h","events":["error"],"headers":{}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONDUCTOR_WEBHOOKS", "")
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("an unknown field in the file: %v", err)
	}
}

// Endpoint is what the API shows of a webhook: its URL without user info,
// query or fragment, which may hold a token.
func TestWebhookEndpoint(t *testing.T) {
	for url, want := range map[string]string{
		"https://hooks.example.com/c":                             "https://hooks.example.com/c",
		"https://user:pw@hooks.example.com:8443/a/b?token=t#frag": "https://hooks.example.com:8443/a/b",
		"http://[::1]:9000/hook?x=1":                              "http://[::1]:9000/hook",
		"https://hooks.example.com":                               "https://hooks.example.com",
		"https://hooks.example.com/a%2Fb?t=1":                     "https://hooks.example.com/a%2Fb",
		"https://hooks.example.com:t0k3n/c":                       "",
	} {
		if got := (Webhook{URL: url}).Endpoint(); got != want {
			t.Errorf("%s: %q, want %q", url, got, want)
		}
	}
}
