package ext

import (
	"context"
	"strings"
	"time"
)

// MediaRoomProvider adds ICE servers to the configured ones (minted TURN
// credentials, for one) and answers rooms. Its free default adds no server
// and has no room: the configured STUN and TURN entries and the WebSocket
// relay are what connects.
type MediaRoomProvider interface {
	Provider
	// ICEServers answers the servers one connection may use, beside the
	// configured ones: a hosted session's viewer, a host's registration, a
	// paste invite's answer. Safe for concurrent use.
	ICEServers(ctx context.Context, r ICERequest) ([]ICEServer, error)
	// Room answers how to join the room of a piece of work; ok false: none.
	Room(ctx context.Context, r RoomRequest) (RoomJoin, bool, error)
}

// ICERequest is the connection ICE servers are asked for.
type ICERequest struct {
	Purpose string `json:"purpose"` // viewer | host | paste (GET /api/ice's offer never reaches a provider)
	Session string `json:"session,omitempty"`
	Role    string `json:"role,omitempty"`
	Viewer  string `json:"viewer,omitempty"`
}

// ICEServer is one STUN or TURN server, as WebRTC's RTCIceServer.
type ICEServer struct {
	URLs       []string `json:"urls"` // stun:, stuns:, turn: or turns:; 1 to MaxICEURLs of at most MaxICEURL bytes
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"` // each at most MaxICESecret bytes
}

// RoomRequest is a piece of work whose room is asked for.
type RoomRequest struct {
	Kind string    `json:"kind"` // ticket | decision | review | run
	ID   string    `json:"id"`
	By   Principal `json:"by"`
}

// RoomJoin is how to join a room.
type RoomJoin struct {
	URL       string    `json:"url"`
	Token     string    `json:"token,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitzero"`
}

const (
	MaxICEServers = 8 // a provider's, per connection; the configured ones are not counted and never cut
	MaxICEURLs    = 4
	MaxICEURL     = 512
	MaxICESecret  = 256
	ICEWait       = 2 * time.Second
)

// iceSchemes are the URL schemes an ICE server may have.
var iceSchemes = []string{"stun:", "stuns:", "turn:", "turns:"}

// Valid checks s against its bounds: 1 to MaxICEURLs URLs, each of at most
// MaxICEURL bytes of printable ASCII with a stun:, stuns:, turn: or turns:
// scheme, and a username and a credential each of one line of at most
// MaxICESecret bytes.
func (s ICEServer) Valid() error {
	const r = "ice server"
	if len(s.URLs) == 0 || len(s.URLs) > MaxICEURLs {
		return invalid(r, "%d urls, not 1 to %d", len(s.URLs), MaxICEURLs)
	}
	for _, u := range s.URLs {
		switch {
		case len(u) > MaxICEURL:
			return invalid(r, "a url is %d bytes, past %d", len(u), MaxICEURL)
		case !printableASCII(u):
			return invalid(r, "url %q is not printable ASCII without spaces", u)
		case !iceScheme(u):
			return invalid(r, "url %q is not stun:, stuns:, turn: or turns:", u)
		}
	}
	if err := line(r, "username", s.Username, MaxICESecret, false); err != nil {
		return err
	}
	return line(r, "credential", s.Credential, MaxICESecret, false)
}

// iceScheme reports whether u begins with one of iceSchemes and names
// something after it.
func iceScheme(u string) bool {
	for _, p := range iceSchemes {
		if rest, ok := strings.CutPrefix(u, p); ok {
			return rest != ""
		}
	}
	return false
}
