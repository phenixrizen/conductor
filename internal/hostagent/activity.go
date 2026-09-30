package hostagent

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/phenixrizen/conductor/internal/session"
)

// activityQueue bounds the entries waiting for the control connection.
const activityQueue = 256

// activityForwarder carries the activity entries of the local session to the
// server. The session calls its OnActivity hook on the goroutine that
// recorded the entry, which may be the one reading the process or the one
// serving a viewer, and that goroutine must not wait for a network write. So
// the hook only queues, and one goroutine sends, in the order queued. The
// queue is bounded: past it entries are dropped, which costs the admin stream
// a line and nothing else, because the session keeps its own log.
type activityForwarder struct {
	queue   chan forwarded
	send    func(session.ActivityEntry, session.AttentionState)
	log     *slog.Logger
	pending atomic.Int64 // entries queued or being sent
	dropped atomic.Uint64
}

// forwarded is an entry waiting for the connection, with the attention state
// it records when it is an attention entry: the hook reads it when the entry
// is recorded, for by the time the entry is sent the session may be in
// another.
type forwarded struct {
	entry session.ActivityEntry
	state session.AttentionState
}

func newActivityForwarder(send func(session.ActivityEntry, session.AttentionState), log *slog.Logger) *activityForwarder {
	return &activityForwarder{queue: make(chan forwarded, activityQueue), send: send, log: log}
}

// push queues e, with the attention state it records, without waiting. It
// reports false, and drops e, when the queue is full.
func (f *activityForwarder) push(e session.ActivityEntry, state session.AttentionState) bool {
	f.pending.Add(1) // before the entry is visible, so idle never misses it
	select {
	case f.queue <- forwarded{e, state}:
		return true
	default:
	}
	f.pending.Add(-1)
	if n := f.dropped.Add(1); n == 1 || n%100 == 0 {
		f.log.Debug("activity not forwarded: the queue for the server is full", "dropped", n)
	}
	return false
}

// run sends queued entries until ctx ends.
func (f *activityForwarder) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case q := <-f.queue:
			f.send(q.entry, q.state)
			f.pending.Add(-1)
		}
	}
}

// idle reports whether every entry queued so far has been sent.
func (f *activityForwarder) idle() bool { return f.pending.Load() == 0 }
