package proto

// Signal message discriminators exchanged between a viewer and the server
// (frame type TypeSignal) for hosted sessions.
const (
	SigOffer   = "offer"    // viewer -> server -> host
	SigAnswer  = "answer"   // host -> server -> viewer
	SigICE     = "ice"      // both directions
	SigRelay   = "relay"    // viewer -> server: give up on WebRTC
	SigRelayOK = "relay_ok" // server -> viewer: relay mode active
	SigError   = "error"    // server -> viewer
)

// SDP carries a session description.
type SDP struct {
	T   string `json:"t"`
	SDP string `json:"sdp"`
}

// ICECandidate mirrors RTCIceCandidateInit.
type ICECandidate struct {
	Candidate     string  `json:"candidate"`
	SDPMid        *string `json:"sdpMid,omitempty"`
	SDPMLineIndex *uint16 `json:"sdpMLineIndex,omitempty"`
}

// ICE carries one trickle candidate.
type ICE struct {
	T         string       `json:"t"`
	Candidate ICECandidate `json:"candidate"`
}

// RelayRequest is sent by a viewer to fall back to the server relay.
type RelayRequest struct {
	T      string `json:"t"`
	Reason string `json:"reason"`
}

// MaxICECandidate bounds a single candidate line.
const MaxICECandidate = 2048
