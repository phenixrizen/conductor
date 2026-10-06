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
	// CtlChat is a post from a viewer and, owner -> client, one message of the chat (ChatPost, ChatMessage).
	CtlChat = "chat"
	// CtlChatSend types a kept chat message into the agent (ChatSend); controllers only.
	CtlChatSend = "chat_send"
	// CtlChatRoster lists the people on a run's chat (ChatRoster); owner -> client.
	CtlChatRoster = "chat_roster"

	// owner -> client
	CtlAttention   = "attention"
	CtlActivity    = "activity"
	CtlWelcome     = "welcome"
	CtlReady       = "ready"
	CtlStatus      = "status"
	CtlViewers     = "viewers"
	CtlError       = "error"
	CtlPong        = "pong"
	CtlChatHistory = "chat_history"
)

// Error codes carried by CtlError.
const (
	ErrCodeReadOnly         = "read_only"
	ErrCodeSlowConsumer     = "slow_consumer"
	ErrCodeBadFrame         = "bad_frame"
	ErrCodeHelloTimeout     = "hello_timeout"
	ErrCodeRevoked          = "revoked"
	ErrCodeSessionEnded     = "session_ended"
	ErrCodeRelayOff         = "relay_off" // a switchyard without a relay refused the viewer's relay request
	ErrCodeHostDisconnected = "host_disconnected"
	ErrCodeFileDenied       = "file_denied"
	ErrCodeTooManyRequests  = "too_many_requests"
	// ErrCodeNotSent answers a chat_send whose text could not be typed into
	// the member it names (a run's chat): the message says why.
	ErrCodeNotSent = "not_sent"
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
	// ChatOnly asks for a quiet connection, for a run's chat alone: no
	// scrollback, output or files, not counted among the session's viewers
	// and no join line in its chat; it is on the run's roster. An older
	// owner ignores it and sends output, which the client drops.
	ChatOnly bool `json:"chatOnly,omitempty"`
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

// Chat beside the terminal (docs/protocol.md, Chat): the people on a session
// talk to each other over the connection the terminal takes. A viewer posts
// with `chat`; the owner keeps the last session.MaxChat messages, sends each
// to every viewer as `chat`, and the kept ones to a new viewer as
// `chat_history`. `chat_send` types a kept message into the agent.
const (
	ChatScopeSession = "session"
	ChatScopeRun     = "run"

	ChatKindMessage     = "message"
	ChatKindSystem      = "system"
	ChatKindSentToAgent = "sent_to_agent"

	// ChatToAgent is the `to` of a post that is also typed into this session's agent (controllers only).
	ChatToAgent = "agent"

	// MaxChatText bounds a message's text in bytes, after cleaning. A frame of
	// one message always fits MaxControl: chat frames are encoded without
	// HTML escaping (MustControlRaw), so the worst case doubles it.
	MaxChatText = 2048
	// MaxChatNonce bounds a post's own id, echoed in the message so the sender knows it.
	MaxChatNonce = 32
	// ChatRatePerSecond and ChatBurst bound the posts of one connection.
	ChatRatePerSecond = 10
	ChatBurst         = 20
	// ChatReplayBytes bounds what a new viewer is sent of the kept chat: the newest messages that fit.
	ChatReplayBytes = 128 << 10
	// MaxChatRoster bounds the people a chat_roster lists (a full list of the
	// longest names fits MaxControl); Count says how many there are.
	MaxChatRoster = 32
)

// ChatPost is a viewer's message (client -> owner). Scope is "session" when
// empty. To is "agent" for a controller's post that is also typed into the
// agent, or a member's name in a run's chat. On is the member the sender is
// looking at (a run's chat). Nonce is the client's own id of the post.
type ChatPost struct {
	T     string `json:"t"`
	Nonce string `json:"nonce,omitempty"`
	Scope string `json:"scope,omitempty"`
	Text  string `json:"text"`
	On    string `json:"on,omitempty"`
	To    string `json:"to,omitempty"`
}

// ChatSend asks the owner to type the text of the kept message Ref into the
// agent (client -> owner, controllers only), or into member To of a run.
type ChatSend struct {
	T     string `json:"t"`
	Ref   string `json:"ref"`
	Scope string `json:"scope,omitempty"`
	To    string `json:"to,omitempty"`
}

// ChatBy is who a chat message is from: the viewer's subscription id, name and role.
type ChatBy struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// ChatMessage is one message of a chat (owner -> client). Kind "message"
// carries Text; "system" carries Event (join, leave) about By; "sent_to_agent"
// marks that By typed the message Ref into the agent (To names a run's member).
type ChatMessage struct {
	T     string `json:"t"`
	ID    string `json:"id"`
	At    string `json:"at"`
	Scope string `json:"scope"`
	Kind  string `json:"kind"`
	By    ChatBy `json:"by"`
	Text  string `json:"text,omitempty"`
	Ref   string `json:"ref,omitempty"`
	To    string `json:"to,omitempty"`
	On    string `json:"on,omitempty"`
	Event string `json:"event,omitempty"`
	Nonce string `json:"nonce,omitempty"`
}

// ChatHistory replays the kept messages to a new viewer, oldest first, in
// frames of at most MaxControl; More says another frame follows.
type ChatHistory struct {
	T        string        `json:"t"`
	Scope    string        `json:"scope"`
	Messages []ChatMessage `json:"messages"`
	More     bool          `json:"more,omitempty"`
}

// ChatPerson is one person on a run's chat: a viewer of any of its members,
// one row per name. On is the member they look at, when they look at one.
type ChatPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
	On   string `json:"on,omitempty"`
}

// ChatRoster lists who is on a run's chat (owner -> client), sent to every
// member's viewers whenever any member's roster changes: Count is how many,
// List the first MaxChatRoster of them.
type ChatRoster struct {
	T     string       `json:"t"`
	Scope string       `json:"scope"`
	Count int          `json:"count"`
	List  []ChatPerson `json:"list"`
}

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
	T               string `json:"t"`
	Proto           int    `json:"proto"`
	SessionID       string `json:"sessionId"`
	Role            string `json:"role"`
	SubscriberID    string `json:"subscriberId,omitempty"`
	ViewerID        string `json:"viewerId,omitempty"`
	Cols            uint16 `json:"cols"`
	Rows            uint16 `json:"rows"`
	Status          string `json:"status"`
	ScrollbackBytes int    `json:"scrollbackBytes"`
	Transport       string `json:"transport"`
	FileView        bool   `json:"fileView"`
	// Chat says the owner takes chat and chat_send: a client sends neither
	// to an owner whose welcome lacks it (an older server closes on them).
	Chat bool `json:"chat,omitempty"`
	// RunChat says the session is a run's member with a run chat: `chat` and
	// `chat_send` take scope `run`, and `chat_roster` arrives.
	RunChat        bool        `json:"runChat,omitempty"`
	ICEServers     []ICEServer `json:"iceServers,omitempty"`
	RelayTimeoutMs int         `json:"relayTimeoutMs,omitempty"`
	RelayOnly      bool        `json:"relayOnly,omitempty"`
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
	// Quiet marks a connection for a run's chat alone (hello.chatOnly): it
	// is left out of a session's `viewers` and listed on the run's roster.
	Quiet bool `json:"quiet,omitempty"`
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
	// RequestID names the host's link request an error answers (host
	// control connection), empty otherwise.
	RequestID string `json:"requestId,omitempty"`
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
