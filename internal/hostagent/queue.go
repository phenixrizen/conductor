package hostagent

import (
	"slices"
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
	// submitTimeout bounds a submission from its arrival: its wait in the
	// queue, the wait for the session's turn, the pause and its Enter (the
	// server's submitTimeout, whose read loop has no queue).
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

// request is one of a viewer's requests.
type request struct {
	run func()
	// live says whether the request's viewer is still there; a waiting
	// request whose viewer is gone is dropped, unanswered. Nil is always
	// there. It may take the peer's lock, never the queue's.
	live func() bool
	// deadline, when set, is when a request still waiting is refused with
	// expired, whether its turn has come or not: one stuck ahead of it
	// holds it no longer than its own time.
	deadline time.Time
	expired  func()
}

// waitingRequest is a request in the queue, with its deadline's timer.
type waitingRequest struct {
	request
	timer *time.Timer
}

// requestQueue runs requests one at a time, in the order they were added,
// on one goroutine that it starts for the first and that ends once none is
// left: an idle viewer holds no goroutine. At most maxQueued wait behind the
// one running. Its zero value is ready. Lock order: the queue's lock, then
// the peer's (live).
type requestQueue struct {
	mu      sync.Mutex
	waiting []*waitingRequest
	running bool
	closed  bool
	// done is closed when the goroutine running the requests ends.
	done chan struct{}
}

// add runs r after the requests added before it, or reports false, running
// nothing, when maxQueued wait for viewers still there or the queue is
// closed. The request added to an idle queue has its turn at once. add
// never blocks.
func (q *requestQueue) add(r request) bool {
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
		go q.run(r.run, q.done)
		return true
	}
	if len(q.waiting) >= maxQueued {
		// Those whose viewer is gone (one that moved to the relay and
		// attached again) do not count.
		q.waiting = slices.DeleteFunc(q.waiting, func(w *waitingRequest) bool {
			if w.live == nil || w.live() {
				return false
			}
			w.stop()
			return true
		})
		if len(q.waiting) >= maxQueued {
			return false
		}
	}
	w := &waitingRequest{request: r}
	if !r.deadline.IsZero() && r.expired != nil {
		w.timer = time.AfterFunc(time.Until(r.deadline), func() { q.expire(w) })
	}
	q.waiting = append(q.waiting, w)
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
		w := q.waiting[0]
		q.waiting[0] = nil
		q.waiting = q.waiting[1:]
		// Its expiry, if it is on its way, finds it gone; the request's own
		// deadline answers it then.
		w.stop()
		q.mu.Unlock()
		if w.live == nil || w.live() {
			return w.run
		}
	}
}

// expire refuses w, unless its turn came first or it was dropped.
func (q *requestQueue) expire(w *waitingRequest) {
	q.mu.Lock()
	i := slices.Index(q.waiting, w)
	if i >= 0 {
		q.waiting = slices.Delete(q.waiting, i, i+1)
	}
	q.mu.Unlock()
	if i >= 0 {
		w.expired()
	}
}

func (w *waitingRequest) stop() {
	if w.timer != nil {
		w.timer.Stop()
	}
}

// close drops the requests that wait and refuses those added after it. The
// one running finishes, within its own timeout: a submission whose text is
// typed goes on to its Enter when its viewer goes, as on the server.
func (q *requestQueue) close() {
	q.mu.Lock()
	q.closed = true
	for _, w := range q.waiting {
		w.stop()
	}
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
