package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/session"
)

// Webhooks: the server POSTs the activity entries of every session to the
// webhooks of its config (config.Webhook) that list their event type, one
// entry a request. One sink types each entry and builds its body once, and
// each webhook has a queue and a goroutine of its own that delivers from it in
// order, so a slow or dead endpoint holds up neither the sessions, whose
// goroutines only queue, nor the other webhooks.

const (
	// webhookQueue bounds the deliveries waiting for one webhook: a full
	// queue drops its oldest.
	webhookQueue = 256
	// webhookTimeout bounds a delivery, from the dial to the end of the
	// answer. A delivery is tried once: one that fails is gone.
	webhookTimeout = 5 * time.Second
	// webhookDrain bounds what is read of an answer, so that its connection
	// can carry the next delivery.
	webhookDrain = 64 << 10
)

// webhookResolver resolves webhook hosts when the server dials them; nil is
// the default resolver. Tests point it at a DNS server of their own.
var webhookResolver *net.Resolver

// webhookPayload is the body of a delivery: the session an entry belongs to
// and the entry, as the admin event stream sends it.
type webhookPayload struct {
	SessionID string                `json:"sessionId"`
	Session   webhookSession        `json:"session"`
	Entry     session.ActivityEntry `json:"entry"`
}

// webhookSession names the session of an entry. Name and AgentID are empty
// for a session the registry does not hold.
type webhookSession struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	AgentID string `json:"agentId"`
}

// webhookDelivery is a request waiting in a webhook's queue. Webhooks that
// get the same entry share its body.
type webhookDelivery struct {
	event string // X-Conductor-Event
	body  []byte
}

// webhookSender runs the webhooks of the config.
type webhookSender struct {
	hooks    []*webhook
	registry *session.Registry
	ctx      context.Context // done once the sender is closed
	cancel   context.CancelFunc
	wg       sync.WaitGroup // the delivering goroutines
}

// startWebhooks starts a goroutine for every webhook of hooks, as Validate
// has checked them, and makes the sender a sink of events. The session an
// entry belongs to is read from registry. close stops them.
func startWebhooks(hooks []config.Webhook, events *eventHub, registry *session.Registry, log *slog.Logger) *webhookSender {
	ctx, cancel := context.WithCancel(context.Background())
	ws := &webhookSender{registry: registry, ctx: ctx, cancel: cancel}
	for i, h := range hooks {
		u, err := url.Parse(h.URL)
		if err != nil {
			// Validate refuses it; the error would quote the URL.
			log.Error("webhook left out: its url is not a valid URL", "webhook", i)
			continue
		}
		w := &webhook{
			index:  i,
			url:    h.URL,
			host:   u.Host,
			events: map[string]bool{},
			secret: []byte(h.Secret),
			client: webhookClient(h.AllowPrivate),
			log:    log,
			ctx:    ctx,
			wake:   make(chan struct{}, 1),
		}
		for _, t := range h.Events {
			w.events[t] = true
		}
		ws.hooks = append(ws.hooks, w)
		ws.wg.Add(1)
		go func() {
			defer ws.wg.Done()
			w.run()
		}()
	}
	if len(ws.hooks) > 0 {
		events.addSink(ws.offer)
	}
	return ws
}

