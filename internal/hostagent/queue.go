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
// viewer's frames, so each peer runs them on queues of its own
// (requestQueue): the three that type into the agent on one, so that they
// are typed in the order sent, as the server's read loop types them; the
// editor's opens on another, so that a slow Neovim never holds a reply back.
const (
	// maxQueued bounds the requests that wait behind the one running, per
	// queue and viewer; one past it is refused (too_many_requests) and the
	// connection stays.
	maxQueued = 8
	// submitTimeout bounds a submission from its turn: the wait for the
	// session's turn, the pause and its Enter (the server's submitTimeout).
	submitTimeout = 10 * time.Second
	// nvimOpenTimeout bounds an nvim_open from its turn (the server's).
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
	waiting []func()
	running bool
	closed  bool
	// done is closed when the goroutine running the requests ends.
	done chan struct{}
}

// add runs fn after the requests added before it, or reports false, running
// nothing, when maxQueued wait already or the queue is closed. It never
// blocks.
func (q *requestQueue) add(fn func()) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case q.closed:
		return false
	case !q.running:
		// The first runs at once: it is the one running from now on, which
		// close lets finish.
		q.running = true
		q.done = make(chan struct{})
		go q.run(fn, q.done)
	case len(q.waiting) >= maxQueued:
		return false
	default:
		q.waiting = append(q.waiting, fn)
	}
	return true
}

func (q *requestQueue) run(fn func(), done chan struct{}) {
	defer close(done)
	for {
		fn()
		q.mu.Lock()
		if len(q.waiting) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		fn = q.waiting[0]
		q.waiting[0] = nil
		q.waiting = q.waiting[1:]
		q.mu.Unlock()
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
