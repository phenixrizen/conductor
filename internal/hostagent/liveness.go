package hostagent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/session"
)

// What the host knows of the server being there. A link's going reaches
// the host on its control connection (link_gone, a viewer_leave with a
// reason), and as the host notices that connection lost it counts every
// link gone (linkCreds.lost). A connection that stops carrying what the
// server sends without the host noticing (the server has closed it, or the
// network drops what it carries, and nothing the host wrote has failed yet)
// tells it neither, for as long as that lasts. So the host acts on what a
// viewer who joined through a link sends (a line, a key, a save, a read, an
// editor's open, a chat post) only while it vouches for the server: while a
// ping of its own, sent within linkHeardWithin, has been answered (peer.fresh).
// A pong follows everything the server wrote before it, so what the host
// read before the answer is no older than the ping, and a link's going the
// server queued by then has been read, or follows the answer behind at most
// one relayed message and the server's own lane (the server writes its own
// messages ahead of the frames it relays). Nothing else vouches: not a
// frame the server relays, not the server's own pings, not bytes read,
// which could have waited on the way for any time. Past the bound, what a
// link's viewer sends is withdrawn as for a link gone (serverQuiet).
//
// The host pings at once when a viewer joins through a link (agent.addPeer,
// before its hello can arrive), and every livePingEvery while a link someone
// joined through is held (linkCreds.held), quiet or not, each ping waiting
// linkHeardWithin for its pong, several side by side. A viewer attaches on
// its link's credential alone; its first line, key or save waits, within
// its own time, for a ping under way to be answered when the host does not
// vouch yet (after a start, a reconnect, or a spell with no link held) and
// no ping sent since the last answer has gone unanswered (awaitVouch). The
// cost: while a server pauses its reading for longer than the bound (a
// switchyard's bound on what open hosts relay, at its worst; a large frame
// of the host's ahead of its ping on a slow uplink), a link's line waits,
// and is refused once its time is up, rather than being entered.
//
// Whatever the host reads on the connection counts for one thing alone: a
// connection the host has pinged and heard nothing at all on for
// writeStall is given up (keepHearing). What is read counts as the
// connection's TLS gives it (hearingTransport), record by record, so a
// large frame that comes in slowly keeps the connection while it comes.
// These are the WebSocket's own pings: no message of the protocol changes.
const (
	// livePingEvery is how often the host pings the server while a link's
	// viewer may need it.
	livePingEvery = 5 * time.Second
	// linkHeardWithin bounds how long ago a ping the server answered was
	// sent for a link's viewer's line, key or save to be acted on, and how
	// long each ping waits for its pong. A live connection answers within a
	// round trip, and the host pings every livePingEvery, so a quiet live
	// connection keeps the host vouching with room to spare. It is also how
	// long the server waits for a write of its own to the host before it
	// closes the connection, and how long a link that goes as the
	// connection stops carrying anything can still have a line entered.
	linkHeardWithin = 15 * time.Second
	// maxPingsUnderWay bounds the host's pings waiting for their pongs on
	// one connection: livePingEvery's, and those of viewers joining through
	// links meanwhile, past which a join pings no more (one under way
	// answers for it).
	maxPingsUnderWay = 8
)

// liveEpoch is the origin of connLive's times, read on the monotonic clock.
var liveEpoch = time.Now()

// sinceEpoch is now, as time since liveEpoch.
func sinceEpoch() time.Duration { return time.Since(liveEpoch) }

// connLive is what the host knows of its server on one control connection:
// when it last read anything there (heard), and its pings: when the latest
// one answered was sent, when the latest one that went unanswered was, and
// how many wait for their pongs.
type connLive struct {
	// heard is when, as time since liveEpoch.
	heard atomic.Int64

	mu sync.Mutex
	// answered is when the latest ping the server answered was sent, since
	// liveEpoch; hasAnswer is false before the first.
	answered  time.Duration
	hasAnswer bool
	// missed is when the latest ping whose wait ran out unanswered was
	// sent; hasMissed is false before the first.
	missed    time.Duration
	hasMissed bool
	// underWay counts the pings waiting for their pongs.
	underWay int
	// changed is closed, and replaced, at each ping's outcome and as the
	// connection ends (end).
	changed chan struct{}
	ended   bool
}

// newConnLive is a connection's, heard from now (its upgrade answered),
// with no ping answered yet.
func newConnLive() *connLive {
	l := &connLive{changed: make(chan struct{})}
	l.hear()
	return l
}