// close stops the webhooks: the delivery in flight is abandoned, the queued
// ones are not made, and no entry is queued any more. It waits for their
// goroutines to end, or for ctx, and closes the connections they kept open.
func (ws *webhookSender) close(ctx context.Context) {
	ws.cancel()
	done := make(chan struct{})
	go func() {
		ws.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	for _, w := range ws.hooks {
		w.client.CloseIdleConnections()
	}
}

// offer is the webhooks' activity sink. It types e once (eventTypeOf) and
// queues it for every webhook that lists that type or e's own, with one body
// for all of them, built for the first. It runs on the goroutine that
// recorded e, so it never waits: it looks the session up, for the body, only
// once a webhook wants the entry, and queues.
func (ws *webhookSender) offer(sessionID string, e session.ActivityEntry, state session.AttentionState) {
	if ws.ctx.Err() != nil {
		return
	}
	typ := eventTypeOf(e, state)
	var body []byte
	for _, w := range ws.hooks {
		event := w.eventFor(typ, e.Type)
		if event == "" {
			continue
		}
		if body == nil {
			var info session.Info
			if d, ok := ws.registry.Get(sessionID); ok {
				info = d.Info()
			}
			b, err := json.Marshal(webhookPayload{
				SessionID: sessionID,
				Session:   webhookSession{ID: sessionID, Name: info.Name, AgentID: info.AgentID},
				Entry:     e,
			})
			if err != nil {
				return
			}
			body = b
		}
		w.push(webhookDelivery{event: event, body: body})
	}
}

// eventTypeOf is the event type the Events page gives an entry, as
// eventTypeOf in web/app/utils/events.ts does, or "" for an entry that is
// none: an attention entry is the attention state it records, a status entry
// of a process that exited on its own with a non-zero code is exit_nonzero,
// and the six event types are themselves. For an attention entry state is
// the state the entry records, handed on with it (OnActivity for a server
// session, the host's activity message for a hosted one); it is "" only when
// the host sent none (an older host) or one that is not one of the three,
// which is dropped, and then the entry names its state itself when the report
// had no message. (The browser reads the same state from the event's state
// field, and consults the session only for an entry that carries none.)
func eventTypeOf(e session.ActivityEntry, state session.AttentionState) string {
	switch e.Type {
	case session.ActivityAttention:
		if isAttentionState(state) {
			return string(state)
		}
		if named := session.AttentionState(e.Message); isAttentionState(named) {
			return string(named)
		}
	case session.ActivityStatus:
		if code, ok := session.ExitCode(e.Message); ok && code != 0 {
			return config.EventExitNonZero
		}
	case session.ActivityProgress, session.ActivityArtifact, session.ActivityHandoff,
		session.ActivityToolUse, session.ActivityToolDenied, session.ActivityError:
		return e.Type
	}
	return ""
}

// isAttentionState reports whether s is a state an attention entry records.
func isAttentionState(s session.AttentionState) bool {
	return s == session.AttentionNeedsInput || s == session.AttentionWorking || s == session.AttentionDone
}

// webhook delivers to one URL.
type webhook struct {
	index  int    // its place among the config's webhooks, which logs name it by
	url    string // as configured, and never logged: its query may hold a token
	host   string // host[:port], which logs name it by
	events map[string]bool
	secret []byte
	client *http.Client
	log    *slog.Logger
	ctx    context.Context // done once the sender is closed

	mu      sync.Mutex
	queue   [webhookQueue]webhookDelivery // a ring: n deliveries from head
	head, n int
	dropped uint64
	wake    chan struct{} // something was queued
}

// eventFor is the event type under which an entry of type own, which the
// Events page types typ ("" for none), reaches the webhook, "" when it lists
// neither: typ when the webhook lists it, which says more, else own.
func (w *webhook) eventFor(typ, own string) string {
	if typ != "" && w.events[typ] {
		return typ
	}
	if w.events[own] {
		return own
	}
	return ""
}

// push queues d, dropping the oldest delivery when the queue is full. The
// first drop and every 100th are logged.
func (w *webhook) push(d webhookDelivery) {
	w.mu.Lock()
	dropped := uint64(0)
	if w.n == webhookQueue {
		w.queue[w.head] = webhookDelivery{}
		w.head = (w.head + 1) % webhookQueue
		w.n--
		w.dropped++
		dropped = w.dropped
	}
	w.queue[(w.head+w.n)%webhookQueue] = d
	w.n++
	w.mu.Unlock()
	select {
	case w.wake <- struct{}{}:
	default:
	}
	if dropped > 0 && (dropped == 1 || dropped%100 == 0) {
		w.log.Warn("webhook queue full: dropped the oldest entry", "webhook", w.index, "host", w.host, "dropped", dropped)
	}
}

// pop takes the oldest queued delivery.
func (w *webhook) pop() (webhookDelivery, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.n == 0 {
		return webhookDelivery{}, false
	}
	d := w.queue[w.head]
	w.queue[w.head] = webhookDelivery{}
	w.head = (w.head + 1) % webhookQueue
	w.n--
	return d, true
}

// stats reports how many deliveries wait and how many were dropped so far.
func (w *webhook) stats() (queued int, dropped uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.n, w.dropped
}

// run delivers what is queued, in order, until the sender is closed.
func (w *webhook) run() {
	for {
		d, ok := w.pop()
		if !ok {
			select {
			case <-w.ctx.Done():
				return
			case <-w.wake:
			}
			continue
		}
		if w.ctx.Err() != nil {
			return
		}
		w.deliver(d)
	}
}

// deliver POSTs one entry, once, and logs a failure or an answer that is not
// 2xx at warn level. Neither the URL, whose query may hold a token, nor the
// answer's body is logged.
func (w *webhook) deliver(d webhookDelivery) {
	req, err := http.NewRequestWithContext(w.ctx, http.MethodPost, w.url, bytes.NewReader(d.body))
	if err != nil {
		w.log.Warn("webhook delivery failed: no request for its url", "webhook", w.index, "host", w.host, "event", d.event)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Conductor-Event", d.event)
	if len(w.secret) > 0 {
		mac := hmac.New(sha256.New, w.secret)
		mac.Write(d.body)
		req.Header.Set("X-Conductor-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := w.client.Do(req)
	if err != nil {
		if w.ctx.Err() == nil {
			w.log.Warn("webhook delivery failed", "webhook", w.index, "host", w.host, "event", d.event, "err", withoutURL(err))
		}
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, webhookDrain))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		w.log.Warn("webhook answered with an error status", "webhook", w.index, "host", w.host, "event", d.event, "status", resp.StatusCode)
		return
	}
	w.log.Debug("webhook delivered", "webhook", w.index, "host", w.host, "event", d.event, "status", resp.StatusCode)
}

// withoutURL is err without the URL a *url.Error quotes: the webhook's query
// may hold a token.
func withoutURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// webhookClient makes one attempt at the URL it is given: a delivery has
// webhookTimeout, and a redirect is the answer, not somewhere else to send
// the entry. It never uses a proxy, so that the address rule sees where every
// request goes. It dials as the standard dialer does, looking the host up for
// every connection and, when the host has several addresses, giving each a
// share of the time (and the other address family a head start), and unless
// allowPrivate it refuses, in Control, every address config.CheckWebhookAddr
// refuses, just before connecting to it: a name that resolves elsewhere since
// the config was checked (DNS rebinding) reaches no private address, and the
// dial goes on to the host's next address.
func webhookClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: webhookTimeout, Resolver: webhookResolver}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return err
			}
			return config.CheckWebhookAddr(ap.Addr())
		}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = dialer.DialContext
	return &http.Client{
		Transport: tr,
		Timeout:   webhookTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
