package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/phenixrizen/conductor/internal/proto"
)

// Chat beside the terminal (docs/protocol.md, Chat): the people on a session
// talk to each other over the connection the terminal takes, so it works
// wherever a share link works. Every viewer may post, whatever its role; a
// controller may also have a message typed into the agent (ChatSend), which
// leaves a marker in the chat. The session keeps the last MaxChat messages
// and replays them to a new viewer; they go with the session. A chat line
// is not an activity entry: it has its own ring, frames and hook (OnChat).

// MaxChat is how many messages a session keeps.
const MaxChat = 200

var (
	ErrChatEmpty       = errors.New("chat: nothing to say")
	ErrChatTooLong     = errors.New("chat: message too long")
	ErrChatRateLimited = errors.New("chat: too many messages")
	ErrChatUnknownRef  = errors.New("chat: no such message")
	ErrChatBadScope    = errors.New("chat: no such chat here")
)

// ChatBy is who a message is from: the viewer's subscription, as the roster names it.
type ChatBy struct {
	ID   string
	Name string
	Role Role
}

// ChatMessage is one message of a chat (proto.ChatMessage, with the time as a time).
type ChatMessage struct {
	ID    string
	At    time.Time
	Scope string
	Kind  string
	By    ChatBy
	Text  string
	Ref   string
	To    string
	On    string
	Event string
	Nonce string
}

// ChatToProto encodes m as the `chat` message of docs/protocol.md.
func ChatToProto(m ChatMessage) proto.ChatMessage {
	return proto.ChatMessage{T: proto.CtlChat, ID: m.ID, At: m.At.UTC().Format(time.RFC3339Nano), Scope: m.Scope, Kind: m.Kind,
		By: proto.ChatBy{ID: m.By.ID, Name: m.By.Name, Role: string(m.By.Role)}, Text: m.Text, Ref: m.Ref, To: m.To, On: m.On, Event: m.Event, Nonce: m.Nonce}
}

