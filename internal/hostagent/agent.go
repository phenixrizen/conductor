// Package hostagent implements `conductor host`: it runs a command in a local
// PTY, registers the session with a conductor server over a control
// WebSocket, and serves browser viewers over WebRTC data channels or, as a
// fallback, through the server relay.
package hostagent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/coder/websocket"

	"github.com/phenixrizen/conductor/internal/agents"
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
	// ServerIsOwners says the server this host reports to runs on the
	// owner's own machine (conductor host to a server on localhost): a
	// viewer it sends with no link is then one of the owner's own windows,
	// offered the editor's Neovim. Never so for a switchyard, whose
	// workbench is its operator's, not the owner's: the uplink leaves it off.
	ServerIsOwners bool
	// LocalAttach connects Stdin/Stdout to the PTY as a controller.
	LocalAttach bool
	Stdin       *os.File
	Stdout      *os.File
	// ICEServers override the servers handed out by the conductor server.
	ICEServers []proto.ICEServer
	// ICE is how the peers gather: one UDP port and an address to advertise
	// (a forwarder's), or pion's defaults.
	ICE             ICE
	ScrollbackBytes int
	MaxViewers      int
	FileView        string
	// FileEdit is whether the control role may edit files through the
	// editor's Neovim on this machine: "control" (the default), "off".
	FileEdit string
	// FileDeny gives, for each file request, what no file read, save or
	// editor of the session may reach, even inside its working directory
	// (session.Options.FileDenyFunc): conductor host passes
	// config.LocalFileDenyFunc's, the data directory, the config file and the
	// catalog file of the server on this machine. Nil refuses nothing more.
	FileDeny func() []string
	Log      *slog.Logger
	// Pattern, when set, is matched against the last line of the terminal after
	// 500 ms without output; a match marks the session needs_input. See
	// session.Options.Pattern.
	Pattern *regexp.Regexp
	// Adapter names the hook adapter of the command (`conductor host --agent`).
	// When it is one with a launch route, the hook assets are written to
	// HooksDir and the command is started with the adapter's flags and
	// environment, as the server starts an agent whose signal is "hook". An
	// adapter without one (its agent reads hooks only from its own config)
	// and any other name change nothing and write nothing.
	Adapter string
	// HooksDir is where the host writes the hook assets; empty means
	// agents.HostHooksDir().
	HooksDir string
	// ReconnectMax bounds the reconnect backoff.
	ReconnectMax time.Duration
	// Registered is called once the first registration succeeds (tests, CLI
	// banner), and again when the server lost the session (a restarted
	// switchyard) and the host registered afresh under a new id.
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
	// The flags are part of the command from the start, so the server lists
	// the command as it runs.
	var adapterEnv map[string]string
	opts.Argv, adapterEnv = injectHooks(opts)
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
	// An instance of its own, so a switchyard that restarts gives the session its id back.
	instance, _ := share.NewToken()
	a := &agent{opts: opts, wsURL: wsURL, dir: dir, cols: cols, rows: rows, peers: map[string]*peer{}, log: opts.Log, agentToken: agentToken, instance: instance, localID: "1"}
	a.activity = newActivityForwarder(a.sendActivity, a.sendChat, opts.Log)
	firstConn, registered, err := a.dialAndRegister(ctx)
	if err != nil {
		return Result{}, err
	}
	notifyURL := strings.TrimRight(opts.ServerURL, "/") + "/api/sessions/" + registered.SessionID + "/attention"
	// The binary the hooks run (the one the assets name when injectHooks
	// wrote them), for what the agent runs itself (the skill).
	bin, _ := agents.Binary()
	env := pty.Inject(registered.SessionID, notifyURL, agentToken, bin)
	for k, v := range adapterEnv {
		if _, ok := env[k]; !ok {
			env[k] = v
		}
	}
	proc, err := pty.Start(pty.Spec{Argv: opts.Argv, Dir: dir, Env: hostEnv(env), Cols: cols, Rows: rows})
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
		FileEdit:        opts.FileEdit,
		FileDenyFunc:    opts.FileDeny,
		Transport:       proto.TransportWebRTC,
		Log:             opts.Log,
		OnChange:        a.onLocalChange,
		OnActivity:      a.onLocalActivity,
		OnChat:          a.onLocalChat,
		Pattern:         opts.Pattern,
		WatchGit:        true,
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
	// Give the control connection a moment to deliver the final status, the
	// last activity entries and what the viewers are still to be sent, unless
	// it is gone already: ctx is cancelled when the host is stopped from
	// outside.
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
	// instance and localID fix the session's id on the server across its
	// restarts (proto.HostInfo.Instance); instance is a secret, never logged.
	instance, localID string
	// held is what the server last said it holds of the links minted for
	// the session (registered.links, then link_created and link_revoked);
	// heldKnown is false until a server said. Guarded by mu.
	held      map[string]bool
	heldKnown bool
	// links are the link requests waiting for the server's answer, by
	// request id. Guarded by mu.
	links map[string]chan linkAnswer

	// activity carries the local session's activity entries to the server;
	// statusQueued says the session's final (status) entry is in it.
	activity     *activityForwarder
	statusQueued atomic.Bool

	// sendHook replaces the control connection in tests.
	sendHook func(v any)
	// submitWait replaces submitTimeout in tests.
	submitWait time.Duration
}

