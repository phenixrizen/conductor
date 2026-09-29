// Package hostagent implements `conductor host`: it runs a command in a local
// PTY, registers the session with a conductor server over a control
// WebSocket, and serves browser viewers over WebRTC data channels or, as a
// fallback, through the server relay.
package hostagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/proto"
	"github.com/phenixrizen/conductor/internal/pty"
	"github.com/phenixrizen/conductor/internal/session"
	"github.com/phenixrizen/conductor/internal/share"
	"github.com/phenixrizen/conductor/internal/version"
)

// Options configure a host run.
type Options struct {
	ServerURL string // http(s)://conductor.example
	Token     string
	Name      string // session name shown in the UI
	HostName  string // machine label
	AgentID   string
	Argv      []string
	Dir       string
	RelayOnly bool
	// LocalAttach connects Stdin/Stdout to the PTY as a controller.
	LocalAttach bool
	Stdin       *os.File
	Stdout      *os.File
	// ICEServers override the servers handed out by the conductor server.
	ICEServers      []proto.ICEServer
	ScrollbackBytes int
	MaxViewers      int
	FileView        string
	Log             *slog.Logger
	// ReconnectMax bounds the reconnect backoff.
	ReconnectMax time.Duration
	// Registered is called once the first registration succeeds (tests, CLI banner).
	Registered func(sessionID, shareBaseURL string)
}

// Result reports how the hosted process ended.
type Result struct {
	SessionID string
	ExitCode  int
}

// Run hosts opts.Argv until the process exits or ctx is cancelled.
func Run(ctx context.Context, opts Options) (Result, error) {
	if len(opts.Argv) == 0 {
		return Result{}, errors.New("host: no command given")
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.ReconnectMax <= 0 {
		opts.ReconnectMax = 30 * time.Second
	}
	if opts.HostName == "" {
		opts.HostName, _ = os.Hostname()
	}
	if opts.AgentID == "" {
		opts.AgentID = filepath.Base(opts.Argv[0])
	}
	dir := opts.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Result{}, err
	}
	wsURL, err := controlURL(opts.ServerURL)
	if err != nil {
		return Result{}, err
	}
	cols, rows := uint16(80), uint16(24)
	if opts.LocalAttach && opts.Stdin != nil {
		if c, r, err := terminalSize(opts.Stdin); err == nil {
			cols, rows = c, r
		}
	}
	// The agent token lets hooks inside the PTY report attention straight to
	// the server. Register first so the session ID is known before the
	// process starts and can be placed in its environment.
	agentToken, _ := share.NewToken()
	a := &agent{opts: opts, wsURL: wsURL, dir: dir, cols: cols, rows: rows, peers: map[string]*peer{}, log: opts.Log, agentToken: agentToken}
	a.activity = newActivityForwarder(a.sendActivity, opts.Log)
	firstConn, registered, err := a.dialAndRegister(ctx)
	if err != nil {
		return Result{}, err
	}
	notifyURL := strings.TrimRight(opts.ServerURL, "/") + "/api/sessions/" + registered.SessionID + "/attention"
	proc, err := pty.Start(pty.Spec{Argv: opts.Argv, Dir: dir, Env: hostEnv(pty.Inject(registered.SessionID, notifyURL, agentToken)), Cols: cols, Rows: rows})
	if err != nil {
		firstConn.Close(websocket.StatusNormalClosure, "start failed")
		return Result{}, err
	}
	info := session.Info{
		ID:        registered.SessionID,
		Name:      opts.Name,
		Kind:      session.KindHosted,
		AgentID:   opts.AgentID,
		Command:   opts.Argv,
		Cwd:       dir,
		Status:    session.StatusRunning,
		Cols:      cols,
		Rows:      rows,
		HostName:  opts.HostName,
		HostUser:  currentUser(),
		Branch:    session.GitBranch(dir),
		CreatedAt: time.Now().UTC(),
	}
	local := session.NewLocal(info, proc, session.Options{
		ScrollbackBytes: opts.ScrollbackBytes,
		MaxViewers:      opts.MaxViewers,
		FileView:        opts.FileView,
		Transport:       proto.TransportWebRTC,
		Log:             opts.Log,
		OnChange:        a.onLocalChange,
		OnActivity:      a.onLocalActivity,
	})
	a.mu.Lock()
	a.local, a.proc = local, proc
	a.mu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go a.activity.run(runCtx)

	if opts.LocalAttach && opts.Stdin != nil && opts.Stdout != nil {
		restore, err := a.attachLocal(runCtx)
		if err != nil {
			opts.Log.Warn("local attach unavailable", "err", err)
		} else {
			defer restore()
		}
	}

	go a.controlLoop(runCtx, firstConn)

	select {
	case <-local.Ended():
	case <-ctx.Done():
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = local.Stop(stopCtx)
		stopCancel()
	}
	// Give the control connection a moment to deliver the final status and the
	// last activity entries, unless it is gone already: ctx is cancelled when
	// the host is stopped from outside.
	a.settle(ctx)
	cancel()
	a.closeAllPeers()
	exit := proc.Exit()
	return Result{SessionID: a.sessionID(), ExitCode: exit.Code}, nil
}