// CleanChatText is a message's text as kept: valid UTF-8, line breaks as
// \n, every other control character but a tab dropped, trimmed.
func CleanChatText(s string) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// cleanNonce keeps a post's own id as printable ASCII of at most MaxChatNonce bytes.
func cleanNonce(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > ' ' && r < 0x7f && b.Len() < proto.MaxChatNonce {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// chatRing keeps the last MaxChat messages. The owner's lock guards it.
type chatRing struct {
	buf []ChatMessage
}

func (r *chatRing) add(m ChatMessage) {
	r.buf = append(r.buf, m)
	if len(r.buf) > MaxChat {
		r.buf = r.buf[len(r.buf)-MaxChat:]
	}
}

func (r *chatRing) find(id string) (ChatMessage, bool) {
	for i := len(r.buf) - 1; i >= 0; i-- {
		if r.buf[i].ID == id {
			return r.buf[i], true
		}
	}
	return ChatMessage{}, false
}

func (r *chatRing) snapshot() []ChatMessage {
	return append([]ChatMessage(nil), r.buf...)
}

// chatReplayFrames packs the newest messages that fit proto.ChatReplayBytes
// into chat_history frames of at most proto.MaxControl each, oldest first,
// More set on every frame but the last. None for no messages.
func chatReplayFrames(scope string, msgs []ChatMessage) [][]byte {
	budget := proto.ChatReplayBytes
	start := len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		n := len(proto.MustControlRaw(ChatToProto(msgs[i])))
		if n > budget {
			break
		}
		budget -= n
		start = i
	}
	kept := msgs[start:]
	if len(kept) == 0 {
		return nil
	}
	var batches [][]proto.ChatMessage
	var batch []proto.ChatMessage
	for _, m := range kept {
		next := append(batch, ChatToProto(m))
		if len(batch) > 0 && len(proto.MustControlRaw(proto.ChatHistory{T: proto.CtlChatHistory, Scope: scope, Messages: next, More: true})) > proto.MaxControl {
			batches = append(batches, batch)
			next = []proto.ChatMessage{ChatToProto(m)}
		}
		batch = next
	}
	batches = append(batches, batch)
	frames := make([][]byte, 0, len(batches))
	for i, b := range batches {
		frames = append(frames, proto.MustControlRaw(proto.ChatHistory{T: proto.CtlChatHistory, Scope: scope, Messages: b, More: i < len(batches)-1}))
	}
	return frames
}

// chatBucket bounds one connection's posts: proto.ChatBurst at once, then
// proto.ChatRatePerSecond. A clock that steps back earns nothing.
type chatBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func (b *chatBucket) take(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.last.IsZero():
		b.tokens, b.last = proto.ChatBurst, now
	case now.After(b.last):
		b.tokens = min(proto.ChatBurst, b.tokens+now.Sub(b.last).Seconds()*proto.ChatRatePerSecond)
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func chatBy(sub *Subscription) ChatBy {
	return ChatBy{ID: sub.ID, Name: sub.Name, Role: sub.Role}
}

// Chat keeps and sends a viewer's post to every viewer, and returns it as
// kept. Every role may post. With To "agent" (a controller's) the caller
// goes on with ChatSend on the message. An ended session takes no post.
func (s *Local) Chat(sub *Subscription, post proto.ChatPost) (ChatMessage, error) {
	if !sub.chat.take(time.Now()) {
		return ChatMessage{}, ErrChatRateLimited
	}
	scope := post.Scope
	if scope == "" {
		scope = proto.ChatScopeSession
	}
	if scope != proto.ChatScopeSession {
		return ChatMessage{}, ErrChatBadScope
	}
	if post.To != "" {
		if post.To != proto.ChatToAgent {
			return ChatMessage{}, ErrChatBadScope
		}
		if sub.Role != RoleControl {
			return ChatMessage{}, ErrReadOnly
		}
	}
	text := CleanChatText(post.Text)
	if text == "" {
		return ChatMessage{}, ErrChatEmpty
	}
	if len(text) > proto.MaxChatText {
		return ChatMessage{}, ErrChatTooLong
	}
	m := ChatMessage{ID: NewID(), At: time.Now().UTC(), Scope: scope, Kind: proto.ChatKindMessage, By: chatBy(sub), Text: text, Nonce: cleanNonce(post.Nonce)}
	s.mu.Lock()
	if s.info.Status.Ended() {
		s.mu.Unlock()
		return ChatMessage{}, ErrSessionEnded
	}
	id := s.keepChat(m)
	s.mu.Unlock()
	s.chatHook(id, m)
	return m, nil
}

// ChatSend types the text of the kept message Ref into the agent, as a reply
// box does (Submit), and marks it in the chat. Controllers only.
func (s *Local) ChatSend(ctx context.Context, sub *Subscription, send proto.ChatSend) error {
	if sub.Role != RoleControl {
		return ErrReadOnly
	}
	if send.Scope != "" && send.Scope != proto.ChatScopeSession {
		return ErrChatBadScope
	}
	s.mu.Lock()
	ref, ok := s.chat.find(send.Ref)
	s.mu.Unlock()
	if !ok || ref.Kind != proto.ChatKindMessage {
		return ErrChatUnknownRef
	}
	if _, err := s.Submit(ctx, Submission{Text: ref.Text, By: sub}); err != nil {
		return err
	}
	m := ChatMessage{ID: NewID(), At: time.Now().UTC(), Scope: proto.ChatScopeSession, Kind: proto.ChatKindSentToAgent, By: chatBy(sub), Ref: ref.ID}
	s.mu.Lock()
	id := s.keepChat(m)
	s.mu.Unlock()
	s.chatHook(id, m)
	return nil
}

// ChatHistory is the kept chat, oldest first.
func (s *Local) ChatHistory() []ChatMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.chat.snapshot()
}

// keepChat adds m to the ring and sends it to every viewer, in one critical
// section (as record does), so a viewer attaching meanwhile finds it in its
// replay or receives it, never both. The caller holds s.mu; the session's
// id comes back for the hook, which runs without the lock.
func (s *Local) keepChat(m ChatMessage) string {
	s.chat.add(m)
	s.hub.Broadcast(proto.MustControlRaw(ChatToProto(m)))
	return s.info.ID
}

func (s *Local) chatHook(id string, m ChatMessage) {
	if s.opts.OnChat != nil {
		s.opts.OnChat(id, m)
	}
}

// chatSystem keeps a join or leave line about sub, unless another connection
// of the same name is there already (a join) or still (a leave): a person on
// two tabs joins and leaves once. The caller holds s.mu; the line, if any,
// comes back for the hook.
func (s *Local) chatSystem(sub *Subscription, event string) (ChatMessage, bool) {
	if s.hub.namePresent(sub.Name, sub.ID) {
		return ChatMessage{}, false
	}
	m := ChatMessage{ID: NewID(), At: time.Now().UTC(), Scope: proto.ChatScopeSession, Kind: proto.ChatKindSystem, By: chatBy(sub), Event: event}
	s.keepChat(m)
	return m, true
}

// ChatErrorFrame is the error frame a chat post or send gets for err, naming
// the post's nonce or the send's ref as its requestId.
func ChatErrorFrame(err error, requestID string) []byte {
	code, msg := "input_failed", "could not be delivered"
	switch {
	case errors.Is(err, ErrChatRateLimited):
		code, msg = proto.ErrCodeTooManyRequests, "too many messages at once"
	case errors.Is(err, ErrReadOnly):
		code, msg = proto.ErrCodeReadOnly, "this link is view-only"
	case errors.Is(err, ErrSessionEnded):
		code, msg = proto.ErrCodeSessionEnded, "the session has ended"
	case errors.Is(err, ErrChatEmpty), errors.Is(err, ErrChatTooLong), errors.Is(err, ErrChatUnknownRef), errors.Is(err, ErrChatBadScope):
		code, msg = proto.ErrCodeBadFrame, err.Error()
	}
	return proto.MustControl(proto.ErrorMsg{T: proto.CtlError, Code: code, Message: msg, RequestID: requestID})
}