// submitDeadline is when a submission arriving now must be done by: its
// wait in its viewer's queue counts, so one that waited out its time behind
// a submission the process does not take is refused, never typed late.
func (a *agent) submitDeadline() time.Time {
	d := submitTimeout
	if a.submitWait > 0 {
		d = a.submitWait
	}
	return time.Now().Add(d)
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
// per-session variables, which win over what the environment says.
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
	// No token, no header: an open host on a switchyard that admits them.
	header := map[string][]string{}
	if a.opts.Token != "" {
		header["Authorization"] = []string{"Bearer " + a.opts.Token}
	}
	dial := &websocket.DialOptions{HTTPHeader: header}
	if a.opts.ServerIsOwners {
		dial.HTTPClient = ownersServerClient()
	}
	c, resp, err := websocket.Dial(dialCtx, a.wsURL, dial)
	cancel()
	if err != nil {
		if refused := refusal(resp); refused != nil {
			return nil, proto.Registered{}, refused
		}
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
		Host:  proto.HostInfo{Name: a.opts.HostName, Version: version.Version, User: currentUser(), Instance: a.instance},
		Session: proto.HostSession{
			Name: a.opts.Name, AgentID: a.opts.AgentID, Command: a.opts.Argv, Cwd: a.dir, Cols: cols, Rows: rows,
			RelayOnly: a.opts.RelayOnly, AgentToken: a.agentToken, Branch: session.GitBranch(a.dir), LocalID: a.localID,
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
		if reg.Resume != nil && websocket.CloseStatus(err) == websocket.StatusCode(proto.CloseNotFound) {
			// The server no longer knows the session (it restarted and keeps
			// hosted sessions in memory): resuming would fail forever, so the
			// next attempt registers afresh, under the same instance and local
			// id, which give the same session id on a server that keeps them.
			a.mu.Lock()
			old := a.sessID
			a.sessID, a.secret = "", ""
			a.mu.Unlock()
			return fail(fmt.Errorf("registration: the server no longer knows session %s; registering afresh", old))
		}
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
	if registered.Links != nil {
		a.held, a.heldKnown = map[string]bool{}, true
		for _, id := range registered.Links {
			a.held[id] = true
		}
	}
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

// onLocalActivity is the local session's OnActivity hook: it queues the entry,
// with the attention state an attention entry records, for the server, whose
// admin stream shows every entry of every session. The session calls it on the
// goroutine that recorded the entry, so it never waits for the connection.
// Entries recorded while the connection is down are lost to the stream; the
// session log has them.
func (a *agent) onLocalActivity(_ string, e session.ActivityEntry, state session.AttentionState) {
	a.activity.push(e, state)
	if e.Type == session.ActivityStatus {
		a.statusQueued.Store(true)
	}
}

// onLocalChat is the local session's OnChat hook: it queues the message for
// the server, whose admin stream shows it to the browsers' unread counts.
// The session calls it on the goroutine that kept the message, so it never
// waits for the connection; a message kept while the connection is down is
// lost to the stream, and the session's chat keeps it.
func (a *agent) onLocalChat(_ string, m session.ChatMessage) {
	a.activity.pushChat(m)
}

// sendChat delivers one chat message on the control connection.
func (a *agent) sendChat(m session.ChatMessage) {
	a.send(proto.HostChatMsg{T: proto.HostChat, SessionID: a.sessionID(), Message: session.ChatToProto(m)})
}

// sendActivity delivers one entry, and the attention state it records, on the
// control connection.
func (a *agent) sendActivity(e session.ActivityEntry, state session.AttentionState) {
	m := hostActivityMsg(a.sessionID(), e)
	m.State = string(state)
	a.send(m)
}

// settle gives the control connection a moment to deliver what the session
// leaves behind when it ends: the final status message, the activity entries
// queued for the server, the final status entry among them, and the frames
// queued for the viewers, whose final status is the last thing a viewer hears
// before its connection closes. It gives up at once when ctx is cancelled. That
// is how the host is stopped from outside (SIGINT, SIGTERM), and it ends the
// control connection and the forwarder before the session records its last
// entry, so there is nothing left to wait for.
func (a *agent) settle(ctx context.Context) {
	a.flushStatus(ctx)
	a.flushActivity(ctx)
	a.flushViewers(ctx)
}

// flushViewers waits for the viewers to be handed the frames queued for them,
// and for the data channels of the WebRTC viewers to have what they were given
// acknowledged. A relay viewer's frames go out on the control connection, which
// closes once settle returns, and a data channel drops what it still holds when
// its connection is closed: a frame that is queued or held by then is lost, and
// the viewer is left without the status that says the session ended. Run it
// after flushActivity: the session announces its end, and only then queues the
// status entry for its viewers.
func (a *agent) flushViewers(ctx context.Context) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if a.local.Drained() && !a.channelsBuffered() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// channelsBuffered reports whether the data channel of a WebRTC viewer still
// holds bytes that the viewer has not acknowledged.
func (a *agent) channelsBuffered() bool {
	a.mu.Lock()
	peers := make([]*peer, 0, len(a.peers))
	for _, p := range a.peers {
		peers = append(peers, p)
	}
	a.mu.Unlock()
	for _, p := range peers {
		if p.buffered() > 0 {
			return true
		}
	}
	return false
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
	case proto.HostLinkCreated:
		var m proto.LinkCreated
		if json.Unmarshal(data, &m) == nil {
			a.noteHeld(m.LinkID, true)
			a.answerLink(m.RequestID, m, nil)
		}
	case proto.HostLinkRevoked:
		var m proto.LinkRevoked
		if json.Unmarshal(data, &m) == nil {
			a.noteHeld(m.LinkID, false)
			a.answerLink(m.RequestID, proto.LinkCreated{LinkID: m.LinkID}, nil)
		}
	case proto.HostRunLinkUpdated:
		var m proto.RunLinkUpdated
		if json.Unmarshal(data, &m) == nil {
			a.answerLink(m.RequestID, proto.LinkCreated{RunID: m.RunID}, nil)
		}
	case proto.HostError:
		var m proto.ErrorMsg
		_ = json.Unmarshal(data, &m)
		if m.RequestID != "" {
			a.answerLink(m.RequestID, proto.LinkCreated{}, fmt.Errorf("%s: %s", m.Code, m.Message))
			return nil
		}
		a.log.Warn("server error", "code", m.Code, "message", m.Message)
	}
	return nil
}

// linkAnswer is what a link request waits for.
type linkAnswer struct {
	link proto.LinkCreated
	err  error
}

// requestLink asks the server for a share link to the session and waits
// for its answer (link_created, or an error naming the request), at most
// until ctx ends.
func (a *agent) requestLink(ctx context.Context, role string, ttl time.Duration, label string) (proto.LinkCreated, error) {
	return a.ask(ctx, func(id string) any {
		return proto.HostLinkMsg{T: proto.HostLink, RequestID: id, Role: role, TTLSeconds: int(ttl / time.Second), Label: label}
	})
}

// requestRevoke asks the server to revoke a link it minted for the session
// and waits for the answer; a link the server does not know answers
// "not_found: …".
func (a *agent) requestRevoke(ctx context.Context, linkID string) error {
	_, err := a.ask(ctx, func(id string) any {
		return proto.HostLinkRevokeMsg{T: proto.HostLinkRevoke, RequestID: id, LinkID: linkID}
	})
	return err
}

// requestRunLink asks the server for one link to the sessions of a run's
// members (proto.HostRunLinkMsg) and waits for link_created.
func (a *agent) requestRunLink(ctx context.Context, role string, ttl time.Duration, label string, run proto.RunGroup) (proto.LinkCreated, error) {
	return a.ask(ctx, func(id string) any {
		return proto.HostRunLinkMsg{T: proto.HostRunLink, RequestID: id, Role: role, TTLSeconds: int(ttl / time.Second), Label: label, Run: run}
	})
}

// requestRunUpdate tells the server a run's members now and waits for
// link_run_updated; a run without links there answers "not_found: …".
func (a *agent) requestRunUpdate(ctx context.Context, run proto.RunGroup) error {
	_, err := a.ask(ctx, func(id string) any {
		return proto.HostRunLinkUpdateMsg{T: proto.HostRunLinkUpdate, RequestID: id, Run: run}
	})
	return err
}

// ask sends the request build makes with a fresh request id and waits for
// the server's answer to it (answerLink), or for ctx.
func (a *agent) ask(ctx context.Context, build func(requestID string) any) (proto.LinkCreated, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return proto.LinkCreated{}, err
	}
	id := hex.EncodeToString(b[:])
	ch := make(chan linkAnswer, 1)
	a.mu.Lock()
	if a.links == nil {
		a.links = map[string]chan linkAnswer{}
	}
	a.links[id] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.links, id)
		a.mu.Unlock()
	}()
	a.send(build(id))
	select {
	case ans := <-ch:
		return ans.link, ans.err
	case <-ctx.Done():
		return proto.LinkCreated{}, ctx.Err()
	}
}

