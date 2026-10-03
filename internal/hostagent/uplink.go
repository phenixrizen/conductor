package hostagent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
)

// Uplink publishes sessions that already run, a server's local sessions, to
// a rendezvous Conductor through the host protocol: one control connection
// per session, as `conductor host` makes for the process it runs. The
// rendezvous lists the session as hosted, serves its viewers over WebRTC or
// its relay, and mints the share links; attention and activity travel both
// ways through the hooks the caller chains (Published.OnChange, OnActivity).
type Uplink struct {
	ServerURL string
	Token     string
	HostName  string
	RelayOnly bool
	// ICEServers override the rendezvous's.
	ICEServers []proto.ICEServer
	// ICE is how the peers gather (Options.ICE).
	ICE          ICE
	ReconnectMax time.Duration
	Log          *slog.Logger
}

// Published is a session published to the rendezvous.
type Published struct {
	// SessionID is the rendezvous's id for the session; ShareBaseURL the
	// base its links take.
	SessionID    string
	ShareBaseURL string

	a      *agent
	cancel context.CancelFunc
	stop   func()
	done   chan struct{}
	once   sync.Once
}

// Link asks the rendezvous for a share link to the session: its URL there,
// the same as an invite, and the link's id, role, label and expiry.
func (p *Published) Link(ctx context.Context, role string, ttl time.Duration, label string) (proto.LinkCreated, error) {
	return p.a.requestLink(ctx, role, ttl, label)
}

// Publish registers local with the rendezvous (ctx bounds the registration)
// and serves its viewers until the session ends or Stop is called. It
// returns once registered.
func (u *Uplink) Publish(ctx context.Context, local *session.Local) (*Published, error) {
	if local == nil {
		return nil, errors.New("uplink: no session")
	}
	log := u.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	info := local.Info()
	opts := Options{
		ServerURL: u.ServerURL, Token: u.Token, Name: info.Name, HostName: u.HostName, AgentID: info.AgentID,
		Argv: info.Command, Dir: info.Cwd, RelayOnly: u.RelayOnly, ICEServers: u.ICEServers, ICE: u.ICE, ReconnectMax: u.ReconnectMax, Log: log,
	}
	if opts.HostName == "" {
		opts.HostName, _ = os.Hostname()
	}
	if opts.ReconnectMax <= 0 {
		opts.ReconnectMax = 30 * time.Second
	}
	wsURL, err := controlURL(u.ServerURL)
	if err != nil {
		return nil, err
	}
	// The agent token registered with the rendezvous lets an agent report
	// attention there; agents of a published session report to their own
	// server, so this one is used by nobody, and is fresh for every publish.
	agentToken, _ := share.NewToken()
	a := &agent{opts: opts, wsURL: wsURL, dir: info.Cwd, cols: info.Cols, rows: info.Rows, peers: map[string]*peer{}, log: log, agentToken: agentToken, local: local}
	a.activity = newActivityForwarder(a.sendActivity, log)
	conn, registered, err := a.dialAndRegister(ctx)
	if err != nil {
		return nil, err
	}
	// The publication outlives the registration's context: Stop or the
	// session's end are what end it.
	runCtx, cancel := context.WithCancel(context.Background())
	p := &Published{SessionID: registered.SessionID, ShareBaseURL: registered.ShareBaseURL, a: a, cancel: cancel, done: make(chan struct{})}
	stopped := make(chan struct{})
	p.stop = func() { close(stopped) }
	go a.activity.run(runCtx)
	go a.controlLoop(runCtx, conn)
	go func() {
		defer close(p.done)
		select {
		case <-local.Ended():
			a.settle(runCtx)
		case <-stopped:
			// The session goes on here; for the rendezvous it is over. Tell it
			// so, rather than leave it waiting for a host that will not return.
			a.send(proto.HostStatusMsg{T: proto.HostStatus, SessionID: registered.SessionID, Status: string(session.StatusStopped)})
			a.flushViewers(runCtx)
		}
		cancel()
		a.closeAllPeers()
	}()
	return p, nil
}

// OnChange forwards a change of the local session (its attention) to the
// rendezvous; the caller chains it from the session's OnChange hook.
func (p *Published) OnChange(info session.Info) { p.a.onLocalChange(info) }

// OnActivity forwards an activity entry of the local session; the caller
// chains it from the session's OnActivity hook. It never waits.
func (p *Published) OnActivity(id string, e session.ActivityEntry, state session.AttentionState) {
	p.a.onLocalActivity(id, e, state)
}

// ID is the rendezvous's id for the session; Base the base its links take.
func (p *Published) ID() string   { return p.SessionID }
func (p *Published) Base() string { return p.ShareBaseURL }

// Stop ends the publication: the rendezvous sees the session leave. The
// local session itself is left alone.
func (p *Published) Stop() {
	p.once.Do(p.stop)
	<-p.done
}
