package hostagent

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/proto"
)

const (
	dcLowThreshold = 512 << 10
	dcHighWater    = 1 << 20
	dcHardLimit    = 4 << 20
	dcSendTimeout  = 30 * time.Second
)

// dcSink writes frames to a WebRTC data channel with backpressure. Frames
// larger than the browser message limit are fragmented into chunks.
type dcSink struct {
	dc     *webrtc.DataChannel
	log    *slog.Logger
	low    chan struct{}
	closed atomic.Bool
	msgID  atomic.Uint32
	once   sync.Once
}

func newDCSink(dc *webrtc.DataChannel, log *slog.Logger) *dcSink {
	s := &dcSink{dc: dc, log: log, low: make(chan struct{}, 1)}
	dc.SetBufferedAmountLowThreshold(dcLowThreshold)
	dc.OnBufferedAmountLow(func() {
		select {
		case s.low <- struct{}{}:
		default:
		}
	})
	return s
}

// Transport labels welcome messages sent through this sink.
func (s *dcSink) Transport() string { return proto.TransportWebRTC }

func (s *dcSink) WriteFrame(frame []byte) error {
	if s.closed.Load() {
		return errors.New("data channel closed")
	}
	parts := proto.Chunk(uint16(s.msgID.Add(1)), frame)
	// One deadline for the whole frame, not one per part: a peer that drains
	// a part just before each wait ran out would stretch a frame of many
	// parts, and a client the session dropped holds its slot until its
	// write returns (session.Sink).
	until := time.Now().Add(dcSendTimeout)
	for _, part := range parts {
		if err := s.waitForRoom(until); err != nil {
			return err
		}
		if err := s.dc.Send(part); err != nil {
			return err
		}
	}
	return nil
}

// buffered returns the bytes the data channel has taken that the other end has
// not acknowledged yet, which closing the connection drops. It is zero for a
// channel that is not open.
func (s *dcSink) buffered() uint64 {
	if s.closed.Load() || s.dc.ReadyState() != webrtc.DataChannelStateOpen {
		return 0
	}
	return s.dc.BufferedAmount()
}

func (s *dcSink) waitForRoom(until time.Time) error {
	return waitForRoom(until, s.dc.BufferedAmount, func() bool {
		// The sink's Close comes after the write (session.Sink), so the wait
		// looks for the channel's own close too: the peer letting go of its
		// viewer, or the viewer closing its channel. A closed channel keeps
		// counting what it held, so the count alone would wait it out.
		return !s.closed.Load() && s.dc.ReadyState() == webrtc.DataChannelStateOpen
	}, s.low)
}

// waitForRoom waits until the channel holds no more than its mark, its
// buffered count over the hard limit, the channel no longer open, or until.
func waitForRoom(until time.Time, buffered func() uint64, open func() bool, low <-chan struct{}) error {
	for {
		n := buffered()
		if n > dcHardLimit {
			return errors.New("data channel backlog too large")
		}
		if n <= dcHighWater {
			return nil
		}
		left := time.Until(until)
		if left <= 0 {
			return errors.New("data channel send timeout")
		}
		select {
		case <-low:
		case <-time.After(min(left, 250*time.Millisecond)):
		}
		if !open() {
			return errors.New("data channel closed")
		}
	}
}

func (s *dcSink) Close(reason error) {
	s.once.Do(func() {
		s.closed.Store(true)
		if reason != nil {
			if code := errorCode(reason); code != "" {
				_ = s.dc.Send(proto.NewError(code, reason.Error()))
			}
		}
		_ = s.dc.Close()
	})
}

// relaySink writes frames through the server relay, on the host's control
// connection, each in its turn among the other relayed viewers' (agent's
// sendRelay): a frame whose turn does not come within relayTurnWait ends
// the viewer's subscription as a slow consumer, the viewer alone.
type relaySink struct {
	a        *agent
	viewerID string
	once     sync.Once
	// gone is closed when the peer lets go of the viewer (let): a frame
	// waiting for its turn is withdrawn then. Nil never closes.
	gone     chan struct{}
	goneOnce sync.Once
}

func newRelaySink(a *agent, viewerID string) *relaySink {
	return &relaySink{a: a, viewerID: viewerID, gone: make(chan struct{})}
}

// let has the sink stop waiting on the viewer's behalf.
func (s *relaySink) let() {
	if s.gone != nil {
		s.goneOnce.Do(func() { close(s.gone) })
	}
}

// Transport labels welcome messages sent through this sink.
func (s *relaySink) Transport() string { return proto.TransportRelay }

func (s *relaySink) WriteFrame(frame []byte) error { return s.a.sendRelay(s.viewerID, frame, s.gone) }

// Close tells the viewer why, when its turn comes in time, and then the
// server that the viewer is closed, however long that waits while the
// connection lasts: a close the server missed would leave the viewer
// attached there, to a session that no longer serves it. It runs on the
// viewer's own goroutine (session.Sink).
func (s *relaySink) Close(reason error) {
	s.once.Do(func() {
		if reason != nil {
			if code := errorCode(reason); code != "" {
				_ = s.a.sendRelay(s.viewerID, proto.NewError(code, reason.Error()), s.gone)
			}
			_ = s.a.tell(proto.ViewerRef{T: proto.HostViewerClose, ViewerID: s.viewerID})
		}
	})
}

func errorCode(reason error) string {
	switch reason.Error() {
	case "session: link revoked":
		return proto.ErrCodeRevoked
	case "session: slow consumer":
		return proto.ErrCodeSlowConsumer
	}
	return ""
}
