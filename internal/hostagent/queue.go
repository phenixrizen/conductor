package hostagent

import (
	"slices"
	"sync"
	"time"

	"github.com/rocksolidlabs/conductor/internal/proto"
)

// A viewer's requests that wait. A submit, a chat sent to the agent and a
// chat_send wait for the session's one submission at a time and pause
// before their Enter; a keystroke (INPUT) waits for the program to take it,
// which one that stopped reading its terminal does not; an nvim_open waits
// for Neovim to start, and the editor's keys, swap answers and closes for
// Neovim to answer; a save's part (FILE_WRITE) for the disk, its last one
// writing the file. None of them may run on the frame loop, which for the
// relay carries every relayed viewer's frames and the server's messages:
// the save's parts run on a queue of their own (saving), in the order
// sent, one at a time, as the editor's requests do. The three that type into
// the agent run on the peer's typing queue (requestQueue), one at a time in
// the order sent, as the server's read loop types them; the keystrokes on a
// queue of their own (keys), in the order sent, as before: those sent
// before a submission are written before it (typeIn waits for them), and
// its pause never holds back those after; the editor's keys, swap answers
// and closes on a third (editing), in the order sent; the editor's opens
// run side by side, at most proto.MaxNvimPerSub at once per viewer (the
// editors a connection may hold), so that a slow Neovim never holds a
// reply or another open back.
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
	// maxQueuedKeys and maxQueuedKeyBytes bound the keystrokes that wait
	// behind the one being written, per viewer: room for a burst of keys at
	// typing speed or a few pastes of a whole INPUT frame, while the program
	// is slow to take them; past either a frame is refused
	// (too_many_requests). Each has session.InputWait from its arrival.
	maxQueuedKeys     = 256
	maxQueuedKeyBytes = 4 * proto.MaxInput
	// maxQueuedEdits bounds the editor's requests that wait behind the one
	// Neovim handles, per viewer: its keys' burst (proto.NvimKeyBurst), which
	// the session refuses past anyway.
	maxQueuedEdits = proto.NvimKeyBurst
	// maxQueuedSaveParts and maxQueuedSaveBytes bound the parts of a
	// viewer's saves that wait behind the one the session takes (a save's
	// last part writes the file), per viewer: two whole saves, a part being
	// at most proto.MaxWritePart and a save proto.MaxFileBytes. Past either
	// a part is refused (too_many_requests).
	maxQueuedSaveParts = 2 * proto.MaxFileBytes / proto.MaxWritePart
	maxQueuedSaveBytes = 2 * proto.MaxFileBytes
)

// queueFullWords say why a request was refused.
const queueFullWords = "too many requests are waiting on this connection: try again once they are done"

// saveQueueFullWords say why a save's part was refused.
const saveQueueFullWords = "too many saves are waiting on this connection: save again once they are done"

// queueFull is the error a request gets when its queue is full, naming the
// request it refuses (a chat's id or a chat_send's ref), or none for a submit.
func queueFull(requestID string) []byte {
	return proto.MustControl(proto.ErrorMsg{T: proto.CtlError, Code: proto.ErrCodeTooManyRequests, Message: queueFullWords, RequestID: requestID})
}

// request is one of a viewer's requests.
type request struct {
	run func()
	// size is what the request holds, for a queue bounded in bytes.
	size int
	// live says whether the request's viewer is still there; a waiting
	// request whose viewer is gone is dropped, unanswered. Nil is always
	// there. It may take the peer's lock, never the queue's.
	live func() bool
	// deadline, when set, is when a request still waiting is refused with
	// expired, whether its turn has come or not: one stuck ahead of it
	// holds it no longer than its own time.
	deadline time.Time
	expired  func()
	// done, when set, runs once the request is finished whichever way: run,
	// refused, or dropped with its viewer or the queue. It may run with the
	// queue's lock held, and takes none.
	done func()
}

// waitingRequest is a request in the queue, with its deadline's timer.
type waitingRequest struct {
	request
	timer *time.Timer
	// finished is closed once the request has run, or been dropped or
	// refused without running (after).
	finished chan struct{}
}