// hear notes the server heard from now. Each who hears keeps the latest.
func (l *connLive) hear() {
	now := int64(sinceEpoch())
	for {
		was := l.heard.Load()
		if was >= now || l.heard.CompareAndSwap(was, now) {
			return
		}
	}
}

// quietFor is how long ago the server was last heard from.
func (l *connLive) quietFor() time.Duration {
	return sinceEpoch() - time.Duration(l.heard.Load())
}

// change wakes whoever waits on the pings. Called with l.mu held.
func (l *connLive) change() {
	close(l.changed)
	l.changed = make(chan struct{})
}

// pingSent notes a ping going now and returns when, since liveEpoch; false,
// and nothing noted, when maxPingsUnderWay wait already or the connection
// ended.
func (l *connLive) pingSent() (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ended || l.underWay >= maxPingsUnderWay {
		return 0, false
	}
	l.underWay++
	return sinceEpoch(), true
}

// pingDone notes the outcome of the ping sent at sent: answered, or not
// within its wait.
func (l *connLive) pingDone(sent time.Duration, answered bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.underWay--
	switch {
	case answered && (!l.hasAnswer || sent > l.answered):
		l.answered, l.hasAnswer = sent, true
	case !answered && (!l.hasMissed || sent > l.missed):
		l.missed, l.hasMissed = sent, true
	}
	l.change()
}

// answer notes a ping sent at sent answered, as pingDone does for one the
// host sent (tests).
func (l *connLive) answer(sent time.Duration) {
	l.mu.Lock()
	l.underWay++
	l.mu.Unlock()
	l.pingDone(sent, true)
}

// end notes the connection ended: nothing of it vouches from now on, and
// whoever waits on its pings stops.
func (l *connLive) end() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.ended {
		l.ended = true
		l.change()
	}
}

// vouchesWithin says whether a ping the server answered was sent within
// the last within. The caller holds l.mu.
func (l *connLive) vouchesWithin(within time.Duration) bool {
	return !l.ended && l.hasAnswer && sinceEpoch()-l.answered <= within
}

// missedSinceAnswer says whether a ping sent after the latest answered one
// went unanswered: the server failed to answer since it last did. The
// caller holds l.mu.
func (l *connLive) missedSinceAnswer() bool {
	return l.hasMissed && (!l.hasAnswer || l.missed > l.answered)
}

// vouches says whether the host vouches for its server on this connection,
// a ping answered within the last within.
func (l *connLive) vouches(within time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.vouchesWithin(within)
}

// soon is what a write of a link's viewer that finds the host not vouching
// may wait on (awaitVouch): the next ping's outcome, while a ping is under
// way and none sent since the latest answer went unanswered; nil when the
// host vouches already, or the write has nothing to wait for.
func (l *connLive) soon(within time.Duration) <-chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.vouchesWithin(within) || l.ended || l.underWay == 0 || l.missedSinceAnswer() {
		return nil
	}
	return l.changed
}

// serverQuiet is why a link's viewer's line is withdrawn while the host does
// not vouch for its server (no ping it sent within its bound answered): a
// kind of session.ErrRevoked (errors.Is holds), so that the session does
// with the line what it does for a link gone.
type serverQuiet struct{ within time.Duration }

// Error is the words a page shows for a refusal, the same whatever the
// host takes for hearing from its server: no ping of its own answered.
func (e serverQuiet) Error() string {
	return fmt.Sprintf("host: the server has not been heard from within %s, which a link's line needs", e.within)
}

// Is makes errors.Is(serverQuiet, session.ErrRevoked) hold.
func (serverQuiet) Is(target error) bool { return target == session.ErrRevoked }

// heardWithin is linkHeardWithin, or a test's own (liveWithin).
func (a *agent) heardWithin() time.Duration {
	if a.liveWithin > 0 {
		return a.liveWithin
	}
	return linkHeardWithin
}

// pingEvery is livePingEvery, or a test's own (livePing).
func (a *agent) pingEvery() time.Duration {
	if a.livePing > 0 {
		return a.livePing
	}
	return livePingEvery
}

// vouches says why the host cannot vouch now for the links its viewers
// joined through (serverQuiet): no control connection, or no ping it sent
// within heardWithin answered on it; nil when it can. It takes no lock but
// the connection's own: the session asks under its own (peer.fresh).
func (a *agent) vouches() error {
	within := a.heardWithin()
	if l := a.live.Load(); l != nil && l.vouches(within) {
		return nil
	}
	return serverQuiet{within}
}

