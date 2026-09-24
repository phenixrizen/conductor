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
	for _, part := range parts {
		if err := s.waitForRoom(); err != nil {
			return err
		}
		if err := s.dc.Send(part); err != nil {
			return err
		}
	}
	return nil
}

func (s *dcSink) waitForRoom() error {
	deadline := time.NewTimer(dcSendTimeout)
	defer deadline.Stop()
	for {
		buffered := s.dc.BufferedAmount()
		if buffered > dcHardLimit {
			return errors.New("data channel backlog too large")
		}
		if buffered <= dcHighWater {
			return nil
		}
		select {
		case <-s.low:
		case <-deadline.C:
			return errors.New("data channel send timeout")
		case <-time.After(250 * time.Millisecond):
		}
		if s.closed.Load() {
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

// relaySink writes frames through the server relay.
type relaySink struct {
	a        *agent
	viewerID string
	once     sync.Once
}

// Transport labels welcome messages sent through this sink.
func (s *relaySink) Transport() string { return proto.TransportRelay }

func (s *relaySink) WriteFrame(frame []byte) error { return s.a.sendRelay(s.viewerID, frame) }

func (s *relaySink) Close(reason error) {
	s.once.Do(func() {
		if reason != nil {
			if code := errorCode(reason); code != "" {
				_ = s.a.sendRelay(s.viewerID, proto.NewError(code, reason.Error()))
			}
			s.a.send(proto.ViewerRef{T: proto.HostViewerClose, ViewerID: s.viewerID})
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
