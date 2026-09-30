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
	"net/url"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/config"
	"github.com/phenixrizen/conductor/internal/session"
)

// Webhooks: the server POSTs the activity entries of every session to the
// webhooks of its config (config.Webhook) that list their event type, one
// entry a request. Each webhook has a queue and a goroutine of its own that
// delivers from it in order, so a slow or dead endpoint holds up neither the
// sessions, whose goroutines only queue, nor the other webhooks.

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

// webhookDelivery is a request waiting in a webhook's queue.
type webhookDelivery struct {
	event string // X-Conductor-Event
	body  []byte
}

// webhookSender runs the webhooks of the config.
type webhookSender struct {
	hooks  []*webhook
	ctx    context.Context // done once the sender is closed
	cancel context.CancelFunc
	wg     sync.WaitGroup // the delivering goroutines
}

// startWebhooks makes every webhook of hooks, as Validate has checked them, a
// sink of events and starts its goroutine. The session an entry belongs to is
// read from registry. close stops them.
func startWebhooks(hooks []config.Webhook, events *eventHub, registry *session.Registry, log *slog.Logger) *webhookSender {
	ctx, cancel := context.WithCancel(context.Background())
	ws := &webhookSender{ctx: ctx, cancel: cancel}
	for i, h := range hooks {
		u, err := url.Parse(h.URL)
		if err != nil {
			// Validate refuses it; the error would quote the URL.
			log.Error("webhook left out: its url is not a valid URL", "webhook", i)
			continue
		}
		w := &webhook{
			index:    i,
			url:      h.URL,
			host:     u.Host,
			events:   map[string]bool{},
			secret:   []byte(h.Secret),
			client:   webhookClient(h.AllowPrivate),
			registry: registry,
			log:      log,
			ctx:      ctx,
			wake:     make(chan struct{}, 1),
		}
		for _, t := range h.Events {
			w.events[t] = true
		}
		ws.hooks = append(ws.hooks, w)
		events.addSink(w.offer)
		ws.wg.Add(1)
		go func() {
			defer ws.wg.Done()
			w.run()
		}()
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

// webhook delivers to one URL.
type webhook struct {
	index    int    // its place among the config's webhooks, which logs name it by
	url      string // as configured, and never logged: its query may hold a token
	host     string // host[:port], which logs name it by
	events   map[string]bool
	secret   []byte
	client   *http.Client
	registry *session.Registry
	log      *slog.Logger
	ctx      context.Context // done once the sender is closed

	mu      sync.Mutex
	queue   [webhookQueue]webhookDelivery // a ring: n deliveries from head
	head, n int
	dropped uint64
	wake    chan struct{} // something was queued
}

// offer is the webhook's activity sink: it queues e when the webhook lists
// its event type (webhookEvent). It runs on the goroutine that recorded e, so
// it never waits: it reads the session's attention, which types an attention
// entry, and its name for the body, and queues. For a server session the
// attention it reads is the one the entry records: Local records the entry
// right after it sets the state, on the same goroutine. The host of a hosted
// session sends the change and the entry on separate paths, and the entry may
// come first; the state read is then the one before, unless the entry names
// its state (recordedState).
func (w *webhook) offer(sessionID string, e session.ActivityEntry) {
	if w.ctx.Err() != nil || !w.mayList(e.Type) {
		return
	}
	var info session.Info
	if d, ok := w.registry.Get(sessionID); ok {
		info = d.Info()
	}
	event := webhookEvent(w.events, e, info.Attention)
	if event == "" {
		return
	}
	body, err := json.Marshal(webhookPayload{
		SessionID: sessionID,
		Session:   webhookSession{ID: sessionID, Name: info.Name, AgentID: info.AgentID},
		Entry:     e,
	})
	if err != nil {
		return
	}
	w.push(webhookDelivery{event: event, body: body})
}

// mayList reports whether an entry of type t can be of an event type the
// webhook lists, which spares looking up the session of every other entry.
func (w *webhook) mayList(t string) bool {
	switch t {
	case session.ActivityAttention:
		return w.events[t] || w.events[string(session.AttentionNeedsInput)] || w.events[string(session.AttentionWorking)] || w.events[string(session.AttentionDone)]
	case session.ActivityStatus:
		return w.events[t] || w.events[config.EventExitNonZero]
	}
	return w.events[t]
}

// webhookEvent is the event type under which e reaches a webhook that lists
// events, "" when it lists none that e is: the type the Events page gives e
// (eventTypeOf) or else e's own. When a webhook lists both, the Events page's,
// which says more, names it.
func webhookEvent(events map[string]bool, e session.ActivityEntry, att session.Attention) string {
	if t := eventTypeOf(e, att); t != "" && events[t] {
		return t
	}
	if events[e.Type] {
		return e.Type
	}
	return ""
}

// eventTypeOf is the event type the Events page gives an entry, as
// eventTypeOf in web/app/utils/events.ts does, or "" for an entry that is
// none: an attention entry is the attention state it records, a status entry
// of a process that exited on its own with a non-zero code is
// exit_nonzero, and the six event types are themselves. att is the
// attention of the entry's session when the entry arrived.
func eventTypeOf(e session.ActivityEntry, att session.Attention) string {
	switch e.Type {
	case session.ActivityAttention:
		return string(recordedState(e, att))
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

// recordedState is the attention state an attention entry records. The
// session logs the report's message, or the state itself for a report without
// one. It is the session's state when the session's attention is the one the
// entry records (the same message, or no message and the entry naming the
// state); else the state the entry names; else the state the session is in.
// A session whose attention is cleared gives none.
func recordedState(e session.ActivityEntry, att session.Attention) session.AttentionState {
	inState := isAttentionState(att.State)
	if inState && (att.Message != "" && att.Message == e.Message || att.Message == "" && e.Message == string(att.State)) {
		return att.State
	}
	if named := session.AttentionState(e.Message); isAttentionState(named) {
		return named
	}
	if inState {
		return att.State
	}
	return session.AttentionNone
}

// isAttentionState reports whether s is a state an attention entry records.
func isAttentionState(s session.AttentionState) bool {
	return s == session.AttentionNeedsInput || s == session.AttentionWorking || s == session.AttentionDone
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
// the entry. It never uses a proxy, and it dials only addresses that pass
// config.WebhookAddrs, looked up again for every connection: the address
// check sees where every request goes, and a name that resolves elsewhere
// since the config was checked (DNS rebinding) reaches no private address.
func webhookClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: webhookTimeout}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		addrs, err := config.WebhookAddrs(ctx, host, allowPrivate)
		if err != nil {
			return nil, err
		}
		var errs []error
		for _, a := range addrs {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
			if err == nil {
				return conn, nil
			}
			errs = append(errs, err)
		}
		return nil, errors.Join(errs...)
	}
	return &http.Client{
		Transport: tr,
		Timeout:   webhookTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
