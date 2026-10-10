package hostagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/pion/webrtc/v4"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
)

// A paste invite connects a viewer to a session with no server between them
// at all: the viewer's browser gathers its ICE candidates and hands its
// offer over as a blob (internal/paste), through any messenger; the
// session's side answers with a blob of its own; the viewer pastes that back
// and the data channel opens.

// PasteOptions is what AnswerPaste needs beside the session.
type PasteOptions struct {
	Role session.Role
	// Name is how the viewer is listed on the session, until its hello
	// says more.
	Name string
	// ICE is how the peer gathers (Options.ICE); ICEServers the STUN
	// servers it uses.
	ICE        ICE
	ICEServers []proto.ICEServer
	// Gather bounds the wait for the candidates (5 s when zero).
	Gather time.Duration
	Log    *slog.Logger
}

// PastePeer is the session's side of a paste invite: closed when the
// session ends or the person revokes it.
type PastePeer struct {
	p     *peer
	since time.Time
}

// Close ends the connection.
func (pp *PastePeer) Close() { pp.p.close() }

// Since is when the answer was made.
func (pp *PastePeer) Since() time.Time { return pp.since }

// Connected reports whether the viewer's data channel is open.
func (pp *PastePeer) Connected() bool {
	pp.p.mu.Lock()
	defer pp.p.mu.Unlock()
	return pp.p.dc != nil
}

// AnswerPaste takes a viewer's offer for local and answers it: the answer's
// SDP with every candidate gathered, once gathering is complete or o.Gather
// has passed. The peer serves the viewer as a host's peer does (the hello
// over the data channel, the welcome, the scrollback, input by role).
func AnswerPaste(ctx context.Context, local *session.Local, offerSDP string, o PasteOptions) (*PastePeer, string, error) {
	if o.Log == nil {
		o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if !o.Role.Valid() {
		return nil, "", errors.New("paste: role must be view or control")
	}
	gather := o.Gather
	if gather <= 0 {
		gather = 5 * time.Second
	}
	a := &agent{opts: Options{ICE: o.ICE}, local: local, peers: map[string]*peer{}, log: o.Log}
	// What the peer would send a server (its answer, its candidates) goes
	// nowhere: the answer is read from the peer connection once gathered.
	a.sendHook = func(any) {}
	// The person who pastes the invite back is a guest, not the owner.
	p := newPeer(a, newPasteViewerID(), o.Role, "", "paste", false)
	if err := p.startWebRTC(o.ICEServers); err != nil {
		return nil, "", err
	}
	p.mu.Lock()
	pc := p.pc
	p.mu.Unlock()
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := p.handleOffer(offerSDP); err != nil {
		p.close()
		return nil, "", fmt.Errorf("paste: %w", err)
	}
	select {
	case <-gathered:
	case <-time.After(gather):
	case <-ctx.Done():
		p.close()
		return nil, "", ctx.Err()
	}
	desc := pc.LocalDescription()
	if desc == nil {
		p.close()
		return nil, "", errors.New("paste: no answer")
	}
	return &PastePeer{p: p, since: time.Now()}, desc.SDP, nil
}

// newPasteViewerID is a viewer id for a paste peer (16 hex digits, as the
// server's viewer ids are).
func newPasteViewerID() string {
	return "paste" + session.NewID()[:11]
}
