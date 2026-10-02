package proto

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Control message discriminators (the "t" field).
const (
	// client -> owner
	CtlHello   = "hello"
	CtlResize  = "resize"
	CtlPing    = "ping"
	CtlFileGet = "file_get"
	CtlSubmit  = "submit"

	// owner -> client
	CtlAttention = "attention"
	CtlActivity  = "activity"
	CtlWelcome   = "welcome"
	CtlReady     = "ready"
	CtlStatus    = "status"
	CtlViewers   = "viewers"
	CtlError     = "error"
	CtlPong      = "pong"
)

// Error codes carried by CtlError.
const (
	ErrCodeReadOnly         = "read_only"
	ErrCodeSlowConsumer     = "slow_consumer"
	ErrCodeBadFrame         = "bad_frame"
	ErrCodeHelloTimeout     = "hello_timeout"
	ErrCodeRevoked          = "revoked"
	ErrCodeSessionEnded     = "session_ended"
	ErrCodeHostDisconnected = "host_disconnected"
	ErrCodeFileDenied       = "file_denied"
	ErrCodeTooManyRequests  = "too_many_requests"
)

// Transport labels reported in Welcome.
const (
	TransportWS     = "ws"
	TransportWebRTC = "webrtc"
	TransportRelay  = "relay"
)

// WebSocket close codes.
const (
	CloseProtocolError   = 4400
	CloseUnauthorized    = 4401
	CloseForbidden       = 4403
	CloseNotFound        = 4404
	CloseTooManyViewers  = 4409
	CloseSessionEnded    = 4410
	CloseServerShutdown  = 1001
	CloseNormal          = 1000
	MaxTerminalDimension = 500
)

// Header is the common discriminator of every control and signal message.
type Header struct {
	T string `json:"t"`
}

// MaxNameLen bounds a viewer display name in runes (see session.CleanName).
const MaxNameLen = 40

// Hello is the first message a client sends.
type Hello struct {
	T      string `json:"t"`
	Proto  int    `json:"proto"`
	Cols   uint16 `json:"cols"`
	Rows   uint16 `json:"rows"`
	Client string `json:"client,omitempty"`
	// Name is the display name other viewers see; a label, not authentication.
	Name string `json:"name,omitempty"`
}

// Resize is sent by controllers to change the PTY size and broadcast by the
// owner to every attached client.
type Resize struct {
	T    string `json:"t"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
	By   string `json:"by,omitempty"`
}

// Submit asks the owner to submit Text as a line, as a reply box does: the
// text, as a paste when the program asks for one, then Enter on its own after
// a pause (session.Local.Submit). Controllers only; Text is at most MaxSubmit
// bytes.
type Submit struct {
	T    string `json:"t"`
	Text string `json:"text"`
}

// MaxSubmit bounds the text of a submit message, in bytes: a reply box's line.
const MaxSubmit = 4096

// Ping and Pong carry an opaque client timestamp.
type Ping struct {
	T  string `json:"t"`
	TS int64  `json:"ts"`
}

// FileGet asks the owner to read a file or directory relative to the session cwd.
type FileGet struct {
	T     string `json:"t"`
	ReqID string `json:"reqId"`
	Path  string `json:"path"`
	Stat  bool   `json:"stat,omitempty"`
}

// Welcome is the owner's first message after hello (or before signaling for hosted sessions).
type Welcome struct {
	T               string      `json:"t"`
	Proto           int         `json:"proto"`
	SessionID       string      `json:"sessionId"`
	Role            string      `json:"role"`
	SubscriberID    string      `json:"subscriberId,omitempty"`
	ViewerID        string      `json:"viewerId,omitempty"`
	Cols            uint16      `json:"cols"`
	Rows            uint16      `json:"rows"`
	Status          string      `json:"status"`
	ScrollbackBytes int         `json:"scrollbackBytes"`
	Transport       string      `json:"transport"`
	FileView        bool        `json:"fileView"`
	ICEServers      []ICEServer `json:"iceServers,omitempty"`
	RelayTimeoutMs  int         `json:"relayTimeoutMs,omitempty"`
	RelayOnly       bool        `json:"relayOnly,omitempty"`
}

// ICEServer is the WebRTC ICE server description sent to clients.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Status reports session lifecycle changes.
type Status struct {
	T        string `json:"t"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

// ViewerInfo describes one attached client in the viewers roster.
type ViewerInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
	// Link is the label of the share link the viewer joined through, if any.
	Link  string `json:"link,omitempty"`
	Since string `json:"since"`
	// LastInputAt is refreshed at most every 2 s while the viewer types.
	LastInputAt string `json:"lastInputAt,omitempty"`
}

