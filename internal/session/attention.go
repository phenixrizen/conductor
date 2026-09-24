package session

import (
	"strings"
	"time"
)

// AttentionState says whether the agent in a session is waiting for a human.
type AttentionState string

const (
	AttentionNone       AttentionState = ""
	AttentionWorking    AttentionState = "working"
	AttentionNeedsInput AttentionState = "needs_input"
	AttentionDone       AttentionState = "done"
)

// Valid reports whether s is a known state.
func (s AttentionState) Valid() bool {
	switch s {
	case AttentionNone, AttentionWorking, AttentionNeedsInput, AttentionDone:
		return true
	}
	return false
}

// Attention sources.
const (
	SourceAPI   = "api"
	SourceBell  = "bell"
	SourceOSC   = "osc"
	SourceInput = "input"
	SourceAdmin = "admin"
)

// Attention is the current signal state of a session.
type Attention struct {
	State   AttentionState `json:"state"`
	Message string         `json:"message,omitempty"`
	Source  string         `json:"source,omitempty"`
	Since   *time.Time     `json:"since,omitempty"`
}

// MaxAttentionMessage bounds messages from any source.
const MaxAttentionMessage = 500

// Event is an attention signal found in terminal output.
type Event struct {
	Source  string // SourceBell or SourceOSC
	Message string
}

// Scanner finds terminal bells and OSC notification sequences in a byte
// stream. It keeps state across chunks so split sequences are still seen.
//
// Recognised:
//   - a bare BEL (0x07) outside an escape sequence          -> bell
//   - ESC ] 9 ; text (BEL | ESC \)                          -> osc (iTerm2 style)
//   - ESC ] 777 ; notify ; title ; body (BEL | ESC \)        -> osc (urxvt style)
//
// Other OSC sequences (window title, hyperlinks, colours) are skipped; a BEL
// that terminates one of them is not a bell.
type Scanner struct {
	state   scanState
	param   []byte // OSC number and payload, bounded
	dropped bool
}

type scanState int

const (
	scanText   scanState = iota
	scanEsc              // saw ESC
	scanOSC              // inside ESC ] ...
	scanOSCEsc           // inside OSC, saw ESC (expecting \ for ST)
	scanCSI              // inside ESC [ ... until a final byte 0x40-0x7E
)

const maxOSCCapture = 256

// Scan consumes p and returns any events found in it.
func (s *Scanner) Scan(p []byte) []Event {
	var events []Event
	for _, b := range p {
		switch s.state {
		case scanText:
			switch b {
			case 0x07:
				events = append(events, Event{Source: SourceBell})
			case 0x1b:
				s.state = scanEsc
			}
		case scanEsc:
			switch b {
			case ']':
				s.state = scanOSC
				s.param = s.param[:0]
				s.dropped = false
			case '[':
				s.state = scanCSI
			default:
				// Two-byte escapes and anything else end here.
				s.state = scanText
			}
		case scanCSI:
			if b >= 0x40 && b <= 0x7e {
				s.state = scanText
			}
		case scanOSC:
			switch b {
			case 0x07:
				events = s.finishOSC(events)
			case 0x1b:
				s.state = scanOSCEsc
			default:
				if len(s.param) < maxOSCCapture {
					s.param = append(s.param, b)
				} else {
					s.dropped = true
				}
			}
		case scanOSCEsc:
			if b == '\\' {
				events = s.finishOSC(events)
			} else {
				// Not a string terminator; treat ESC as the start of a new sequence.
				s.state = scanEsc
				s.param = s.param[:0]
			}
		}
	}
	return events
}

func (s *Scanner) finishOSC(events []Event) []Event {
	s.state = scanText
	payload := string(s.param)
	num, rest, _ := strings.Cut(payload, ";")
	switch num {
	case "9":
		events = append(events, Event{Source: SourceOSC, Message: trimMessage(rest)})
	case "777":
		parts := strings.SplitN(rest, ";", 3)
		if len(parts) >= 1 && parts[0] == "notify" {
			var msg string
			if len(parts) == 3 {
				msg = strings.TrimSpace(parts[1]) + ": " + strings.TrimSpace(parts[2])
			} else if len(parts) == 2 {
				msg = parts[1]
			}
			events = append(events, Event{Source: SourceOSC, Message: trimMessage(msg)})
		}
	}
	s.param = s.param[:0]
	return events
}

func trimMessage(m string) string {
	m = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, m))
	if len(m) > 200 {
		m = m[:200]
	}
	return m
}

// CleanMessage bounds and strips control characters from a user or agent
// supplied attention message.
func CleanMessage(m string) string {
	m = strings.Map(func(r rune) rune {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			return -1
		}
		return r
	}, m)
	if len(m) > MaxAttentionMessage {
		m = m[:MaxAttentionMessage]
	}
	return strings.TrimSpace(m)
}