type agent struct {
	opts  Options
	wsURL string
	dir   string
	cols  uint16
	rows  uint16
	local *session.Local
	proc  *pty.Process
	log   *slog.Logger

	mu      sync.Mutex
	sessID  string
	secret  string
	ice     []proto.ICEServer
	conn    *websocket.Conn
	peers   map[string]*peer
	flushed chan struct{}

	agentToken    string
	lastAttention session.Attention

	// activity carries the local session's activity entries to the server;
	// statusQueued says the session's final (status) entry is in it.
	activity     *activityForwarder
	statusQueued atomic.Bool

	// sendHook replaces the control connection in tests.
	sendHook func(v any)
}

func (a *agent) sessionID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessID
}

func controlURL(server string) (string, error) {
	u, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("host: invalid server url %q", server)
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("host: unsupported scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws/host"
	u.RawQuery = ""
	return u.String(), nil
}

// currentUser returns the OS user name for "hosted by", bounded and never
// empty-on-error: an unknown user is simply omitted.
func currentUser() string {
	name := ""
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	if name == "" {
		name = os.Getenv("USER")
	}
	if i := strings.LastIndexAny(name, `\/`); i >= 0 { // DOMAIN\user on Windows
		name = name[i+1:]
	}
	if len(name) > proto.MaxHostUser {
		name = name[:proto.MaxHostUser]
	}
	return name
}

// hostEnv forwards the developer's full environment (agents need their own
// credentials) minus conductor tokens and loader overrides, then adds the
// per-session variables.
func hostEnv(inject map[string]string) []string {
	var out []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "CONDUCTOR_") || k == "LD_PRELOAD" || k == "LD_LIBRARY_PATH" {
			continue
		}
		out = append(out, kv)
	}
	for k, v := range inject {
		out = append(out, k+"="+v)
	}
	out = append(out, "TERM=xterm-256color", "COLORTERM=truecolor")
	return out
}