// answerLink hands the server's answer to the request waiting for it.
func (a *agent) answerLink(id string, link proto.LinkCreated, err error) {
	a.mu.Lock()
	ch := a.links[id]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- linkAnswer{link, err}:
		default:
		}
	}
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
	p := newPeer(a, id, role, linkID, linkLabel, linkID == "" && a.opts.ServerIsOwners)
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

// noteHeld keeps held in step with a link the server minted or revoked.
func (a *agent) noteHeld(linkID string, on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.heldKnown || linkID == "" {
		return
	}
	if on {
		a.held[linkID] = true
	} else {
		delete(a.held, linkID)
	}
}

// heldLinks is what the server last said it holds, and whether it said.
func (a *agent) heldLinks() ([]string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.heldKnown {
		return nil, false
	}
	out := make([]string, 0, len(a.held))
	for id := range a.held {
		out = append(out, id)
	}
	return out, true
}

// RefusedError is the rendezvous answering a registration with an HTTP
// error rather than the WebSocket: its status, the code and words of its
// JSON error, and how long it asked to wait (Retry-After), when it said.
type RefusedError struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *RefusedError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Code != "" {
		return fmt.Sprintf("the switchyard refused it (%d %s): %s", e.Status, e.Code, msg)
	}
	return fmt.Sprintf("the switchyard refused it (%d): %s", e.Status, msg)
}

