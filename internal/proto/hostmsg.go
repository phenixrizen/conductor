package proto

// Host control messages are JSON text frames on /ws/host. Relayed terminal
// frames travel as binary TypeRelay envelopes on the same connection.
const (
	// host -> server
	HostRegister    = "register"
	HostStatus      = "status"
	HostResize      = "resize"
	HostAnswer      = "answer"
	HostICE         = "ice"
	HostViewerError = "viewer_error"
	HostViewerClose = "viewer_closed"
	// both directions
	HostAttention = "attention"
	HostActivity  = "activity"

	// server -> host
	HostRegistered  = "registered"
	HostViewerJoin  = "viewer_join"
	HostOffer       = "offer"
	HostRelayStart  = "relay_start"
	HostViewerLeave = "viewer_leave"
	HostStop        = "stop"
	HostError       = "error"
)

// HostInfo describes the host process.
type HostInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// User is the OS user running the host (≤ 64 bytes), for "hosted by".
	User string `json:"user,omitempty"`
}

// MaxHostUser bounds HostInfo.User.
const MaxHostUser = 64

// HostSession describes the session a host offers.
type HostSession struct {
	Name    string   `json:"name"`
	AgentID string   `json:"agentId"`
	Command []string `json:"command"`
	Cwd     string   `json:"cwd"`
	Cols    uint16   `json:"cols"`
	Rows    uint16   `json:"rows"`
	// RelayOnly tells viewers to skip WebRTC and use the server relay.
	RelayOnly bool `json:"relayOnly,omitempty"`
	// AgentToken authorizes the hosted agent to report attention to the server.
	AgentToken string `json:"agentToken,omitempty"`
	// Branch is the git branch of Cwd when known (≤ 200 bytes).
	Branch string `json:"branch,omitempty"`
}

// HostResume lets a reconnecting host reclaim its session.
type HostResume struct {
	SessionID string `json:"sessionId"`
	Secret    string `json:"secret"`
}

// Register is the host's first message.
type Register struct {
	T       string      `json:"t"`
	Proto   int         `json:"proto"`
	Host    HostInfo    `json:"host"`
	Session HostSession `json:"session"`
	Resume  *HostResume `json:"resume,omitempty"`
}

// Registered acknowledges a registration.
type Registered struct {
	T            string      `json:"t"`
	SessionID    string      `json:"sessionId"`
	Secret       string      `json:"secret"`
	ShareBaseURL string      `json:"shareBaseUrl"`
	Resumed      bool        `json:"resumed"`
	ICEServers   []ICEServer `json:"iceServers,omitempty"`
}

// HostStatusMsg reports the hosted process state.
type HostStatusMsg struct {
	T         string `json:"t"`
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	ExitCode  *int   `json:"exitCode,omitempty"`
}

// HostResizeMsg reports the current PTY size for listings.
type HostResizeMsg struct {
	T         string `json:"t"`
	SessionID string `json:"sessionId"`
	Cols      uint16 `json:"cols"`
	Rows      uint16 `json:"rows"`
}

// ViewerJoin tells the host a viewer has been authenticated.
type ViewerJoin struct {
	T        string `json:"t"`
	ViewerID string `json:"viewerId"`
	Role     string `json:"role"`
	LinkID   string `json:"linkId,omitempty"`
	// LinkLabel is shown in the viewers roster; the host never sees the token.
	LinkLabel string `json:"linkLabel,omitempty"`
}

// ViewerRef addresses one viewer (viewer_leave, relay_start, viewer_closed).
type ViewerRef struct {
	T        string `json:"t"`
	ViewerID string `json:"viewerId"`
}

// ViewerSDP carries an offer or answer for one viewer.
type ViewerSDP struct {
	T        string `json:"t"`
	ViewerID string `json:"viewerId"`
	SDP      string `json:"sdp"`
}

// ViewerICE carries one candidate for one viewer.
type ViewerICE struct {
	T         string       `json:"t"`
	ViewerID  string       `json:"viewerId"`
	Candidate ICECandidate `json:"candidate"`
}

// ViewerError reports a per-viewer failure from the host.
type ViewerError struct {
	T        string `json:"t"`
	ViewerID string `json:"viewerId"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// HostAttentionMsg carries an attention change between host and server.
// SessionID and Source are set by the host; the server omits them.
type HostAttentionMsg struct {
	T         string            `json:"t"`
	SessionID string            `json:"sessionId,omitempty"`
	State     string            `json:"state"`
	Message   string            `json:"message,omitempty"`
	Source    string            `json:"source,omitempty"`
	Kind      string            `json:"kind,omitempty"`
	Options   []AttentionOption `json:"options,omitempty"`
}

// HostActivityMsg carries one activity-log entry between host and server.
// Host to server: an entry the host's session recorded. Server to host: an
// event an agent reported through the events route for the hosted session,
// which the host records like any other. SessionID is set by the host and
// ignored by the server, which takes the session from the connection; the
// server omits it. Entry is bounded by session.CleanEntry so that it fits one
// CONTROL frame (MaxControl); the envelope adds a few dozen bytes, far below
// MaxHostMessage.
//
// State goes host to server only, with an attention entry: the attention
// state the entry records (needs_input, working or done), which the host's
// session hands on with the entry, set with the entry's stamp. The server
// keeps it only on an attention entry and only as one of those three.
type HostActivityMsg struct {
	T         string   `json:"t"`
	SessionID string   `json:"sessionId,omitempty"`
	Entry     Activity `json:"entry"`
	State     string   `json:"state,omitempty"`
}

// HostStopMsg asks the host to stop its process.
type HostStopMsg struct {
	T         string `json:"t"`
	SessionID string `json:"sessionId"`
}

// MaxHostMessage bounds a host control text frame.
const MaxHostMessage = 64 << 10
