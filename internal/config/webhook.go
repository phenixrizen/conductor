package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"time"

	"github.com/phenixrizen/conductor/internal/session"
)

// Webhook is a URL the server POSTs activity entries to: the entries of every
// session whose event type Events lists (ValidWebhookEvent). With a Secret,
// each request carries an HMAC-SHA256 signature of its body. The URL may not
// reach a loopback, link-local, private or unspecified address unless
// AllowPrivate says so (WebhookAddrs).
type Webhook struct {
	URL          string   `json:"url"`
	Events       []string `json:"events"`
	Secret       string   `json:"secret"`
	AllowPrivate bool     `json:"allowPrivate"`
}

// Bounds for webhooks.
const (
	MaxWebhooks   = 16
	MaxWebhookURL = 2048 // bytes
)

// EventExitNonZero is the event type of the status entry of a process that
// exited on its own with a non-zero code (session.ExitCode), as the Events
// page names it.
const EventExitNonZero = "exit_nonzero"

// webhookResolveTimeout bounds the lookup of a webhook's host at validation.
const webhookResolveTimeout = 5 * time.Second

// ValidWebhookEvent reports whether a webhook may list t: an activity entry
// type (session.ValidEventType); an attention state, needs_input, working or
// done, which an attention entry counts as when it records it; or
// EventExitNonZero. These are the Events page's names.
func ValidWebhookEvent(t string) bool {
	switch session.AttentionState(t) {
	case session.AttentionNeedsInput, session.AttentionWorking, session.AttentionDone:
		return true
	}
	return t == EventExitNonZero || session.ValidEventType(t)
}

// Endpoint is the webhook's URL without its user info, query and fragment,
// any of which may hold a token: scheme://host[:port]/path, what the API
// shows of it. It is "" for a URL that does not parse.
func (w Webhook) Endpoint() string {
	u, err := url.Parse(w.URL)
	if err != nil {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath}).String()
}

// validate checks one webhook and, unless AllowPrivate, resolves its host and
// applies WebhookAddrs. No message quotes the URL: its query or user info may
// hold a token.
func (w Webhook) validate() error {
	switch {
	case w.URL == "":
		return errors.New("url must not be empty")
	case len(w.URL) > MaxWebhookURL:
		return fmt.Errorf("url must be at most %d bytes", MaxWebhookURL)
	}
	u, err := url.Parse(w.URL)
	if err != nil {
		return errors.New("url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must be http or https")
	}
	if u.Opaque != "" || u.Hostname() == "" {
		return errors.New("url must name a host")
	}
	if len(w.Events) == 0 {
		return errors.New("events must list at least one event type")
	}
	for _, t := range w.Events {
		if !ValidWebhookEvent(t) {
			return fmt.Errorf("events: %q is not an event type", t)
		}
	}
	if w.AllowPrivate {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), webhookResolveTimeout)
	defer cancel()
	_, err = WebhookAddrs(ctx, u.Hostname(), false)
	return err
}

// LookupWebhookHost resolves the host name of a webhook, at validation and
// again before every connection to it. Tests replace it.
var LookupWebhookHost = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// WebhookAddrs returns the addresses of a webhook's host, a name or an
// address, and applies the network rule to them: unless allowPrivate, a host
// with any loopback, link-local, private or unique-local, or unspecified
// address, IPv4 or IPv6, is refused, so that a webhook reaches neither the
// server itself nor the network behind it. The server applies it when it
// validates its config and again, to a fresh lookup, every time it dials a
// webhook, and connects only to the addresses it returns: a name that
// resolves elsewhere by then (DNS rebinding) reaches no such address either.
// An IPv4 address written as IPv6 is taken as the IPv4 one.
func WebhookAddrs(ctx context.Context, host string, allowPrivate bool) ([]netip.Addr, error) {
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		found, err := LookupWebhookHost(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", host, err)
		}
		addrs = found
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("resolve %s: no address", host)
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		a = a.Unmap()
		if kind := privateKind(a); kind != "" && !allowPrivate {
			if a.String() == host {
				return nil, fmt.Errorf("%s is a %s address; set allowPrivate to send to it", host, kind)
			}
			return nil, fmt.Errorf("%s resolves to %s, a %s address; set allowPrivate to send to it", host, a, kind)
		}
		out = append(out, a)
	}
	return out, nil
}

// privateKind names the range of a that a webhook may reach only with
// allowPrivate, or is "" for an address outside them all. a is unmapped.
func privateKind(a netip.Addr) string {
	switch {
	case a.IsLoopback():
		return "loopback"
	case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast():
		return "link-local"
	case a.IsPrivate() && a.Is4():
		return "private"
	case a.IsPrivate():
		return "unique-local"
	case a.IsUnspecified(), a.Is4() && a.As4()[0] == 0:
		// 0.0.0.0/8 is "this network": a connection to it reaches this host.
		return "unspecified"
	}
	return ""
}

// parseWebhooks reads the value of CONDUCTOR_WEBHOOKS: a JSON array of
// webhooks, as the config file holds them, and nothing after it.
func parseWebhooks(v string) ([]Webhook, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(v)))
	dec.DisallowUnknownFields()
	var hooks []Webhook
	if err := dec.Decode(&hooks); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("more after the JSON array")
	}
	return hooks, nil
}