// awaitVouch waits, until ctx ends, for the host to vouch for its server
// when it does not yet and may soon (connLive.soon): a ping under way, and
// none sent since the latest answer gone unanswered; so the first write of
// a viewer who joined through a link after a start, a reconnect or a spell
// with no link held waits for the ping its join sent. It returns at once
// otherwise: the session's own check of the write then says.
func (a *agent) awaitVouch(ctx context.Context) {
	within := a.heardWithin()
	for {
		l := a.live.Load()
		if l == nil {
			return
		}
		wait := l.soon(within)
		if wait == nil {
			return
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return
		}
	}
}

// pingServer pings the server on c, whose pings l keeps, on a goroutine of
// its own (pingOn). The ping is noted under way here, on the caller's
// goroutine, before it returns: a write that comes next finds it under way
// and waits for its answer (awaitVouch). A ping past maxPingsUnderWay is not
// sent.
func (a *agent) pingServer(ctx context.Context, c *websocket.Conn, l *connLive) {
	sent, ok := l.pingSent()
	if !ok {
		return
	}
	go a.pingOn(ctx, c, l, sent)
}

// pingOn sends the ping noted under way at sent and notes its outcome:
// answered (the pong came within heardWithin), or not. Its send is when the
// ping was noted, before it is written: the host vouches from no later than
// the ping left.
func (a *agent) pingOn(ctx context.Context, c *websocket.Conn, l *connLive, sent time.Duration) {
	if seam := a.beforePing.Load(); seam != nil {
		(*seam)()
	}
	pctx, cancel := context.WithTimeout(ctx, a.heardWithin())
	err := c.Ping(pctx)
	cancel()
	if err == nil {
		l.hear()
	}
	l.pingDone(sent, err == nil)
}

// pingNow pings the server at once on the current control connection: a
// viewer joining through a link, whose first line waits for the answer when
// the host does not vouch yet. The ping is under way once it returns.
func (a *agent) pingNow() {
	a.mu.Lock()
	w := a.out
	a.mu.Unlock()
	if w == nil || w.live == nil {
		return
	}
	a.pingServer(w.ctx, w.c, w.live)
}

// keepHearing pings the server on c, whose pings l keeps, every pingEvery
// while a link someone joined through is held (linkCreds.held), quiet or
// not, each ping on a goroutine of its own, until done closes (the
// connection's writer ends) or ctx ends. A ping that is not answered ends
// nothing at once: the bound does what it must for the links. But once the
// host has pinged and heard nothing at all for writeStall, it gives the
// connection up, as it does a socket that takes nothing for that long: its
// pings keep a connection that drops what it carries from ever being idle,
// so TCP's own keepalive, which would have ended it, never probes it.
func (a *agent) keepHearing(ctx context.Context, c *websocket.Conn, l *connLive, done <-chan struct{}) {
	every := a.pingEvery()
	giveUp := writeStall
	if a.stallWait > 0 {
		giveUp = a.stallWait
	}
	within := a.heardWithin()
	t := time.NewTicker(every)
	defer t.Stop()
	told := false
	// pinging is when the host began to ping with nothing heard since
	// (heardThen, as connLive keeps it, is what it had heard then); zero
	// while it does not ping. Anything heard begins it again: a pong, or
	// any other frame, which may come while a ping waits for its pong.
	var pinging time.Time
	var heardThen int64
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
		}
		l.mu.Lock()
		vouched, missed := l.vouchesWithin(within), l.missedSinceAnswer()
		l.mu.Unlock()
		switch {
		case !vouched && missed && !told:
			told = true
			a.log.Warn("the server has not answered the host's pings; what viewers who joined through a link send is not acted on until it does", "within", within)
		case vouched && told:
			told = false
			a.log.Info("the server answers the host's pings again")
		}
		if !a.creds.held() {
			pinging = time.Time{}
			continue
		}
		if heard := l.heard.Load(); pinging.IsZero() || heard != heardThen {
			pinging, heardThen = time.Now(), heard
		}
		if time.Since(pinging) > giveUp {
			a.log.Warn("the server has not answered the host's pings; giving the control connection up", "quiet", l.quietFor().Round(time.Second))
			c.CloseNow()
			return
		}
		a.pingServer(ctx, c, l)
	}
}
