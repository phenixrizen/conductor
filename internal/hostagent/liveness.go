package hostagent

import (
	"context"
	"fmt"
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
// tells it neither, for as long as that lasts. So the host types a line of
// a viewer who joined through a link, and enters it, only while it has
// heard from the server within linkHeardWithin (peer.authorize): past that
// the line is withdrawn as for a link gone, its text not typed, or typed
// and not entered and the session's turn held for a person.
//
// Whatever the host reads on the connection counts: the server's messages,
// the frames it relays, its pings, and its pongs. The server pings every 30
// s, and every 5 s, pong or none, while its reading of the connection is
// paused (a relay bound), which holds back its answers to the host's pings,
// and while a viewer is attached through a link; an older server pings
// every 30 s and stops once a pong is late. The host pings it too, once the
// connection has been quiet for livePingEvery while someone joined through
// a link or something of theirs is under way (linkCreds.held), so a
// connection that is live and quiet keeps being heard from, an older
// server's too, which answers them as it reads. These are the WebSocket's
// own pings: no message of the protocol changes.
const (
	// livePingEvery is how long the connection may be quiet before the host
	// pings the server, while a link's viewer may need it.
	livePingEvery = 5 * time.Second
	// linkHeardWithin bounds how long ago the server must have been heard
	// from for a link's viewer's line to be typed and entered. A live
	// connection answers the host's ping within a round trip, so a line
	// meets at most livePingEvery, a quarter of it for the check's tick, and
	// a round trip of quiet: about 7 s with a slow path. While the server
	// pauses its reading (a switchyard's bound on what a host relays), which
	// holds its answers back, it pings the host every 5 s itself, whatever it
	// reads, and the bound is three of those (an older server does not: what
	// the relayed viewers send, their pings every 10 s among it, still comes
	// meanwhile, and a line may be withheld it would have let through). It is
	// also how long the server waits for a write of its own to the host
	// before it closes the connection. And it is how long a link that goes as
	// the connection stops carrying anything can still have a line entered,
	// where that was until the host noticed the connection lost: its socket
	// taking nothing for 2 minutes, TCP's keepalive after about 5, or, with
	// data unacknowledged, retransmissions that give up after many more.
	linkHeardWithin = 15 * time.Second
)

// liveEpoch is the origin of connLive's times, read on the monotonic clock.
var liveEpoch = time.Now()

// connLive is what the host has heard on one control connection: when it
// last read anything there.
type connLive struct {
	// heard is when, as time since liveEpoch.
	heard atomic.Int64
}

// newConnLive is a connection's, heard from now (its upgrade answered).
func newConnLive() *connLive {
	l := &connLive{}
	l.hear()
	return l
}

// hear notes the server heard from now. Each who hears keeps the latest.
func (l *connLive) hear() {
	now := int64(time.Since(liveEpoch))
	for {
		was := l.heard.Load()
		if was >= now || l.heard.CompareAndSwap(was, now) {
			return
		}
	}
}

// quietFor is how long ago the server was last heard from.
func (l *connLive) quietFor() time.Duration {
	return time.Since(liveEpoch) - time.Duration(l.heard.Load())
}

// serverQuiet is why a link's viewer's line is withdrawn while the server
// has not been heard from within its bound: a kind of session.ErrRevoked
// (errors.Is holds), so that the session does with the line what it does
// for a link gone.
type serverQuiet struct{ within time.Duration }

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

// pingWhenQuiet is livePingEvery, or a test's own (livePing).
func (a *agent) pingWhenQuiet() time.Duration {
	if a.livePing > 0 {
		return a.livePing
	}
	return livePingEvery
}

// vouches says why the host cannot vouch now for the links its viewers
// joined through (serverQuiet): no control connection, or one it has heard
// nothing on within heardWithin; nil when it can. It takes no lock: the
// session asks under its own (peer.authorize).
func (a *agent) vouches() error {
	within := a.heardWithin()
	if l := a.live.Load(); l != nil && l.quietFor() <= within {
		return nil
	}
	return serverQuiet{within}
}

// keepHearing pings the server on c, whose hearing is l, each time the
// connection has been quiet for pingWhenQuiet while a link someone joined
// through is held (linkCreds.held), one ping at a time, until done closes
// (the connection's writer ends) or ctx ends. The connection's reader takes
// the pong. A ping that is not answered ends nothing at once: the bound
// does what it must for the links. But once the host has pinged and heard
// nothing at all for writeStall, it gives the connection up, as it does a
// socket that takes nothing for that long: its pings keep a connection that
// drops what it carries from ever being idle, so TCP's own keepalive, which
// would have ended it, never probes it.
func (a *agent) keepHearing(ctx context.Context, c *websocket.Conn, l *connLive, done <-chan struct{}) {
	every := a.pingWhenQuiet()
	giveUp := writeStall
	if a.stallWait > 0 {
		giveUp = a.stallWait
	}
	t := time.NewTicker(max(every/4, time.Millisecond))
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
		quiet := l.quietFor()
		if quiet < every || !a.creds.held() {
			pinging = time.Time{}
			if told && quiet < every {
				told = false
				a.log.Info("the server is heard from again")
			}
			continue
		}
		if heard := l.heard.Load(); pinging.IsZero() || heard != heardThen {
			pinging, heardThen = time.Now(), heard
		}
		if within := a.heardWithin(); quiet > within && !told {
			told = true
			a.log.Warn("the server has not been heard from; the lines of viewers who joined through a link are not entered until it is", "quiet", quiet.Round(time.Second))
		}
		if time.Since(pinging) > giveUp {
			a.log.Warn("the server has not answered the host's pings; giving the control connection up", "quiet", quiet.Round(time.Second))
			c.CloseNow()
			return
		}
		pctx, cancel := context.WithTimeout(ctx, a.heardWithin())
		if c.Ping(pctx) == nil {
			l.hear()
			pinging = time.Time{}
		}
		cancel()
	}
}
