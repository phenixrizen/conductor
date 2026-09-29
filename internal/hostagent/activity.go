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
	queue   chan session.ActivityEntry
	send    func(session.ActivityEntry)
	log     *slog.Logger
	pending atomic.Int64 // entries queued or being sent
	dropped atomic.Uint64
}

func newActivityForwarder(send func(session.ActivityEntry), log *slog.Logger) *activityForwarder {
	return &activityForwarder{queue: make(chan session.ActivityEntry, activityQueue), send: send, log: log}
}

// push queues e without waiting. It reports false, and drops e, when the
// queue is full.
func (f *activityForwarder) push(e session.ActivityEntry) bool {
	f.pending.Add(1) // before the entry is visible, so idle never misses it
	select {
	case f.queue <- e:
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
		case e := <-f.queue:
			f.send(e)
			f.pending.Add(-1)
		}
	}
}

// idle reports whether every entry queued so far has been sent.
func (f *activityForwarder) idle() bool { return f.pending.Load() == 0 }
