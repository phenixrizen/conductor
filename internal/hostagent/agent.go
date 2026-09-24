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
	"path/filepath"
	"strings"
	"sync"
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
		CreatedAt: time.Now().UTC(),
	}
	local := session.NewLocal(info, proc, session.Options{
		ScrollbackBytes: opts.ScrollbackBytes,
		MaxViewers:      opts.MaxViewers,
		FileView:        opts.FileView,
		Transport:       proto.TransportWebRTC,
		Log:             opts.Log,
		OnChange:        a.onLocalChange,
	})
	a.mu.Lock()
	a.local, a.proc = local, proc
	a.mu.Unlock()

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

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
	// Give the control connection a moment to deliver the final status.
	a.flushStatus()
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
		Host:  proto.HostInfo{Name: a.opts.HostName, Version: version.Version},
		Session: proto.HostSession{
			Name: a.opts.Name, AgentID: a.opts.AgentID, Command: a.opts.Argv, Cwd: a.dir, Cols: cols, Rows: rows,
			RelayOnly: a.opts.RelayOnly, AgentToken: a.agentToken,
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
	changed := info.Attention.State != a.lastAttention.State || info.Attention.Message != a.lastAttention.Message
	a.lastAttention = info.Attention
	a.mu.Unlock()
	if changed {
		a.send(proto.HostAttentionMsg{T: proto.HostAttention, SessionID: info.ID, State: string(info.Attention.State), Message: info.Attention.Message, Source: info.Attention.Source})
	}
}

// serveConn processes messages on an established control connection until
// it fails. Peers are closed when the connection ends.
func (a *agent) serveConn(ctx context.Context, c *websocket.Conn) error {
	defer c.CloseNow()
	// Re-send the current attention after a reconnect.
	if att := a.local.Info().Attention; att.State != session.AttentionNone {
		a.send(proto.HostAttentionMsg{T: proto.HostAttention, SessionID: a.sessionID(), State: string(att.State), Message: att.Message, Source: att.Source})
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

// flushStatus waits briefly for the final status message to be sent.
func (a *agent) flushStatus() {
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
		a.addPeer(m.ViewerID, role, m.LinkID)
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
		a.local.SetAttention(session.AttentionState(m.State), m.Message, source)
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

func (a *agent) addPeer(id string, role session.Role, linkID string) {
	a.removePeer(id)
	p := newPeer(a, id, role, linkID)
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