// controlLoop serves the initial connection, then keeps a registered control
// connection alive with backoff.
func (a *agent) controlLoop(ctx context.Context, first *websocket.Conn) {
	backoff := time.Second
	c := first
	for {
		if c == nil {
			var err error
			c, _, err = a.dialAndRegister(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				a.log.Warn("re-registration failed; retrying", "err", err, "in", backoff)
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				backoff = min(backoff*2, a.opts.ReconnectMax)
				continue
			}
			backoff = time.Second
		}
		err := a.serveConn(ctx, c)
		c = nil
		if ctx.Err() != nil {
			return
		}
		select {
		case <-a.local.Ended():
			return
		default:
		}
		a.log.Warn("control connection lost; reconnecting", "err", err, "in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, a.opts.ReconnectMax)
	}
}

// dialAndRegister opens the control connection and registers (or resumes)
// the session. On success the connection is stored as the active one.
func (a *agent) dialAndRegister(ctx context.Context) (*websocket.Conn, proto.Registered, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	c, _, err := websocket.Dial(dialCtx, a.wsURL, &websocket.DialOptions{
		HTTPHeader: map[string][]string{"Authorization": {"Bearer " + a.opts.Token}},
	})
	cancel()
	if err != nil {
		return nil, proto.Registered{}, err
	}
	c.SetReadLimit(proto.MaxHostMessage + proto.MaxFrame)

	cols, rows := a.cols, a.rows
	a.mu.Lock()
	if a.local != nil {
		info := a.local.Info()
		cols, rows = info.Cols, info.Rows
	}
	reg := proto.Register{
		T:     proto.HostRegister,
		Proto: proto.ProtoVersion,
		Host:  proto.HostInfo{Name: a.opts.HostName, Version: version.Version, User: currentUser()},
		Session: proto.HostSession{
			Name: a.opts.Name, AgentID: a.opts.AgentID, Command: a.opts.Argv, Cwd: a.dir, Cols: cols, Rows: rows,
			RelayOnly: a.opts.RelayOnly, AgentToken: a.agentToken, Branch: session.GitBranch(a.dir),
		},
	}
	if a.sessID != "" {
		reg.Resume = &proto.HostResume{SessionID: a.sessID, Secret: a.secret}
	}
	a.mu.Unlock()
	fail := func(err error) (*websocket.Conn, proto.Registered, error) {
		c.CloseNow()
		return nil, proto.Registered{}, err
	}
	if err := writeJSON(ctx, c, reg); err != nil {
		return fail(err)
	}
	rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
	typ, data, err := c.Read(rctx)
	rcancel()
	if err != nil {
		return fail(fmt.Errorf("registration: %w", err))
	}
	var registered proto.Registered
	if typ != websocket.MessageText || json.Unmarshal(data, &registered) != nil || registered.T != proto.HostRegistered {
		return fail(errors.New("registration rejected"))
	}
	a.mu.Lock()
	first := a.sessID == ""
	a.sessID = registered.SessionID
	a.secret = registered.Secret
	a.ice = registered.ICEServers
	if len(a.opts.ICEServers) > 0 {
		a.ice = a.opts.ICEServers
	}
	a.conn = c
	a.mu.Unlock()
	a.log.Info("registered with conductor", "session", registered.SessionID, "resumed", registered.Resumed)
	if first && a.opts.Registered != nil {
		a.opts.Registered(registered.SessionID, registered.ShareBaseURL)
	}
	return c, registered, nil
}

// onLocalChange forwards attention changes of the local session to the
// server so listings stay current for hosted sessions.
func (a *agent) onLocalChange(info session.Info) {
	a.mu.Lock()
	changed := info.Attention.State != a.lastAttention.State || info.Attention.Message != a.lastAttention.Message || info.Attention.Kind != a.lastAttention.Kind || len(info.Attention.Options) != len(a.lastAttention.Options)
	a.lastAttention = info.Attention
	a.mu.Unlock()
	if changed {
		a.send(hostAttentionMsg(info.ID, info.Attention))
	}
}

// hostAttentionMsg encodes an attention change for the control connection.
func hostAttentionMsg(sessionID string, att session.Attention) proto.HostAttentionMsg {
	m := proto.HostAttentionMsg{T: proto.HostAttention, SessionID: sessionID, State: string(att.State), Message: att.Message, Source: att.Source, Kind: att.Kind}
	for _, o := range att.Options {
		m.Options = append(m.Options, proto.AttentionOption{Label: o.Label, Input: o.Input})
	}
	return m
}

// onLocalActivity is the local session's OnActivity hook: it queues the entry
// for the server, whose admin stream shows every entry of every session. The
// session calls it on the goroutine that recorded the entry, so it never waits
// for the connection. Entries recorded while the connection is down are lost
// to the stream; the session log has them.
func (a *agent) onLocalActivity(_ string, e session.ActivityEntry) {
	a.activity.push(e)
	if e.Type == session.ActivityStatus {
		a.statusQueued.Store(true)
	}
}

// sendActivity delivers one entry on the control connection.
func (a *agent) sendActivity(e session.ActivityEntry) {
	a.send(hostActivityMsg(a.sessionID(), e))
}

// settle gives the control connection a moment to deliver what the session
// leaves behind when it ends: the final status message, and the activity
// entries queued for the server, the final status entry among them. It gives
// up at once when ctx is cancelled. That is how the host is stopped from
// outside (SIGINT, SIGTERM), and it ends the control connection and the
// forwarder before the session records its last entry, so there is nothing
// left to wait for.
func (a *agent) settle(ctx context.Context) {
	a.flushStatus(ctx)
	a.flushActivity(ctx)
}

// flushActivity gives the entries the local session recorded a moment to
// reach the server before the connection closes. Once the process has ended
// that includes the final status entry, which the session records just after
// it announces the end. See settle for ctx.
func (a *agent) flushActivity(ctx context.Context) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if (!a.local.Info().Status.Ended() || a.statusQueued.Load()) && a.activity.idle() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// hostActivityMsg wraps an entry in the host `activity` message. The host
// names its session; the server ignores it, and takes the connection's.
func hostActivityMsg(sessionID string, e session.ActivityEntry) proto.HostActivityMsg {
	return proto.HostActivityMsg{T: proto.HostActivity, SessionID: sessionID, Entry: session.EntryToProto(e)}
}

// serveConn processes messages on an established control connection until
// it fails. Peers are closed when the connection ends.
func (a *agent) serveConn(ctx context.Context, c *websocket.Conn) error {
	defer c.CloseNow()
	// Re-send the current attention after a reconnect.
	if att := a.local.Info().Attention; att.State != session.AttentionNone {
		a.send(hostAttentionMsg(a.sessionID(), att))
	}
	defer func() {
		a.mu.Lock()
		if a.conn == c {
			a.conn = nil
		}
		a.mu.Unlock()
		a.closeAllPeers()
	}()

	// Report the current status in case the process ended while disconnected.
	go a.watchStatus(ctx, c)

	for {
		typ, data, err := c.Read(ctx)
		if err != nil {
			return err
		}
		if typ == websocket.MessageBinary {
			f, err := proto.Decode(data)
			if err != nil || f.Type != proto.TypeRelay {
				continue
			}
			viewerID, inner, err := proto.DecodeRelay(f.Payload)
			if err != nil {
				continue
			}
			if p := a.peer(viewerID); p != nil {
				p.handleFrame(inner)
			}
			continue
		}
		if err := a.handleControl(ctx, data); err != nil {
			a.log.Warn("host control message", "err", err)
		}
	}
}

func (a *agent) watchStatus(ctx context.Context, c *websocket.Conn) {
	select {
	case <-ctx.Done():
		return
	case <-a.local.Ended():
	}
	info := a.local.Info()
	_ = writeJSON(ctx, c, proto.HostStatusMsg{T: proto.HostStatus, SessionID: info.ID, Status: string(info.Status), ExitCode: info.ExitCode})
	a.mu.Lock()
	if a.flushed == nil {
		a.flushed = make(chan struct{})
		close(a.flushed)
	}
	a.mu.Unlock()
}

// flushStatus waits briefly for the final status message to be sent. It gives
// up at once when ctx is cancelled: watchStatus stops then without sending it.
func (a *agent) flushStatus(ctx context.Context) {
	deadline := time.After(2 * time.Second)
	for {
		a.mu.Lock()
		done := a.flushed
		a.mu.Unlock()
		if done != nil {
			return
		}
		select {
		case <-deadline:
			return
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (a *agent) handleControl(ctx context.Context, data []byte) error {
	t, err := proto.ParseHeader(data)
	if err != nil {
		return err
	}
	switch t {
	case proto.HostViewerJoin:
		var m proto.ViewerJoin
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		role := session.Role(m.Role)
		if !role.Valid() || len(m.ViewerID) != proto.ViewerIDLen {
			return errors.New("invalid viewer_join")
		}
		a.addPeer(m.ViewerID, role, m.LinkID, m.LinkLabel)
	case proto.HostOffer:
		var m proto.ViewerSDP
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		if p := a.peer(m.ViewerID); p != nil {
			if err := p.handleOffer(m.SDP); err != nil {
				if errors.Is(err, errNoWebRTC) {
					// The viewer falls back to the relay on its own timeout.
					a.log.Debug("offer ignored; webrtc disabled", "viewer", m.ViewerID)
				} else {
					a.sendViewerError(m.ViewerID, "webrtc_failed", err.Error())
				}
			}
		}
	case proto.HostICE:
		var m proto.ViewerICE
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		if p := a.peer(m.ViewerID); p != nil {
			p.handleICE(m.Candidate)
		}
	case proto.HostRelayStart:
		var m proto.ViewerRef
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		if p := a.peer(m.ViewerID); p != nil {
			p.startRelay()
		}
	case proto.HostViewerLeave:
		var m proto.ViewerRef
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		a.removePeer(m.ViewerID)
	case proto.HostAttention:
		var m proto.HostAttentionMsg
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		source := m.Source
		if source == "" {
			source = session.SourceAPI
		}
		var opts []session.Option
		for _, o := range m.Options {
			opts = append(opts, session.Option{Label: o.Label, Input: o.Input})
		}
		a.local.SetAttentionFull(session.AttentionState(m.State), m.Message, source, m.Kind, opts)
	case proto.HostActivity:
		// An event an agent reported through the API for this session. It is
		// recorded like any other entry, so the people watching the host see
		// it, and the OnActivity hook reports it back to the server.
		var m proto.HostActivityMsg
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		if !session.ValidEventType(m.Entry.Type) {
			return errors.New("activity of an unknown type")
		}
		a.local.Record(session.EntryFromProto(m.Entry))
	case proto.HostStop:
		a.log.Info("stop requested by server")
		go func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = a.local.Stop(stopCtx)
		}()
	case proto.HostError:
		var m proto.ErrorMsg
		_ = json.Unmarshal(data, &m)
		a.log.Warn("server error", "code", m.Code, "message", m.Message)
	}
	return nil
}

// send delivers a JSON message on the current control connection.
func (a *agent) send(v any) {
	if a.sendHook != nil {
		a.sendHook(v)
		return
	}
	a.mu.Lock()
	c := a.conn
	a.mu.Unlock()
	if c == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := writeJSON(ctx, c, v); err != nil {
		a.log.Debug("control send failed", "err", err)
	}
}

// sendRelay delivers a relayed terminal frame for a viewer.
func (a *agent) sendRelay(viewerID string, frame []byte) error {
	env, err := proto.EncodeRelay(viewerID, frame)
	if err != nil {
		return err
	}
	a.mu.Lock()
	c := a.conn
	a.mu.Unlock()
	if c == nil {
		return errors.New("host: control connection down")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return c.Write(ctx, websocket.MessageBinary, env)
}

func (a *agent) sendViewerError(viewerID, code, msg string) {
	a.send(proto.ViewerError{T: proto.HostViewerError, ViewerID: viewerID, Code: code, Message: msg})
}

func (a *agent) addPeer(id string, role session.Role, linkID, linkLabel string) {
	a.removePeer(id)
	p := newPeer(a, id, role, linkID, linkLabel)
	a.mu.Lock()
	a.peers[id] = p
	ice := a.ice
	a.mu.Unlock()
	if !a.opts.RelayOnly {
		if err := p.startWebRTC(ice); err != nil {
			a.log.Warn("webrtc unavailable for viewer; relay only", "viewer", id, "err", err)
		}
	}
}

func (a *agent) peer(id string) *peer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.peers[id]
}

func (a *agent) removePeer(id string) {
	a.mu.Lock()
	p := a.peers[id]
	delete(a.peers, id)
	a.mu.Unlock()
	if p != nil {
		p.close()
	}
}

func (a *agent) closeAllPeers() {
	a.mu.Lock()
	list := make([]*peer, 0, len(a.peers))
	for _, p := range a.peers {
		list = append(list, p)
	}
	a.peers = map[string]*peer{}
	a.mu.Unlock()
	for _, p := range list {
		p.close()
	}
}

func writeJSON(ctx context.Context, c *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return c.Write(wctx, websocket.MessageText, b)
}

// discard is used when no stdout is configured.
var _ io.Writer = io.Discard