// requestQueue runs requests one at a time, in the order they were added,
// on one goroutine that it starts for the first and that ends once none is
// left: an idle viewer holds no goroutine. At most limit (maxQueued when
// zero) wait behind the one running, holding at most maxBytes together (no
// bound when zero). Its zero value is ready. Lock order: the queue's lock,
// then the peer's (live).
type requestQueue struct {
	limit    int
	maxBytes int

	mu      sync.Mutex
	waiting []*waitingRequest
	bytes   int // the sizes of those waiting
	running bool
	closed  bool
	// done is closed when the goroutine running the requests ends.
	done chan struct{}
	// last is the finished channel of the request added last; nil before
	// the first.
	last chan struct{}
	// started counts the goroutines started to run requests, for the test
	// that holds the queue to one at a time.
	started int
}

// add runs r after the requests added before it, or reports false, running
// nothing, when the queue's bound is reached by requests of viewers still
// there or the queue is closed. The request added to an idle queue has its
// turn at once. add never blocks.
func (q *requestQueue) add(r request) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch {
	case q.closed:
		return false
	case !q.running:
		// The first runs at once: it is the one running from now on, which
		// the viewer going lets finish.
		w := &waitingRequest{request: r, finished: make(chan struct{})}
		q.last = w.finished
		q.running = true
		q.done = make(chan struct{})
		q.started++
		go q.run(w, q.done)
		return true
	}
	if q.full(r.size) {
		// Those whose viewer is gone (one that moved to the relay and
		// attached again) do not count.
		q.waiting = slices.DeleteFunc(q.waiting, func(w *waitingRequest) bool {
			if w.live == nil || w.live() {
				return false
			}
			w.stop()
			q.bytes -= w.size
			w.finish()
			return true
		})
		if q.full(r.size) {
			return false
		}
	}
	w := &waitingRequest{request: r, finished: make(chan struct{})}
	q.last = w.finished
	q.bytes += r.size
	if !r.deadline.IsZero() && r.expired != nil {
		w.timer = time.AfterFunc(time.Until(r.deadline), func() { q.expire(w) })
	}
	q.waiting = append(q.waiting, w)
	return true
}

// full reports whether a request of size has no room. The caller holds
// q.mu.
func (q *requestQueue) full(size int) bool {
	limit := q.limit
	if limit <= 0 {
		limit = maxQueued
	}
	return len(q.waiting) >= limit || (q.maxBytes > 0 && q.bytes+size > q.maxBytes)
}

// after returns a channel closed once every request added so far has run,
// or been dropped or refused: what a request of another queue that must
// come after them waits for (a viewer's submission, behind the keys it
// typed before it). The requests run in the order added, so the last one's
// end is theirs.
func (q *requestQueue) after() <-chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.last == nil {
		q.last = make(chan struct{})
		close(q.last)
	}
	return q.last
}

func (q *requestQueue) run(w *waitingRequest, done chan struct{}) {
	defer close(done)
	for w != nil {
		w.run()
		w.finish()
		w = q.next()
	}
}

// next takes the first waiting request whose viewer is still there, dropping
// those before it whose viewer is gone; nil, the queue idle, when none is
// left. live is asked without the queue's lock.
func (q *requestQueue) next() *waitingRequest {
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
		q.bytes -= w.size
		// Its expiry, if it is on its way, finds it gone; the request's own
		// deadline answers it then.
		w.stop()
		q.mu.Unlock()
		if w.live == nil || w.live() {
			return w
		}
		w.finish()
	}
}

// expire refuses w, unless its turn came first or it was dropped.
func (q *requestQueue) expire(w *waitingRequest) {
	q.mu.Lock()
	i := slices.Index(q.waiting, w)
	if i >= 0 {
		q.waiting = slices.Delete(q.waiting, i, i+1)
		q.bytes -= w.size
	}
	q.mu.Unlock()
	if i >= 0 {
		w.expired()
		w.finish()
	}
}

// finish marks w finished (after) and runs its done.
func (w *waitingRequest) finish() {
	close(w.finished)
	if w.done != nil {
		w.done()
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
		w.finish()
	}
	q.waiting = nil
	q.bytes = 0
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