// Viewers reports the attached clients: the count and the full roster.
type Viewers struct {
	T     string       `json:"t"`
	Count int          `json:"count"`
	List  []ViewerInfo `json:"list,omitempty"`
}

// ErrorMsg is a non-fatal or fatal error notification.
type ErrorMsg struct {
	T       string `json:"t"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorInfo is the nested error object used inside other messages.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AttentionOption is a quick-reply choice; Input is sent verbatim as INPUT.
type AttentionOption struct {
	Label string `json:"label"`
	Input string `json:"input"`
}

// Attention reports whether the agent is waiting for a human. Kind and
// Options describe the prompt's shape (see docs/protocol.md, Attention).
type Attention struct {
	T       string            `json:"t"`
	State   string            `json:"state"`
	Message string            `json:"message,omitempty"`
	Source  string            `json:"source,omitempty"`
	Kind    string            `json:"kind,omitempty"`
	Options []AttentionOption `json:"options,omitempty"`
}

// Activity is one entry of the session activity log (owner -> client). The
// last 50 entries are replayed after ready; new ones follow live. URL, To and
// Tool belong to the event types (docs/protocol.md, Events). The owner bounds
// the text fields (session.CleanEntry) so that the message fits MaxControl.
type Activity struct {
	T       string `json:"t"`
	At      string `json:"at"`
	Type    string `json:"type"`
	By      string `json:"by,omitempty"`
	ByName  string `json:"byName,omitempty"`
	Message string `json:"message,omitempty"`
	URL     string `json:"url,omitempty"`
	To      string `json:"to,omitempty"`
	Tool    string `json:"tool,omitempty"`
}

// Simple is a message with only a discriminator (ready).
type Simple struct {
	T string `json:"t"`
}

// ErrUnknownControl is returned for an unrecognised control discriminator.
var ErrUnknownControl = errors.New("proto: unknown control message")

// ParseHeader extracts the discriminator from a JSON payload.
func ParseHeader(payload []byte) (string, error) {
	var h Header
	if err := json.Unmarshal(payload, &h); err != nil {
		return "", fmt.Errorf("proto: bad control json: %w", err)
	}
	if h.T == "" {
		return "", errors.New("proto: control message missing t")
	}
	return h.T, nil
}

// ValidDimension reports whether a terminal dimension is acceptable.
func ValidDimension(v uint16) bool {
	return v >= 1 && v <= MaxTerminalDimension
}

// HelloSize reports whether a hello's size asks for a size: two dimensions in
// 1–MaxTerminalDimension do, and a controller's hello sets the session to it
// (latest controller wins). (0, 0) is the hello of a viewer that shows the
// session at the session's size, a scaled tile or a quick reply: it follows
// the current size and changes nothing, and so does any other pair (a zero
// with a nonzero, a dimension over 500), as such a hello always did. A resize
// message has no such case: there zero is invalid. The size never decides the
// role, which comes from the token on both transports.
func HelloSize(cols, rows uint16) bool {
	return ValidDimension(cols) && ValidDimension(rows)
}

// NewError builds an encoded error control frame.
func NewError(code, message string) []byte {
	return MustControl(ErrorMsg{T: CtlError, Code: code, Message: message})
}
