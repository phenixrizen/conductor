package hostagent

import (
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// A viewer's requests that wait. A submit, a chat sent to the agent and a
// chat_send wait for the session's one submission at a time and pause
// before their Enter; an nvim_open waits for Neovim to start. None of them
// may run on the frame loop, which for the relay carries every relayed
// viewer's frames. The three that type into the agent run on the peer's
// typing queue (requestQueue), one at a time in the order sent, as the
// server's read loop types them; the editor's opens run side by side, at
// most proto.MaxNvimPerSub at once per viewer (the editors a connection may
// hold), so that a slow Neovim never holds a reply or another open back.
const (
	// maxQueued bounds the requests that wait behind the one running, per
	// viewer; one past it is refused (too_many_requests) and the connection
	// stays.
	maxQueued = 8
	// submitTimeout bounds a submission from its turn: the wait for the
	// session's turn, the pause and its Enter (the server's submitTimeout).
	submitTimeout = 10 * time.Second
	// nvimOpenTimeout bounds an nvim_open (the server's).
	nvimOpenTimeout = 15 * time.Second
)

// queueFullWords say why a request was refused.
const queueFullWords = "too many requests are waiting on this connection: try again once they are done"

// queueFull is the error a request gets when its queue is full, naming the
// request it refuses (a chat's id or a chat_send's ref), or none for a submit.
func queueFull(requestID string) []byte {
	return proto.MustControl(proto.ErrorMsg{T: proto.CtlError, Code: proto.ErrCodeTooManyRequests, Message: queueFullWords, RequestID: requestID})
}

// requestQueue runs requests one at a time, in the order they were added,
// on one goroutine that it starts for the first and that ends once none is
// left: an idle viewer holds no goroutine. At most maxQueued wait behind the
// one running. Its zero value is ready.
type requestQueue struct {
	mu      sync.Mutex
	waiting []queued
	running bool
	closed  bool
	// done is closed when the goroutine running the requests ends.
	done chan struct{}
}

type queued struct {
	run func()
	// live says, when the request's turn comes, whether its viewer is
	// still there; a request whose viewer is gone is dropped. Nil is
	// always there.
	live func() bool
}

// add runs fn after the requests added before it, or reports false, running
// nothing, when maxQueued wait already or the queue is closed. fn is dropped
// if live reports false when its turn comes; the request added to an idle
// queue has its turn at once. add never blocks.
func (q *requestQueue) add(live func() bool, fn func()) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case q.closed:
		return false
	case !q.running:
		// The first runs at once: it is the one running from now on, which
		// the viewer going lets finish.
		q.running = true
		q.done = make(chan struct{})
		go q.run(fn, q.done)
	case len(q.waiting) >= maxQueued:
		return false
	default:
		q.waiting = append(q.waiting, queued{run: fn, live: live})
	}
	return true
}

func (q *requestQueue) run(fn func(), done chan struct{}) {
	defer close(done)
	for fn != nil {
		fn()
		fn = q.next()
	}
}

// next takes the first waiting request whose viewer is still there, dropping
// those before it whose viewer is gone; nil, the queue idle, when none is
// left. live is asked without the queue's lock.
func (q *requestQueue) next() func() {
	for {
		q.mu.Lock()
		if len(q.waiting) == 0 {
			q.running = false
			q.mu.Unlock()
			return nil
		}
		r := q.waiting[0]
		q.waiting[0] = queued{}
		q.waiting = q.waiting[1:]
		q.mu.Unlock()
		if r.live == nil || r.live() {
			return r.run
		}
	}
}

// close drops the requests that wait and refuses those added after it. The
// one running finishes, within its own timeout: a submission whose text is
// typed goes on to its Enter when its viewer goes, as on the server.
func (q *requestQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.waiting = nil
	q.mu.Unlock()
}

// wait returns once no request runs. After close, nothing runs again.
func (q *requestQueue) wait() {
	q.mu.Lock()
	running, done := q.running, q.done
	q.mu.Unlock()
	if running {
		<-done
	}
}