// Retryable says whether the refusal passes with time: 429, a rate or a
// limit of live sessions per address (round 14: a crew launched from a home
// running other sessions met the switchyard's limits and was never
// published), and how long to wait first, when the rendezvous said.
func (e *RefusedError) Retryable() (bool, time.Duration) {
	return e.Status == http.StatusTooManyRequests, e.RetryAfter
}

// refusal reads a refused handshake's response (the WebSocket library keeps
// the first 1024 bytes of its body) into a RefusedError; nil for none.
func refusal(resp *http.Response) *RefusedError {
	if resp == nil || resp.StatusCode == http.StatusSwitchingProtocols || resp.StatusCode < 400 {
		return nil
	}
	e := &RefusedError{Status: resp.StatusCode}
	if resp.Body != nil {
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if b, err := io.ReadAll(io.LimitReader(resp.Body, 1024)); err == nil && json.Unmarshal(b, &body) == nil {
			e.Code, e.Message = body.Error.Code, body.Error.Message
		}
	}
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		e.RetryAfter = time.Duration(min(s, 3600)) * time.Second
	}
	return e
}

// ownersServerClient is the HTTP client for a server taken for the owner's
// (Options.ServerIsOwners): its no-link viewers are the owner's windows only
// while it is this machine's, so the connection never leaves it. No proxy,
// a dial only to a loopback address (checked on the address resolved, so no
// spelling of a name gets past it), and a redirect elsewhere refused.
func ownersServerClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	dialer := &net.Dialer{Timeout: 15 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("host: %s is not this machine", address)
		}
		return nil
	}}
	t.DialContext = dialer.DialContext
	return &http.Client{Transport: t, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if !loopbackHost(req.URL.Hostname()) {
			return fmt.Errorf("host: refusing a redirect off this machine to %s", req.URL.Host)
		}
		return nil
	}}
}

// LoopbackServer reports whether a server URL names this machine (localhost
// or a loopback address): its workbench is the owner's, so a viewer it sends
// with no link is one of the owner's own windows (Options.ServerIsOwners).
func LoopbackServer(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && loopbackHost(u.Hostname())
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
